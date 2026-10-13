#!/usr/bin/env bun
import { appendFileSync, existsSync, lstatSync, mkdirSync, readFileSync, readdirSync, realpathSync, writeFileSync } from 'node:fs'
import { dirname, isAbsolute, join, relative, resolve, sep } from 'node:path'
import { createHash } from 'node:crypto'
import { PAIRED_PROTOCOL, SCENARIOS, evaluatePairs, sha256, taskFor, type Measurement, type Pair } from './go-main-paired-benchmark-core'
import { createStreamEvidence, freezeResolvedDependencies } from './go-main-paired-benchmark-evidence'

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
  for (const name of ['src', 'adapters']) visit(join(root, name))
  for (const name of ['preload.ts', 'package.json', 'bun.lock']) rows.push([name, sha256(readFileSync(join(root, name)))])
  return { hash: sha256(JSON.stringify(rows)), rows }
}
function inside(root: string, path: string) {
  const value = relative(root, path)
  return value && value !== '..' && !value.startsWith('..' + sep) && !isAbsolute(value)
}
async function command(command: string[], cwd: string, env: Record<string, string>, timeout: number, input?: string) {
  const started = performance.now()
  const child = Bun.spawn(command, { cwd, env, stdin: input === undefined ? 'ignore' : new TextEncoder().encode(input), stdout: 'pipe', stderr: 'pipe' })
  let timedOut = false
  let force: ReturnType<typeof setTimeout> | undefined
  const timer = setTimeout(() => {
    timedOut = true
    child.kill('SIGTERM')
    force = setTimeout(() => { child.kill('SIGKILL') }, 5_000)
  }, timeout)
  try {
    const [stdout, stderr, code] = await Promise.all([new Response(child.stdout).text(), new Response(child.stderr).text(), child.exited])
    return { stdout, stderr, code, timedOut, durationMs: performance.now() - started }
  } finally {
    clearTimeout(timer)
    if (force) clearTimeout(force)
  }
}

