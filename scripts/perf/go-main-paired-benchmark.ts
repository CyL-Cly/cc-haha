#!/usr/bin/env bun
import { appendFileSync, existsSync, lstatSync, mkdirSync, readFileSync, readdirSync, realpathSync, writeFileSync } from 'node:fs'
import { dirname, isAbsolute, join, relative, resolve, sep } from 'node:path'
import { createHash } from 'node:crypto'
import { PAIRED_PROTOCOL, SCENARIOS, evaluatePairs, sha256, taskFor } from './go-main-paired-benchmark-core'
import { commitCompletedPair, createStreamEvidence, freezeResolvedDependencies, invalidateBatch, loadCompletedPairs, requireValidBatch, sealEngineEvidence, validateBatchStartup, validateEngineEvidence, verifyCandidateEvidence, verifySourceParentEvidence, type ProvenPair } from './go-main-paired-benchmark-evidence'

const argv = process.argv.slice(2)
function argument(name: string) {
  const index = argv.indexOf(name)
  const value = argv[index + 1]
  if (index < 0 || !value || value.startsWith('--')) throw new Error('Missing ' + name)
  return realpathSync(value)
}
function inventory(root: string) {
  const rows: [string, string][] = []
  const visit = (directory: string) => {
    for (const entry of readdirSync(directory, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name))) {
      if (entry.name === 'node_modules' || entry.name === '.git') continue
      const path = join(directory, entry.name)
      if (entry.isSymbolicLink()) throw new Error('Unexpected Source symlink: ' + path)
      if (entry.isDirectory()) visit(path)
      else if (entry.isFile()) rows.push([relative(root, path), sha256(readFileSync(path))])
    }
  }
  for (const name of ['src', 'adapters', 'stubs']) visit(join(root, name))
  for (const name of ['preload.ts', 'package.json', 'bun.lock', 'tsconfig.json']) rows.push([name, sha256(readFileSync(join(root, name)))])
  return { hash: sha256(JSON.stringify(rows)), rows }
}
function inside(root: string, path: string) {
  const value = relative(root, path)
  return value && value !== '..' && !value.startsWith('..' + sep) && !isAbsolute(value)
}
async function command(command: string[], cwd: string, env: Record<string, string>, timeout: number, input?: string) {
  const started = performance.now()
  // The pinned Bun setsid() contract makes this PID the owned POSIX PGID.
  // Never signal the benchmark's own group or retain a PID for a later task.
  const child = Bun.spawn(command, { cwd, env, detached: true, stdin: input === undefined ? 'ignore' : new TextEncoder().encode(input), stdout: 'pipe', stderr: 'pipe' })
  const stdoutReader = child.stdout.getReader(), stderrReader = child.stderr.getReader()
  const recordingErrors: string[] = []
  let timedOut = false, stopping = false, retiredGroup = false, joined = false, closing = false
  let stdoutComplete = false, stderrComplete = false, forciblyClosed = false
  const closures: Promise<unknown>[] = []
  let force: ReturnType<typeof setTimeout> | undefined, drain: ReturnType<typeof setTimeout> | undefined
  const signalOwned = (signal: 'SIGTERM' | 'SIGKILL') => {
    if (retiredGroup) return
    try {
      if (process.platform === 'win32') child.kill(signal)
      else process.kill(-child.pid, signal)
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code === 'ESRCH') retiredGroup = true
      else recordingErrors.push('Owned process cleanup failed: ' + String(error))
    }
  }
  const cancelReaders = () => {
    if (!stdoutComplete || !stderrComplete) forciblyClosed = true
    if (!stdoutComplete) closures.push(stdoutReader.cancel().catch(error => { recordingErrors.push('stdout close failed: ' + String(error)) }))
    if (!stderrComplete) closures.push(stderrReader.cancel().catch(error => { recordingErrors.push('stderr close failed: ' + String(error)) }))
  }
  const stop = () => {
    if (stopping || closing) return
    stopping = true
    signalOwned('SIGTERM')
    force = setTimeout(() => {
      signalOwned('SIGKILL')
      // SIGKILL should close the actual owned group's inherited descriptors.
      // A pipe that still will not close is a cleanup/evidence failure, never
      // a successful task obtained merely by cancelling its reader.
      drain = setTimeout(() => {
        if (!stdoutComplete || !stderrComplete) {
          recordingErrors.push('Owned output pipes stayed open after process-group SIGKILL')
          cancelReaders()
        }
      }, 1_000)
    }, 5_000)
  }
  const read = async (reader: ReadableStreamDefaultReader<Uint8Array>, complete: () => void) => {
    const chunks: Uint8Array[] = []
    let length = 0
    try {
      while (true) {
        const next = await reader.read()
        if (next.done) { if (!forciblyClosed) complete(); break }
        const bytes = new Uint8Array(next.value)
        chunks.push(bytes); length += bytes.length
      }
    } catch (error) {
      recordingErrors.push('Command output read failed: ' + String(error))
      stop()
    } finally { reader.releaseLock() }
    const bytes = new Uint8Array(length)
    let offset = 0
    for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.length }
    return new TextDecoder().decode(bytes)
  }
  const timer = setTimeout(() => { timedOut = true; stop() }, timeout)
  const output = [read(stdoutReader, () => { stdoutComplete = true }), read(stderrReader, () => { stderrComplete = true })] as const
  try {
    const [stdout, stderr, code] = await Promise.all([...output, child.exited.then(code => { joined = true; return code })])
    await Promise.all(closures)
    const ended = performance.now()
    return { stdout, stderr, code, timedOut, startedMonotonicMs: started, endedMonotonicMs: ended, durationMs: ended - started, processGroup: process.platform === 'win32' ? null : child.pid, signal: child.signalCode, joined, stdoutComplete, stderrComplete, recordingErrors }
  } finally {
    closing = true
    // No delayed signal may outlive this command's joined/closed lifetime.
    clearTimeout(timer)
    if (force) clearTimeout(force)
    if (drain) clearTimeout(drain)
    if (!joined) {
      signalOwned('SIGKILL')
      cancelReaders()
      await Promise.allSettled([...output, ...closures, child.exited])
    }
  }
}

async function main() {
  // Preparing freezes the judgment before any scored provider call. A caller
  // must explicitly pass --allow-live to spend quota; CI never invokes this.
  const allocation = argument('--allocation')
  if (!allocation.startsWith('/private/tmp/') && !allocation.startsWith('/tmp/')) throw new Error('Benchmark needs an explicit disposable allocation')
  if (!lstatSync(allocation).isDirectory() || (lstatSync(allocation).mode & 0o077) !== 0) throw new Error('Private allocation required')
  return validateBatchStartup(allocation, async () => {
  const source = argument('--source-root'), binary = argument('--go-binary'), parent = argument('--parent-transcript')
  if ((lstatSync(parent).mode & 0o077) !== 0) throw new Error('Private transcript permissions required')
  const frozenSource = inventory(source), binaryHash = sha256(readFileSync(binary)), parentBytes = readFileSync(parent)
  const listing = Bun.spawn(['git', 'ls-tree', '-rz', PAIRED_PROTOCOL.main, '--', 'src', 'adapters', 'stubs', 'preload.ts', 'package.json', 'bun.lock', 'tsconfig.json'], { cwd: resolve(import.meta.dir, '../..'), stdout: 'pipe', stderr: 'pipe' })
  const [tree, treeError, treeCode] = await Promise.all([new Response(listing.stdout).text(), new Response(listing.stderr).text(), listing.exited])
  if (treeCode !== 0) throw new Error('Cannot verify frozen MAIN identity: ' + treeError)
  const expectedPaths = new Set<string>()
  for (const entry of tree.split('\0').filter(Boolean)) {
    const match = /^(100644|100755) blob ([0-9a-f]{40})\t(.+)$/.exec(entry)
    if (!match) throw new Error('Unsupported frozen MAIN tree entry')
    const bytes = readFileSync(join(source, match[3]))
    const blob = createHash('sha1').update(`blob ${bytes.length}\0`).update(bytes).digest('hex')
    if (blob !== match[2]) throw new Error('Source is not frozen MAIN: ' + match[3])
    expectedPaths.add(match[3])
  }
  if (frozenSource.rows.some(([path]) => !expectedPaths.delete(path)) || expectedPaths.size) throw new Error('Missing or extra frozen MAIN inputs')
  const wrapper = resolve(import.meta.dir, 'go-main-source-live.ts')
  const coreFile = resolve(import.meta.dir, 'go-main-paired-benchmark-core.ts')
  const evidenceFile = resolve(import.meta.dir, 'go-main-paired-benchmark-evidence.ts')
  const runnerHash = sha256(readFileSync(import.meta.path)), coreHash = sha256(readFileSync(coreFile))
  const dependencies = freezeResolvedDependencies(source)
  const evidenceHash = sha256(readFileSync(evidenceFile))
  const sdkPackage = JSON.parse(readFileSync(join(source, 'node_modules/@anthropic-ai/sdk/package.json'), 'utf8'))
  const axiosPackage = JSON.parse(readFileSync(join(source, 'node_modules/axios/package.json'), 'utf8'))
  if (sdkPackage.version !== '0.128.0' || axiosPackage.version !== '1.15.2' || Bun.version !== '1.3.14' || Bun.revision !== '0d9b296af33f2b851fcbf4df3e9ec89751734ba4') throw new Error('Pinned SDK/axios/Bun mismatch')
  const pilot = argv.includes('--pilot')
  const cases = SCENARIOS.flatMap(scenario => Array.from({ length: pilot ? 1 : PAIRED_PROTOCOL.repeats }, (_, repeat) => taskFor(scenario, repeat)))
  requireValidBatch(allocation)
  const candidateFile = argv.includes('--candidate-evidence') ? argument('--candidate-evidence') : undefined
  const candidate = candidateFile ? verifyCandidateEvidence(candidateFile, binaryHash, process.platform + '/' + process.arch) : null
  const parentReceipt = argv.includes('--parent-receipt') ? argument('--parent-receipt') : undefined
  if (argv.includes('--allow-live') && !candidate) throw new Error('--allow-live requires completed ordinary build, quality, React and native candidate evidence; use a fresh allocation after preparation')
  if (argv.includes('--allow-live') && !parentReceipt) throw new Error('--allow-live requires the final real Source --parent-receipt')
  const parentEvidence = parentReceipt && candidate ? verifySourceParentEvidence(parentReceipt, parent, { candidate, source: { root: source, rows: frozenSource.rows }, dependenciesHash: dependencies.hash, wrapperHash: sha256(readFileSync(wrapper)) }) : null
  const manifest = { evidenceVersion: 2, candidate, parentEvidence, protocol: PAIRED_PROTOCOL, phase: pilot ? 'unscored setup pilot' : 'formal batch', cases, source: frozenSource, dependencies, binaryHash, parentHash: sha256(parentBytes), wrapperHash: sha256(readFileSync(wrapper)), runnerHash, coreHash, evidenceHash, sdk: sdkPackage.version, axios: axiosPackage.version, bun: Bun.version, revision: Bun.revision, platform: process.platform, architecture: process.arch, bindings: ['Absent OS MDM', 'Absent keychain prefetch', 'Original private plaintext credentials'], tools: 'Original default complete tool set; no --bare, --tools or custom system prompt', ordering: 'Alternating Source/Go and Go/Source by pair index', scoring: 'One final batch of 120 unique pairs; pilots and partial batches cannot pass; no retrospective retry/exclusion' }
  const manifestFile = join(allocation, 'manifest.json'), manifestText = JSON.stringify(manifest, null, 2) + '\n'
  if (existsSync(manifestFile)) {
    if (readFileSync(manifestFile, 'utf8') !== manifestText) { invalidateBatch(allocation, 'Candidate or frozen protocol changed'); throw new Error('Candidate or frozen protocol changed; use a fresh allocation') }
  } else writeFileSync(manifestFile, manifestText, { flag: 'wx', mode: 0o600 })
  writeFileSync(join(allocation, 'source-inventory.json'), JSON.stringify(frozenSource, null, 2), { mode: 0o600 })
  if (!argv.includes('--allow-live')) {
    console.log({ status: 'frozen-not-run', manifest: manifestFile, hash: sha256(manifestText) })
    return
  }
  if (!candidate || !parentEvidence) throw new Error('Qualified final candidate and real Source parent evidence required before live calls')
  const keyFile = argument('--key-file')
  if ((lstatSync(keyFile).mode & 0o077) !== 0) throw new Error('Private key-file permissions required')
  const key = readFileSync(keyFile, 'utf8').trim()
  if (!key) throw new Error('Empty explicitly supplied key')
  const redact = (value: string) => value.replaceAll(key, '<redacted-key>')
  const manifestHash = sha256(manifestText)
  let pairs: ProvenPair[]
  const checkInputs = () => {
    if (existsSync(join(allocation, 'invalid-batch.json'))) throw new Error('Batch has been permanently invalidated')
    if (inventory(source).hash !== frozenSource.hash || freezeResolvedDependencies(source).hash !== dependencies.hash || sha256(readFileSync(binary)) !== binaryHash || sha256(readFileSync(parent)) !== manifest.parentHash || sha256(readFileSync(wrapper)) !== manifest.wrapperHash || sha256(readFileSync(import.meta.path)) !== runnerHash || sha256(readFileSync(coreFile)) !== coreHash || sha256(readFileSync(evidenceFile)) !== evidenceHash || JSON.stringify(verifyCandidateEvidence(candidate.path, binaryHash, candidate.target)) !== JSON.stringify(candidate)) throw new Error('Frozen input changed during scored batch')
    if (JSON.stringify(verifySourceParentEvidence(parentReceipt!, parent, { candidate: candidate!, source: { root: source, rows: frozenSource.rows }, dependenciesHash: dependencies.hash, wrapperHash: manifest.wrapperHash })) !== JSON.stringify(parentEvidence)) throw new Error('Frozen real Source parent evidence changed during scored batch')
  }
  try {
    pairs = loadCompletedPairs(allocation, cases, manifestHash, manifest.parentHash)
    checkInputs()
  } catch (error) { invalidateBatch(allocation, redact(String(error))); throw error }
  let active: { path: string, route: string, calls: number, writes: Promise<void>[], controllers: Set<AbortController>, errors: string[], transportErrors: string[], recordingErrors: string[], modelCalls: number, closing?: boolean } | undefined
  // Forward provider bytes without Bun's development-mode stream diagnostics
  // or fallback pages. The recorder still seals every partial/error response.
  const upstream = Bun.serve({ hostname: '127.0.0.1', port: 0, development: false, async fetch(request) {
    if (!active) return new Response('No active benchmark task', { status: 503 })
    const capture = active
    const url = new URL(request.url)
    if (!(request.method === 'HEAD' && url.pathname === capture.route + '/anthropic') && !url.pathname.startsWith(capture.route + '/anthropic/')) return new Response('Retired or unexpected benchmark task', { status: 410 })
    const pathname = url.pathname.slice(capture.route.length)
    const controller = new AbortController()
    capture.controllers.add(controller)
    let finished = false
    let number: number | undefined
    let requestHash: string | undefined
    let phase: 'body' | 'fetch' | 'stream' = 'body'
    let finishRequest!: () => void
    const owned = new Promise<void>(resolve => { finishRequest = resolve })
    capture.writes.push(owned)
    const finish = () => {
      if (finished) return
      finished = true
      clearTimeout(requestTimer)
      request.signal.removeEventListener('abort', propagateAbort)
      controller.signal.removeEventListener('abort', canceled)
      capture.controllers.delete(controller)
      finishRequest()
    }
    const propagateAbort = () => controller.abort(new Error('Benchmark HTTP consumer canceled'))
    const transportFailure = (error: string) => {
      if (request.method !== 'HEAD') capture.errors.push(redact(error))
      // Source preconnect is fire-and-forget and catches transport rejection.
      // Keep its real failure separate from a billable model stream failure.
      if (request.method === 'HEAD') capture.transportErrors.push(redact(error))
      if (number !== undefined) {
        try {
          if (request.method !== 'HEAD') writeFileSync(capture.path + `/response-${number}.txt`, new Uint8Array(), { mode: 0o600 })
          writeFileSync(capture.path + `/response-${number}.json`, JSON.stringify({ method: request.method, status: 0, complete: false, error: redact(error), bytes: 0, sha256: sha256(new Uint8Array()), requestHash }), { mode: 0o600 })
        } catch (failure) { capture.recordingErrors.push('Evidence write failed: ' + String(failure)) }
      }
    }
    const canceled = () => {
      if (phase === 'stream') return // Evidence settles and writes its partial body.
      transportFailure(phase === 'fetch' ? 'Upstream fetch failed: owned request canceled before headers' : 'Owned request canceled before body admission')
      finish()
    }
    const requestTimer = setTimeout(() => controller.abort(new Error('Benchmark owned request timeout')), PAIRED_PROTOCOL.timeoutMs + 5_000)
    request.signal.addEventListener('abort', propagateAbort, { once: true })
    controller.signal.addEventListener('abort', canceled, { once: true })
    let body: string | undefined
    let bodyBytes: Uint8Array | undefined
    try {
      if (request.method !== 'GET' && request.method !== 'HEAD') {
        bodyBytes = new Uint8Array(await request.arrayBuffer())
        body = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(bodyBytes)
      }
    } catch (error) {
      capture.errors.push(redact('Request body failed: ' + String(error)))
      finish()
      return new Response('Invalid benchmark request body', { status: 400 })
    }
    if (active !== capture || capture.closing || controller.signal.aborted) {
      finish()
      return new Response('Retired benchmark body admission', { status: 410 })
    }
    number = ++capture.calls
    if (url.pathname.endsWith('/messages')) {
      try {
        const captured = { number, method: request.method, pathname, query: url.search, bodyHash: sha256(bodyBytes!), body: JSON.parse(body!) }
        const serialized = redact(JSON.stringify(captured))
        requestHash = sha256(serialized)
        capture.modelCalls++
        writeFileSync(capture.path + `/request-${number}.txt`, bodyBytes!, { mode: 0o600 })
        appendFileSync(capture.path + '/requests.jsonl', serialized + '\n', { mode: 0o600 })
      } catch (error) {
        capture.recordingErrors.push(redact('Request evidence failed: ' + String(error)))
        finish()
        return new Response('Invalid benchmark request evidence', { status: 400 })
      }
    }
    const upstreamURL = 'https://api.deepseek.com' + pathname + url.search
    try { appendFileSync(capture.path + '/transport.jsonl', JSON.stringify({ number, method: request.method, pathname, query: url.search, upstreamURL }) + '\n', { mode: 0o600 }) } catch (error) {
      capture.recordingErrors.push('Transport evidence write failed: ' + String(error)); finish()
      return new Response('Invalid benchmark transport evidence', { status: 500 })
    }
    const headers = new Headers(request.headers)
    for (const name of ['host', 'connection', 'content-length']) headers.delete(name)
    phase = 'fetch'
    let response: Response
    try {
      response = await fetch(upstreamURL, { method: request.method, headers, body: bodyBytes, signal: controller.signal, redirect: 'error' })
    } catch (error) {
      if (!finished) transportFailure('Upstream fetch failed: ' + String(error))
      finish()
      return new Response('Benchmark upstream transport failed', { status: 502 })
    }
    if (active !== capture || capture.closing || controller.signal.aborted || finished) {
      controller.abort(new Error('Retired benchmark upstream response'))
      // A fetch implementation may deliver headers after cancellation. Its
      // body must not acquire a new recorder or escape the retired owner.
      if (response.body) void response.body.cancel().catch(() => {})
      finish()
      return new Response('Retired benchmark response', { status: 410 })
    }
    const outputHeaders = new Headers(response.headers)
    // Bun fetch decodes gzip. Preserve the actual bytes consumed by both SDKs.
    for (const name of ['content-encoding', 'content-length']) outputHeaders.delete(name)
    if (!response.body || request.method === 'HEAD') {
      try {
        if (request.method !== 'HEAD') writeFileSync(capture.path + `/response-${number}.txt`, new Uint8Array(), { mode: 0o600 })
        writeFileSync(capture.path + `/response-${number}.json`, JSON.stringify({ method: request.method, status: response.status, complete: true, bytes: 0, sha256: sha256(new Uint8Array()), contentType: response.headers.get('content-type') ?? '', requestHash }), { mode: 0o600 })
      } catch (error) { capture.recordingErrors.push('Evidence write failed: ' + String(error)) }
      if (request.method === 'HEAD' && response.body) void response.body.cancel().catch(() => {})
      finish()
      return new Response(null, { status: response.status, headers: outputHeaders })
    }
    phase = 'stream'
    const recorded = createStreamEvidence(response.body, { signal: request.signal, abortController: controller, timeoutMs: PAIRED_PROTOCOL.timeoutMs + 5_000 })
    void recorded.completion.then(result => {
      try {
        writeFileSync(capture.path + `/response-${number}.txt`, result.partialBytes, { mode: 0o600 })
        writeFileSync(capture.path + `/response-${number}.json`, JSON.stringify({ method: request.method, status: response.status, complete: result.complete, error: result.error, bytes: result.partialBytes.length, sha256: sha256(result.partialBytes), contentType: response.headers.get('content-type') ?? '', requestHash }), { mode: 0o600 })
      } catch (error) { capture.recordingErrors.push('Evidence write failed: ' + String(error)) }
      if (!result.complete) capture.errors.push(redact('Incomplete stream evidence: ' + result.error))
      finish()
    })
    return new Response(recorded.consumer, { status: response.status, headers: outputHeaders })
  } })
  const initialParentHash = sha256(readFileSync(parent))
  if (initialParentHash !== manifest.parentHash) { invalidateBatch(allocation, 'Parent transcript changed before batch'); await upstream.stop(true); throw new Error('Parent transcript changed before batch') }
  try {
    for (let index = pairs.length; index < cases.length; index++) {
      checkInputs()
      const task = cases[index], pair: Partial<ProvenPair> = { id: task.id, evidence: { version: 2, manifestHash, source: '', go: '' } }
      writeFileSync(join(allocation, 'pair-in-progress.json'), JSON.stringify({ id: task.id, manifestHash }), { flag: 'wx', mode: 0o600, flush: true })
      const engines = index % 2 === 0 ? ['source', 'go'] as const : ['go', 'source'] as const
      for (const engine of engines) {
        const home = join(allocation, task.id, engine)
        if (!inside(allocation, home) || existsSync(home)) throw new Error('Existing partial task requires diagnosis, not selective rerun: ' + home)
        mkdirSync(join(home, '.claude'), { recursive: true, mode: 0o700 })
        mkdirSync(join(home, 'tmp'), { mode: 0o700 })
        for (const [path, content] of Object.entries(task.files)) {
          mkdirSync(dirname(join(home, path)), { recursive: true })
          writeFileSync(join(home, path), content, { mode: 0o600 })
        }
        writeFileSync(join(home, '.claude/settings.json'), '{"disableAllHooks":true,"maxConcurrentSubagents":2}', { mode: 0o600 })
        const parentCopy = join(home, '.claude/parent.jsonl')
        if (task.fork) writeFileSync(parentCopy, parentBytes, { mode: 0o600 })
        const route = '/runs/' + task.id + '/' + engine
        const env = { HOME: home, USERPROFILE: home, CLAUDE_CONFIG_DIR: home + '/.claude', TMPDIR: home + '/tmp', CLAUDE_CODE_TMPDIR: home + '/tmp', XDG_CONFIG_HOME: home + '/.config', XDG_CACHE_HOME: home + '/.cache', XDG_DATA_HOME: home + '/.local/share', PATH: dirname(process.execPath) + ':/opt/homebrew/bin:/usr/bin:/bin', SHELL: '/bin/bash', BUN_OPTIONS: '--no-env-file', NODE_ENV: 'production', USER_TYPE: 'external', CC_HAHA_DISABLE_TERMINAL_SHELL_ENV: '1', DISABLE_TELEMETRY: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH: '1', CLAUDE_CODE_DISABLE_AUTO_MEMORY: 'true', ANTHROPIC_API_KEY: key, ANTHROPIC_BASE_URL: upstream.url.origin + route + '/anthropic', ANTHROPIC_MODEL: PAIRED_PROTOCOL.model, ANTHROPIC_DEFAULT_HAIKU_MODEL: PAIRED_PROTOCOL.model, ANTHROPIC_DEFAULT_SONNET_MODEL: PAIRED_PROTOCOL.model, ANTHROPIC_DEFAULT_OPUS_MODEL: PAIRED_PROTOCOL.model, CLAUDE_CODE_MODEL_CONTEXT_WINDOWS: JSON.stringify({ [PAIRED_PROTOCOL.model]: PAIRED_PROTOCOL.contextWindow }), CLAUDE_CODE_MAX_OUTPUT_TOKENS: String(PAIRED_PROTOCOL.maxOutputTokens), CLAUDE_CODE_MAX_RETRIES: '2', CC_HAHA_SEND_DISABLED_THINKING: '1', ENABLE_TOOL_SEARCH: 'false', CC_HAHA_PAIRED_ALLOW_LIVE: '1', CC_HAHA_PAIRED_SOURCE_ROOT: source, CC_HAHA_PAIRED_ALLOCATION: allocation }
        const args = ['--print', '--model', PAIRED_PROTOCOL.model, '--thinking', PAIRED_PROTOCOL.thinking, '--no-computer-use', '--dangerously-skip-permissions', '--output-format', 'stream-json', '--verbose', '--max-turns', String(PAIRED_PROTOCOL.maxTurns), ...(task.fork ? ['--resume', parentCopy, '--fork-session'] : []), task.prompt]
        writeFileSync(home + '/requests.jsonl', '', { mode: 0o600 })
        writeFileSync(home + '/transport.jsonl', '', { mode: 0o600 })
        active = { path: home, route, calls: 0, writes: [], controllers: new Set(), errors: [], transportErrors: [], recordingErrors: [], modelCalls: 0 }
        const result = await command(engine === 'go' ? [binary, ...args] : [process.execPath, '--no-env-file', wrapper, ...args], home, env, PAIRED_PROTOCOL.timeoutMs)
        writeFileSync(home + '/stdout.jsonl', redact(result.stdout), { mode: 0o600 })
        writeFileSync(home + '/stderr.log', redact(result.stderr), { mode: 0o600 })
        writeFileSync(home + '/receipt.json', JSON.stringify({ engine, task: task.id, status: 'processing-evidence', exitCode: result.code, timedOut: result.timedOut }), { mode: 0o600 })
        if (!result.timedOut) {
          let grace: ReturnType<typeof setTimeout> | undefined
          await Promise.race([
            Promise.all(active.writes),
            new Promise<void>(resolve => { grace = setTimeout(resolve, 2_000) }),
          ])
          if (grace) clearTimeout(grace)
        }
        active.closing = true
        for (const controller of active.controllers) controller.abort(new Error('Benchmark CLI exited; close owned request'))
        await Promise.all(active.writes)
        const calls = active.calls
        const modelCalls = active.modelCalls
        const evidenceErrors = active.errors
        const transportErrors = active.transportErrors
        const recordingErrors = [...active.recordingErrors, ...(result.recordingErrors ?? [])]
        active = undefined
        const records = result.stdout.trim().split('\n').filter(Boolean).map(line => { try { return JSON.parse(line) } catch { return { type: 'invalid_stdout', line } } })
        const terminal = records.findLast(row => row.type === 'result')
        const immutable = task.immutable.every(path => {
          try { return readFileSync(join(home, path), 'utf8') === task.files[path] } catch { return false }
        }) && (!task.fork || (() => {
          try { return sha256(readFileSync(parentCopy)) === manifest.parentHash } catch { return false }
        })())
        const judged = await command(['/usr/bin/env', 'python3', '-c', task.judge], home, { HOME: home, PATH: env.PATH, TMPDIR: home + '/tmp', PYTHONDONTWRITEBYTECODE: '1' }, 30_000)
        writeFileSync(home + '/judge.log', judged.stdout + judged.stderr, { mode: 0o600 })
        writeFileSync(home + '/judge.json', JSON.stringify({ exitCode: judged.code, timedOut: judged.timedOut, codeHash: sha256(task.judge), logHash: sha256(judged.stdout + judged.stderr), recordingErrors: judged.recordingErrors ?? [] }), { mode: 0o600 })
        writeFileSync(home + '/command.json', JSON.stringify({ argv: engine === 'go' ? [binary, ...args] : [process.execPath, '--no-env-file', wrapper, ...args], exitCode: result.code, timedOut: result.timedOut, durationMs: result.durationMs, startedMonotonicMs: result.startedMonotonicMs, endedMonotonicMs: result.endedMonotonicMs, evidenceErrors, transportErrors, recordingErrors, processGroup: result.processGroup, signal: result.signal, joined: result.joined, stdoutComplete: result.stdoutComplete, stderrComplete: result.stderrComplete, transportCalls: calls, modelRequests: modelCalls }), { mode: 0o600 })
        const toolErrors = records.flatMap(row => Array.isArray(row.message?.content) ? row.message.content.filter((block: any) => block.type === 'tool_result' && block.is_error === true) : [])
        // Persist the same recomputed failure/measurement that is sealed below,
        // including explicit missing accounting rather than a guessed cost.
        const actual = validateEngineEvidence(home, task, manifest.parentHash)
        writeFileSync(home + '/receipt.json', redact(JSON.stringify({ engine, task: task.id, measurement: actual.measurement, accountingComplete: actual.accountingComplete, missingMeasurements: actual.missingMeasurements, reportedSDKCost: actual.reportedSDKCost, immutable, exitCode: result.code, httpRequests: calls, modelRequests: modelCalls, terminal, toolErrors, judgeExitCode: judged.code }, null, 2)), { mode: 0o600 })
        const sealed = sealEngineEvidence(home, task, engine, manifestHash, manifest.parentHash)
        pair[engine] = sealed.measurement
        pair.evidence![engine] = sealed.sha256
        console.log({ task: task.id, engine, success: sealed.measurement.success, modelRequests: modelCalls, httpRequests: calls, durationMs: Math.round(result.durationMs), runtimeErrors: sealed.measurement.runtimeErrors })
      }
      // Validate every frozen input before making a pair durably resumable.
      const completed = pair as ProvenPair
      commitCompletedPair(allocation, task, completed, manifestHash, manifest.parentHash, checkInputs)
      pairs.push(completed)
    }
    checkInputs()
    const score = evaluatePairs(pairs)
    writeFileSync(join(allocation, 'score.json'), JSON.stringify(score, null, 2), { mode: 0o600 })
    console.log(score)
    if (!pilot && score.status !== 'passed') process.exitCode = 1
  } catch (error) {
    invalidateBatch(allocation, redact(String(error)))
    throw error
  } finally {
    if (active) { active.closing = true; for (const controller of active.controllers) controller.abort(new Error('Benchmark batch closed')); await Promise.all(active.writes) }
    await upstream.stop(true)
  }
  })
}

if (import.meta.main) await main()