async function main() {
  // Preparing freezes the judgment before any scored provider call. A caller
  // must explicitly pass --allow-live to spend quota; CI never invokes this.
  const source = argument('--source-root'), binary = argument('--go-binary'), allocation = argument('--allocation'), parent = argument('--parent-transcript')
  if (!allocation.startsWith('/private/tmp/') && !allocation.startsWith('/tmp/')) throw new Error('Benchmark needs an explicit disposable allocation')
  if ((lstatSync(allocation).mode & 0o077) !== 0 || (lstatSync(parent).mode & 0o077) !== 0) throw new Error('Private allocation/transcript permissions required')
  const frozenSource = inventory(source), binaryHash = sha256(readFileSync(binary)), parentBytes = readFileSync(parent)
  const listing = Bun.spawn(['git', 'ls-tree', '-rz', PAIRED_PROTOCOL.main, '--', 'src', 'adapters', 'preload.ts', 'package.json', 'bun.lock'], { cwd: resolve(import.meta.dir, '../..'), stdout: 'pipe', stderr: 'pipe' })
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
  const manifest = { protocol: PAIRED_PROTOCOL, phase: pilot ? 'unscored setup pilot' : 'formal batch', cases, source: frozenSource, dependencies, binaryHash, parentHash: sha256(parentBytes), wrapperHash: sha256(readFileSync(wrapper)), runnerHash, coreHash, evidenceHash, sdk: sdkPackage.version, axios: axiosPackage.version, bun: Bun.version, revision: Bun.revision, platform: process.platform, architecture: process.arch, bindings: ['Absent OS MDM', 'Absent keychain prefetch', 'Original private plaintext credentials'], tools: 'Original default complete tool set; no --bare, --tools or custom system prompt', ordering: 'Alternating Source/Go and Go/Source by pair index', scoring: 'One final batch of 120 unique pairs; pilots and partial batches cannot pass; no retrospective retry/exclusion' }
  const manifestFile = join(allocation, 'manifest.json'), manifestText = JSON.stringify(manifest, null, 2) + '\n'
  if (existsSync(manifestFile)) {
    if (readFileSync(manifestFile, 'utf8') !== manifestText) throw new Error('Candidate or frozen protocol changed; use a fresh allocation')
  } else writeFileSync(manifestFile, manifestText, { flag: 'wx', mode: 0o600 })
  writeFileSync(join(allocation, 'source-inventory.json'), JSON.stringify(frozenSource, null, 2), { mode: 0o600 })
  if (!argv.includes('--allow-live')) {
    console.log({ status: 'frozen-not-run', manifest: manifestFile, hash: sha256(manifestText) })
    return
  }
  const keyFile = argument('--key-file')
  if ((lstatSync(keyFile).mode & 0o077) !== 0) throw new Error('Private key-file permissions required')
  const key = readFileSync(keyFile, 'utf8').trim()
  if (!key) throw new Error('Empty explicitly supplied key')
  const redact = (value: string) => value.replaceAll(key, '<redacted-key>')
  const receiptsFile = join(allocation, 'pairs.jsonl')
  const pairs: Pair[] = existsSync(receiptsFile) ? readFileSync(receiptsFile, 'utf8').trim().split('\n').filter(Boolean).map(line => JSON.parse(line)) : []
  if (new Set(pairs.map(pair => pair.id)).size !== pairs.length || pairs.some((pair, index) => pair.id !== cases[index]?.id)) throw new Error('Invalid partial batch sequence')
  let active: { path: string, route: string, calls: number, writes: Promise<void>[], controllers: Set<AbortController>, errors: string[], closing?: boolean } | undefined
  const upstream = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) {
    if (!active) return new Response('No active benchmark task', { status: 503 })
    const capture = active
    const url = new URL(request.url)
    if (!url.pathname.startsWith(capture.route + '/anthropic/')) return new Response('Retired or unexpected benchmark task', { status: 410 })
    const pathname = url.pathname.slice(capture.route.length)
    const controller = new AbortController()
    capture.controllers.add(controller)
    let finished = false
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
    const canceled = () => {
      if (phase === 'stream') return // Evidence settles and writes its partial body.
      capture.errors.push(phase === 'fetch' ? 'Upstream fetch failed: owned request canceled before headers' : 'Owned request canceled before body admission')
      finish()
    }
    const requestTimer = setTimeout(() => controller.abort(new Error('Benchmark owned request timeout')), PAIRED_PROTOCOL.timeoutMs + 5_000)
    request.signal.addEventListener('abort', propagateAbort, { once: true })
    controller.signal.addEventListener('abort', canceled, { once: true })
    let body: string | undefined
    try { body = request.method === 'GET' ? undefined : await request.text() } catch (error) {
      capture.errors.push(redact('Request body failed: ' + String(error)))
      finish()
      return new Response('Invalid benchmark request body', { status: 400 })
    }
    if (active !== capture || capture.closing || controller.signal.aborted) {
      finish()
      return new Response('Retired benchmark body admission', { status: 410 })
    }
    const number = ++capture.calls
    if (url.pathname.endsWith('/messages')) {
      try {
        appendFileSync(capture.path + '/requests.jsonl', redact(JSON.stringify({ number, pathname, query: url.search, body: JSON.parse(body!) })) + '\n', { mode: 0o600 })
      } catch (error) {
        capture.errors.push(redact('Request evidence failed: ' + String(error)))
        finish()
        return new Response('Invalid benchmark request evidence', { status: 400 })
      }
    }
    const headers = new Headers(request.headers)
    for (const name of ['host', 'connection', 'content-length']) headers.delete(name)
    phase = 'fetch'
    let response: Response
    try {
      response = await fetch('https://api.deepseek.com' + pathname + url.search, { method: request.method, headers, body, signal: controller.signal, redirect: 'error' })
    } catch (error) {
      capture.errors.push(redact('Upstream fetch failed: ' + String(error)))
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
    if (!response.body) {
      finish()
      return new Response(null, { status: response.status, headers: outputHeaders })
    }
    phase = 'stream'
    const recorded = createStreamEvidence(response.body, { signal: request.signal, abortController: controller, timeoutMs: PAIRED_PROTOCOL.timeoutMs + 5_000 })
    void recorded.completion.then(result => {
      try {
        writeFileSync(capture.path + `/response-${number}.txt`, redact(new TextDecoder().decode(result.partialBytes)), { mode: 0o600 })
        writeFileSync(capture.path + `/response-${number}.json`, JSON.stringify({ status: response.status, complete: result.complete, error: result.error, bytes: result.partialBytes.length }), { mode: 0o600 })
      } catch (error) { capture.errors.push('Evidence write failed: ' + String(error)) }
      if (!result.complete) capture.errors.push(redact('Incomplete stream evidence: ' + result.error))
      finish()
    })
    return new Response(recorded.consumer, { status: response.status, headers: outputHeaders })
  } })
  const initialParentHash = sha256(readFileSync(parent))
  try {
    for (let index = pairs.length; index < cases.length; index++) {
      const task = cases[index], pair: Partial<Pair> = { id: task.id }
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
        const env = { HOME: home, USERPROFILE: home, CLAUDE_CONFIG_DIR: home + '/.claude', TMPDIR: home + '/tmp', XDG_CONFIG_HOME: home + '/.config', XDG_CACHE_HOME: home + '/.cache', XDG_DATA_HOME: home + '/.local/share', PATH: dirname(process.execPath) + ':/opt/homebrew/bin:/usr/bin:/bin', SHELL: '/bin/bash', BUN_OPTIONS: '--no-env-file', NODE_ENV: 'production', USER_TYPE: 'external', CC_HAHA_DISABLE_TERMINAL_SHELL_ENV: '1', DISABLE_TELEMETRY: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH: '1', CLAUDE_CODE_DISABLE_AUTO_MEMORY: 'true', ANTHROPIC_API_KEY: key, ANTHROPIC_BASE_URL: upstream.url.origin + route + '/anthropic', ANTHROPIC_MODEL: PAIRED_PROTOCOL.model, ANTHROPIC_DEFAULT_HAIKU_MODEL: PAIRED_PROTOCOL.model, ANTHROPIC_DEFAULT_SONNET_MODEL: PAIRED_PROTOCOL.model, ANTHROPIC_DEFAULT_OPUS_MODEL: PAIRED_PROTOCOL.model, CLAUDE_CODE_MODEL_CONTEXT_WINDOWS: JSON.stringify({ [PAIRED_PROTOCOL.model]: PAIRED_PROTOCOL.contextWindow }), CLAUDE_CODE_MAX_RETRIES: '2', ENABLE_TOOL_SEARCH: 'false', CC_HAHA_PAIRED_ALLOW_LIVE: '1', CC_HAHA_PAIRED_SOURCE_ROOT: source, CC_HAHA_PAIRED_ALLOCATION: allocation }
        const args = ['--print', '--model', PAIRED_PROTOCOL.model, '--thinking', PAIRED_PROTOCOL.thinking, '--no-computer-use', '--dangerously-skip-permissions', '--output-format', 'stream-json', '--verbose', '--max-turns', String(PAIRED_PROTOCOL.maxTurns), ...(task.fork ? ['--resume', parentCopy, '--fork-session'] : []), task.prompt]
        active = { path: home, route, calls: 0, writes: [], controllers: new Set(), errors: [] }
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
        const evidenceErrors = active.errors
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
        const runtimeErrors = [...evidenceErrors, ...records.filter(row => row.type === 'invalid_stdout').map(() => 'Invalid public stdout'), ...(result.timedOut ? ['Whole-task timeout'] : []), ...(immutable ? [] : ['Immutable task or parent changed']), ...(/capability:|capability is not (?:implemented|migrated)|RUNTIME_CONFIG_INVALID/i.test(result.stderr + result.stdout) ? ['Runtime capability/configuration failure'] : [])]
        const toolErrors = records.flatMap(row => Array.isArray(row.message?.content) ? row.message.content.filter((block: any) => block.type === 'tool_result' && block.is_error === true) : [])
        const measurement: Measurement = { success: result.code === 0 && terminal?.subtype === 'success' && terminal.is_error === false && judged.code === 0 && immutable && calls > 0, durationMs: result.durationMs, estimatedCost: terminal?.total_cost_usd ?? 0, runtimeErrors }
        pair[engine] = measurement
        writeFileSync(home + '/receipt.json', redact(JSON.stringify({ engine, task: task.id, measurement, exitCode: result.code, modelRequests: calls, terminal, toolErrors, judgeExitCode: judged.code }, null, 2)), { mode: 0o600 })
        console.log({ task: task.id, engine, success: measurement.success, requests: calls, durationMs: Math.round(result.durationMs), runtimeErrors })
      }
      const completed = pair as Pair
      pairs.push(completed)
      appendFileSync(receiptsFile, JSON.stringify(completed) + '\n', { mode: 0o600 })
      if (inventory(source).hash !== frozenSource.hash || freezeResolvedDependencies(source).hash !== dependencies.hash || sha256(readFileSync(binary)) !== binaryHash || sha256(readFileSync(parent)) !== initialParentHash || sha256(readFileSync(wrapper)) !== manifest.wrapperHash || sha256(readFileSync(import.meta.path)) !== runnerHash || sha256(readFileSync(coreFile)) !== coreHash || sha256(readFileSync(evidenceFile)) !== evidenceHash) throw new Error('Frozen input changed during scored batch')
    }
    const score = evaluatePairs(pairs)
    writeFileSync(join(allocation, 'score.json'), JSON.stringify(score, null, 2), { mode: 0o600 })
    console.log(score)
    if (!pilot && score.status !== 'passed') process.exitCode = 1
  } finally { await upstream.stop(true) }
}

if (import.meta.main) await main()
