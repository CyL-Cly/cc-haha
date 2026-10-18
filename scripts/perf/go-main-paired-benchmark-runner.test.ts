import { afterEach, describe, expect, test } from 'bun:test'
import { appendFileSync, chmodSync, existsSync, lstatSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, realpathSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { dirname, isAbsolute, join, relative, resolve, sep } from 'node:path'
import { createHash } from 'node:crypto'
import { tmpdir } from 'node:os'
import { commitCompletedPair, createStreamEvidence, freezeResolvedDependencies, invalidateBatch, loadCompletedPairs, sealEngineEvidence, validateBatchStartup, validateEngineEvidence, type ProvenPair } from './go-main-paired-benchmark-evidence'
import { PAIRED_PROTOCOL, SCENARIOS, evaluatePairs, sha256, taskFor, type Pair } from './go-main-paired-benchmark-core'
import { createSandboxedTestEnvironment } from '../pr/test-environment'

// Execute the production functions verbatim with only the external HTTP origin
// redirected to a real loopback peer. There is no provider traffic or CLI model
// stub in these focused recorder/process/identity tests.
const runner = readFileSync(join(import.meta.dir, 'go-main-paired-benchmark.ts'), 'utf8')
const section = (start: string, end: string) => {
  const from = runner.indexOf(start), to = runner.indexOf(end, from)
  if (from < 0 || to < 0) throw new Error('Production benchmark seam moved: ' + start)
  return runner.slice(from, to)
}
const javascript = (source: string) => new Bun.Transpiler({ loader: 'ts' }).transformSync(source)
const roots: string[] = []
const temporary = () => { const root = mkdtempSync(join(tmpdir(), 'go-benchmark-runner-')); roots.push(root); return root }
afterEach(() => { for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true }) })

type Capture = { path: string, route: string, calls: number, writes: Promise<void>[], controllers: Set<AbortController>, errors: string[], transportErrors: string[], recordingErrors: string[], modelCalls: number }
const captureAt = (route: string): Capture => new Function('home', 'route', 'let active;\n' + javascript(section('        active = {', '        const result =')) + 'return active')(temporary(), route)

function productionProxy(origin: URL, afterHeaders?: () => Promise<void>) {
  const seen = new Map<string, () => void>()
  const realFetch = globalThis.fetch
  const redirected = (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(input instanceof Request ? input.url : String(input))
    if (url.origin !== 'https://api.deepseek.com') throw new Error('Unapproved fixture fetch')
    return realFetch(new URL(url.pathname + url.search, origin), init).then(async response => {
      if (afterHeaders) await afterHeaders()
      return response
    })
  }
  const observedBun = { serve(options: Parameters<typeof Bun.serve>[0]) {
    return Bun.serve({ ...options, fetch(request, server) {
      const result = options.fetch!(request, server)
      seen.get(new URL(request.url).pathname)?.()
      return result
    } })
  } }
  const proxy = section('let active:', '  const initialParentHash =')
  const finish = section('        if (!result.timedOut) {', '        const records =')
  const build = new Function('Bun', 'fetch', 'appendFileSync', 'writeFileSync', 'createStreamEvidence', 'PAIRED_PROTOCOL', 'redact', 'sha256', javascript(proxy) + `
return { server: upstream, setActive(value) { active=value },
  async finish(result) { ${javascript(finish)} return {calls, evidenceErrors, recordingErrors} }
}`)
  const result = build(observedBun, redirected, appendFileSync, writeFileSync, createStreamEvidence, PAIRED_PROTOCOL, (value: string) => value, sha256) as {
    server: ReturnType<typeof Bun.serve>
    setActive(value: Capture | undefined): void
    finish(result: { timedOut: boolean }): Promise<{ calls: number, evidenceErrors: string[], recordingErrors: string[] }>
  }
  return { ...result, observed(path: string) { return new Promise<void>(resolve => { seen.set(path, resolve) }) } }
}

const productionCommand = new Function('Bun', javascript(section('async function command(', '\nasync function main()')) + 'return command')(Bun) as (command: string[], cwd: string, env: Record<string, string>, timeout: number, input?: string) => Promise<{ stdout: string, stderr: string, code: number, timedOut: boolean, durationMs: number, processGroup: number | null, joined: boolean, stdoutComplete: boolean, stderrComplete: boolean, signal: string | null, recordingErrors: string[] }>

async function processIdentity(home: string, pid: number) {
  const peer = Bun.spawn(['/bin/ps', '-o', 'pid=,pgid=,lstart=', '-p', String(pid)], { cwd: home, env: { HOME: home, PATH: '/usr/bin:/bin', LC_ALL: 'C' }, stdout: 'pipe', stderr: 'pipe' })
  const [text, , code] = await Promise.all([new Response(peer.stdout).text(), new Response(peer.stderr).text(), peer.exited])
  return code === 0 ? text.trim() : undefined
}
function ownedPipeFixture(mode: 'term' | 'leader-exit' | 'ignore-term') {
  const home = temporary(), path = join(home, 'fixture-descendant.sh')
  writeFileSync(path, `
role="$1"; home="$2"; mode="$3"
printf '%s\\n' "$$" > "$home/$role.pid"
LC_ALL=C /bin/ps -o pid=,pgid=,lstart= -p "$$" > "$home/$role.identity"
if [ "$role" = descendant ]; then
  if [ "$mode" = ignore-term ]; then trap '' TERM; else trap 'exit 0' TERM; fi
  printf 'descendant-pipe-open\\n'
  printf ready > "$home/ready"
  while :; do :; done
fi
/bin/bash --noprofile --norc "$0" descendant "$home" "$mode" &
if [ "$mode" = leader-exit ]; then
  while [ ! -f "$home/ready" ]; do :; done
  exit 0
fi
if [ "$mode" = ignore-term ]; then trap '' TERM; else trap 'printf "parent-term\\n" >&2; exit 0' TERM; fi
while :; do :; done
`)
  const completion = productionCommand(['/bin/bash', '--noprofile', '--norc', path, 'parent', home, mode], home, { HOME: home, USERPROFILE: home, CLAUDE_CONFIG_DIR: join(home, '.claude'), PATH: '/usr/bin:/bin', LC_ALL: 'C' }, 250)
  return { home, completion }
}
async function verifyOwnedGroupClosed(home: string, result: Awaited<ReturnType<typeof productionCommand>>) {
  const parent = Number(readFileSync(join(home, 'parent.pid'), 'utf8').trim())
  const identities = ['parent', 'descendant'].map(role => readFileSync(join(home, role + '.identity'), 'utf8').trim().split(/\s+/))
  const host = (await processIdentity(home, process.pid))!.split(/\s+/)
  expect(result.processGroup).toBe(parent)
  expect(identities[0][0]).toBe(String(parent))
  expect(identities[0][1]).toBe(String(parent))
  expect(identities[1][0]).toBe(readFileSync(join(home, 'descendant.pid'), 'utf8').trim())
  expect(identities[1][1]).toBe(String(parent))
  expect(Number(host[1])).toBeGreaterThan(0)
  expect(identities[0][1]).not.toBe(host[1])
  expect(identities[0].slice(2).join(' ')).not.toBe('')
  expect(identities[1].slice(2).join(' ')).not.toBe('')
  const deadline = performance.now() + 2000
  let live: (string | undefined)[] = []
  do {
    live = await Promise.all(identities.map(identity => processIdentity(home, Number(identity[0]))))
    if (live.every(identity => identity === undefined)) break
    await Bun.sleep(10)
  } while (performance.now() < deadline)
  expect(live).toEqual([undefined, undefined])
  expect(result.joined).toBe(true)
  expect(result.stdoutComplete).toBe(true)
  expect(result.stderrComplete).toBe(true)
  expect(result.recordingErrors).toEqual([])
}
async function cleanupOwnedPipeFixture(fixture: ReturnType<typeof ownedPipeFixture>) {
  // macOS ps sess is a kernel-pointer field, not a comparable session ID.
  // PID, actual PGID and process start time are the physical cleanup identity.
  // Failure cleanup signals only a still-matching captured physical identity,
  // never an already reaped PID that may have been reused by an unrelated job.
  for (const role of ['descendant', 'parent']) {
    const identityPath = join(fixture.home, role + '.identity')
    if (!existsSync(identityPath)) continue
    const identity = readFileSync(identityPath, 'utf8').trim()
    const pid = Number(identity.split(/\s+/)[0])
    if (!Number.isSafeInteger(pid) || pid <= 1) throw new Error('Invalid owned fixture PID')
    if (await processIdentity(fixture.home, pid) !== identity) continue
    try { process.kill(pid, 'SIGKILL') } catch (error) { if ((error as NodeJS.ErrnoException).code !== 'ESRCH') throw error }
  }
  let cleanup: ReturnType<typeof setTimeout> | undefined
  try {
    await Promise.race([fixture.completion, new Promise<never>((_, reject) => { cleanup = setTimeout(() => reject(new Error('Owned descendant cleanup did not settle command')), 2000) })])
  } finally { if (cleanup) clearTimeout(cleanup) }
}

describe('production paired runner HTTP and subprocess boundaries', () => {
  test('two concurrent responses retain their own request numbers and task', async () => {
    const peer = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) {
      const { marker, delay } = await request.json() as { marker: string, delay: number }
      await Bun.sleep(delay)
      return new Response(marker)
    } })
    const proxy = productionProxy(peer.url), capture = captureAt('/runs/first/source')
    proxy.setActive(capture)
    try {
      expect(proxy.server.development).toBe(false)
      const path = capture.route + '/anthropic/messages'
      const request = (marker: string, delay: number) => fetch(new URL(path, proxy.server.url), { method: 'POST', body: JSON.stringify({ marker, delay }) }).then(response => response.text())
      const first = request('first-response', 40)
      await Bun.sleep(5)
      const second = request('second-response', 0)
      expect(await second).toBe('second-response')
      expect(await first).toBe('first-response')
      await proxy.finish({ timedOut: false })
      expect(capture.calls).toBe(2)
      expect(readFileSync(join(capture.path, 'response-1.txt'), 'utf8')).toBe('first-response')
      expect(readFileSync(join(capture.path, 'response-2.txt'), 'utf8')).toBe('second-response')
      expect(capture.errors).toEqual([])
    } finally { await proxy.server.stop(true); await peer.stop(true) }
  })

  test('startup HEAD forwards the original base route without a body or model request', async () => {
    const seen: { method: string, path: string, body: string }[] = []
    const peer = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) {
      seen.push({ method: request.method, path: new URL(request.url).pathname, body: await request.text() })
      return new Response(null, { status: 204 })
    } })
    const proxy = productionProxy(peer.url), capture = captureAt('/runs/head/source')
    proxy.setActive(capture)
    try {
      const result = await fetch(new URL(capture.route + '/anthropic', proxy.server.url), { method: 'HEAD' })
      expect(result.status).toBe(204)
      expect(await result.text()).toBe('')
      await proxy.finish({ timedOut: false })
      expect(seen).toEqual([{ method: 'HEAD', path: '/anthropic', body: '' }])
      expect(existsSync(join(capture.path, 'requests.jsonl'))).toBe(false)
      expect(JSON.parse(readFileSync(join(capture.path, 'transport.jsonl'), 'utf8'))).toMatchObject({ number: 1, method: 'HEAD', pathname: '/anthropic' })
      expect(JSON.parse(readFileSync(join(capture.path, 'response-1.json'), 'utf8'))).toMatchObject({ method: 'HEAD', status: 204, complete: true, bytes: 0, sha256: sha256('') })
      expect(capture.errors).toEqual([])
      expect(capture.modelCalls).toBe(0)
    } finally { await proxy.server.stop(true); await peer.stop(true) }
  })

  test('detached HEAD cancellation is preserved separately from model transport failure', async () => {
    let start!: () => void
    const began = new Promise<void>(resolve => { start = resolve })
    const peer = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) {
      start()
      await new Promise<void>(resolve => { request.signal.addEventListener('abort', () => resolve(), { once: true }) })
      return new Response(null, { status: 204 })
    } })
    const proxy = productionProxy(peer.url), capture = captureAt('/runs/held-head/source')
    proxy.setActive(capture)
    const request = fetch(new URL(capture.route + '/anthropic', proxy.server.url), { method: 'HEAD' }).catch(() => undefined)
    try {
      await began
      await proxy.finish({ timedOut: true })
      await request
      expect(capture.errors).toEqual([])
      expect(capture.transportErrors.length).toBeGreaterThan(0)
      expect(capture.modelCalls).toBe(0)
      expect(JSON.parse(readFileSync(join(capture.path, 'response-1.json'), 'utf8'))).toMatchObject({ method: 'HEAD', complete: false, status: 0, bytes: 0 })
    } finally { await proxy.server.stop(true); await peer.stop(true) }
  })

  test('retired task routes cannot create new official requests', async () => {
    let calls = 0
    const peer = Bun.serve({ hostname: '127.0.0.1', port: 0, fetch() { calls++; return new Response('fixture') } })
    const proxy = productionProxy(peer.url), old = captureAt('/runs/old/source'), current = captureAt('/runs/current/go')
    proxy.setActive(old)
    await proxy.finish({ timedOut: true })
    proxy.setActive(current)
    try {
      const response = await fetch(new URL(old.route + '/anthropic/messages', proxy.server.url), { method: 'POST', body: '{}' })
      expect(response.status).toBe(410)
      expect(calls).toBe(0)
      expect(current.calls).toBe(0)
    } finally { await proxy.server.stop(true); await peer.stop(true) }
  })

  test('preheader cancellation is recorded before a task is retired', async () => {
    let started!: () => void, canceled!: () => void
    const begun = new Promise<void>(resolve => { started = resolve })
    const released = new Promise<void>(resolve => { canceled = resolve })
    const peer = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) {
      started()
      await new Promise<void>(resolve => { request.signal.addEventListener('abort', () => { canceled(); resolve() }, { once: true }) })
      return new Response('late headers')
    } })
    const proxy = productionProxy(peer.url), old = captureAt('/runs/headers/source')
    proxy.setActive(old)
    const request = fetch(new URL(old.route + '/anthropic/messages', proxy.server.url), { method: 'POST', body: '{}' }).then(async response => { await response.text(); return response })
    try {
      await begun
      const result = await proxy.finish({ timedOut: true })
      expect(result.evidenceErrors.join(' ')).toContain('Upstream fetch failed')
      expect(old.controllers.size).toBe(0)
      expect((await request).status).toBe(502)
      await released
    } finally {
      for (const controller of old.controllers) controller.abort()
      await request.catch(() => undefined)
      await proxy.server.stop(true)
      await peer.stop(true)
    }
  })

  test('an already admitted slow request body cannot forward after retirement', async () => {
    let calls = 0
    const peer = Bun.serve({ hostname: '127.0.0.1', port: 0, fetch() { calls++; return new Response('late quota') } })
    const proxy = productionProxy(peer.url), old = captureAt('/runs/upload/source'), current = captureAt('/runs/new/go')
    proxy.setActive(old)
    let upload!: ReadableStreamDefaultController<Uint8Array>
    const admitted = proxy.observed(old.route + '/anthropic/messages')
    const body = new ReadableStream<Uint8Array>({ start(controller) { upload = controller; controller.enqueue(new TextEncoder().encode('{"marker":')) } })
    const request = fetch(new URL(old.route + '/anthropic/messages', proxy.server.url), { method: 'POST', body }).then(async response => { await response.text(); return response })
    try {
      await admitted
      await proxy.finish({ timedOut: true })
      proxy.setActive(current)
      upload.enqueue(new TextEncoder().encode('"late"}'))
      upload.close()
      expect((await request).status).toBe(410)
      expect(calls).toBe(0)
      expect(current.calls).toBe(0)
    } finally { await proxy.server.stop(true); await peer.stop(true) }
  })

  test('invalid JSON or failed request evidence writes settle without provider quota', async () => {
    let calls = 0
    const peer = Bun.serve({ hostname: '127.0.0.1', port: 0, fetch() { calls++; return new Response('must not be called') } })
    const proxy = productionProxy(peer.url)
    try {
      for (const scenario of ['invalid-json', 'failed-write']) {
        const capture = captureAt('/runs/' + scenario + '/source')
        proxy.setActive(capture)
        if (scenario === 'failed-write') rmSync(capture.path, { recursive: true })
        const response = await fetch(new URL(capture.route + '/anthropic/messages', proxy.server.url), { method: 'POST', body: scenario === 'invalid-json' ? '{' : '{}' })
        await response.text()
        expect(response.status).toBe(400)
        const result = await proxy.finish({ timedOut: false })
        expect(result.recordingErrors).toHaveLength(1)
        expect(result.recordingErrors[0]).toContain('Request evidence failed')
        expect(result.evidenceErrors).toEqual([])
        expect(capture.controllers.size).toBe(0)
        expect(calls).toBe(0)
      }
    } finally { await proxy.server.stop(true); await peer.stop(true) }
  })

  test('headers delivered after cancellation cannot acquire a retired recorder', async () => {
    let headersReady!: () => void, releaseHeaders!: () => void
    const arrived = new Promise<void>(resolve => { headersReady = resolve })
    const release = new Promise<void>(resolve => { releaseHeaders = resolve })
    const peer = Bun.serve({ hostname: '127.0.0.1', port: 0, fetch() {
      return new Response(new ReadableStream({ start(controller) { controller.enqueue(new TextEncoder().encode('late response body')) } }))
    } })
    const proxy = productionProxy(peer.url, async () => { headersReady(); await release })
    const old = captureAt('/runs/late-headers/source'), current = captureAt('/runs/current/go')
    proxy.setActive(old)
    const request = fetch(new URL(old.route + '/anthropic/messages', proxy.server.url), { method: 'POST', body: '{}' }).then(async response => { await response.text(); return response })
    try {
      await arrived
      const done = await proxy.finish({ timedOut: true })
      expect(done.evidenceErrors.join(' ')).toContain('Upstream fetch failed')
      expect(old.controllers.size).toBe(0)
      expect(done.recordingErrors).toEqual([])
      const before = readFileSync(join(old.path, 'response-1.json'), 'utf8')
      const wire = JSON.parse(before)
      const captured = JSON.parse(readFileSync(join(old.path, 'requests.jsonl'), 'utf8'))
      expect(wire).toMatchObject({ method: 'POST', status: 0, complete: false, bytes: 0, sha256: sha256(new Uint8Array()), requestHash: sha256(JSON.stringify(captured)) })
      expect(wire.error).toContain('Upstream fetch failed')
      expect(readFileSync(join(old.path, 'response-1.txt'))).toHaveLength(0)
      proxy.setActive(current)
      releaseHeaders()
      expect((await request).status).toBe(410)
      expect(readdirSync(old.path).sort()).toEqual(['request-1.txt', 'requests.jsonl', 'response-1.json', 'response-1.txt', 'transport.jsonl'])
      expect(readFileSync(join(old.path, 'response-1.json'), 'utf8')).toBe(before)
      expect(readdirSync(current.path)).toEqual([])
      expect(current.calls).toBe(0)
    } finally { releaseHeaders(); await request.catch(() => undefined); await proxy.server.stop(true); await peer.stop(true) }
  })

  test('normal CLI completion allows EOF grace and actual child timeout preserves output', async () => {
    const peer = Bun.serve({ hostname: '127.0.0.1', port: 0, fetch() {
      return new Response(new ReadableStream({ start(controller) {
        controller.enqueue(new TextEncoder().encode('first'))
        setTimeout(() => { controller.enqueue(new TextEncoder().encode('last')); controller.close() }, 30)
      } }))
    } })
    const proxy = productionProxy(peer.url), capture = captureAt('/runs/grace/go')
    proxy.setActive(capture)
    try {
      const response = await fetch(new URL(capture.route + '/anthropic/messages', proxy.server.url), { method: 'POST', body: '{}' })
      const text = response.text()
      await proxy.finish({ timedOut: false })
      expect(await text).toBe('firstlast')
      expect(capture.errors).toEqual([])
      expect(JSON.parse(readFileSync(join(capture.path, 'response-1.json'), 'utf8')).complete).toBe(true)
      const home = temporary(), child = join(home, 'fixture-cli.sh')
      writeFileSync(child, 'trap \'printf "term-owned\\n" >&2; exit 0\' TERM\nprintf "before-term\\n"\nwhile :; do :; done\n')
      const result = await productionCommand(['/bin/bash', '--noprofile', '--norc', child], home, { HOME: home, USERPROFILE: home, CLAUDE_CONFIG_DIR: join(home, '.claude'), PATH: '/usr/bin:/bin' }, 250)
      expect(result.timedOut).toBe(true)
      expect(result.stdout).toBe('before-term\n')
      expect(result.stderr).toBe('term-owned\n')
      expect(result.durationMs).toBeLessThan(1000)
    } finally { await proxy.server.stop(true); await peer.stop(true) }
  })

  test('whole-task timeout also settles inherited output pipes held by an owned descendant', async () => {
    const fixture = ownedPipeFixture('term')
    let observer: ReturnType<typeof setTimeout> | undefined
    try {
      const result = await Promise.race([fixture.completion, new Promise<never>((_, reject) => { observer = setTimeout(() => reject(new Error('Whole-task timeout left inherited output pipes pending')), 1000) })])
      expect(result.timedOut).toBe(true)
      expect(result.stdout).toBe('descendant-pipe-open\n')
      expect(result.stderr).toBe('parent-term\n')
      expect(result.durationMs).toBeLessThan(1000)
      await verifyOwnedGroupClosed(fixture.home, result)
    } finally {
      if (observer) clearTimeout(observer)
      await cleanupOwnedPipeFixture(fixture)
    }
  })

  test('a normally exited leader cannot leave an owned descendant holding output indefinitely', async () => {
    const fixture = ownedPipeFixture('leader-exit')
    let observer: ReturnType<typeof setTimeout> | undefined
    try {
      const result = await Promise.race([fixture.completion, new Promise<never>((_, reject) => { observer = setTimeout(() => reject(new Error('Exited leader left inherited pipes pending')), 1000) })])
      expect(result.code).toBe(0)
      expect(result.timedOut).toBe(true)
      expect(result.durationMs).toBeLessThan(1000)
      expect(result.stdout).toBe('descendant-pipe-open\n')
      expect(result.stderr).toBe('')
      await verifyOwnedGroupClosed(fixture.home, result)
    } finally { if (observer) clearTimeout(observer); await cleanupOwnedPipeFixture(fixture) }
  })

  test('TERM-ignoring owned processes receive KILL after the original five-second grace and are joined', async () => {
    const fixture = ownedPipeFixture('ignore-term')
    let observer: ReturnType<typeof setTimeout> | undefined
    try {
      const result = await Promise.race([fixture.completion, new Promise<never>((_, reject) => { observer = setTimeout(() => reject(new Error('Owned group did not join after original force grace')), 7500) })])
      expect(result.timedOut).toBe(true)
      expect(result.signal).toBe('SIGKILL')
      expect(result.durationMs).toBeGreaterThanOrEqual(5000)
      expect(result.durationMs).toBeLessThan(7000)
      expect(result.stdout).toBe('descendant-pipe-open\n')
      expect(result.stderr).toBe('')
      await verifyOwnedGroupClosed(fixture.home, result)
    } finally { if (observer) clearTimeout(observer); await cleanupOwnedPipeFixture(fixture) }
  }, 10000)

})

describe('production frozen-input and failed-metric decisions', () => {
  test('the actual runner startup covers missing Source input before candidate validation and refuses restoration', async () => {
    const allocation = mkdtempSync('/tmp/go-benchmark-startup-')
    roots.push(allocation)
    writeFileSync(join(allocation, 'manifest.json'), '{}')
    let missing = true
    const argument = (name: string) => {
      if (name === '--source-root' && missing) throw new Error('Missing frozen Source input')
      return allocation
    }
    // Execute the original allocation boundary and first Source resolution;
    // no git, provider, subprocess or substitute model is entered by this case.
    const prefix = section('async function main() {', '  const frozenSource =')
    const main = new Function('argument', 'lstatSync', 'validateBatchStartup', javascript(prefix + '\n  })\n}\n') + 'return main')(argument, lstatSync, validateBatchStartup)
    await expect(main()).rejects.toThrow('Missing frozen Source input')
    expect(existsSync(join(allocation, 'invalid-batch.json'))).toBe(true)
    missing = false
    await expect(main()).rejects.toThrow('permanently')
  })

  test('the live-only Source boundary keeps native fetch identity and refuses redirects', () => {
    const source = readFileSync(join(import.meta.dir, 'go-main-source-live.ts'), 'utf8')
    const start = source.indexOf('globalThis.fetch = (('), end = source.indexOf('await import(source', start)
    expect(start).toBeGreaterThan(0); expect(end).toBeGreaterThan(start)
    const promise = Promise.resolve(new Response('loopback fixture'))
    let init: RequestInit | undefined
    const fetchOriginal = (_input: unknown, options: RequestInit) => { init = options; return promise }
    const holder = { fetch: globalThis.fetch }
    new Function('globalThis', 'fetchOriginal', 'process', javascript(source.slice(start, end)))(holder, fetchOriginal, { env: { ANTHROPIC_BASE_URL: 'http://127.0.0.1:1234/anthropic' } })
    expect(holder.fetch('http://127.0.0.1:1234/anthropic')).toBe(promise)
    expect(init?.redirect).toBe('error')
    expect(() => holder.fetch('https://outside.invalid/')).toThrow('Unexpected live Source fetch origin')
  })

  test('full actual frozen MAIN Source tree rejects changed, missing and extra inputs', async () => {
    const source = temporary()
    const env = { HOME: source, USERPROFILE: source, XDG_CONFIG_HOME: source, PATH: '/usr/bin:/bin', GIT_CONFIG_NOSYSTEM: '1', GIT_CONFIG_GLOBAL: '/dev/null', GIT_CONFIG_SYSTEM: '/dev/null' }
    const commands = [
      Bun.spawn(['/usr/bin/git', 'ls-tree', '-rz', PAIRED_PROTOCOL.main, '--', 'src', 'adapters', 'stubs', 'preload.ts', 'package.json', 'bun.lock', 'tsconfig.json'], { cwd: resolve(import.meta.dir, '../..'), env, stdout: 'pipe', stderr: 'pipe' }),
      Bun.spawn(['/usr/bin/git', 'archive', '--format=tar', PAIRED_PROTOCOL.main, 'src', 'adapters', 'stubs', 'preload.ts', 'package.json', 'bun.lock', 'tsconfig.json'], { cwd: resolve(import.meta.dir, '../..'), env, stdout: 'pipe', stderr: 'pipe' }),
    ]
    const [tree, bytes, treeCode, fileCode] = await Promise.all([new Response(commands[0].stdout).text(), new Response(commands[1].stdout).arrayBuffer(), commands[0].exited, commands[1].exited])
    expect([treeCode, fileCode]).toEqual([0, 0])
    const unpack = Bun.spawn(['/usr/bin/tar', '-xf', '-', '-C', source], { env, stdin: new Uint8Array(bytes), stdout: 'pipe', stderr: 'pipe' })
    expect(await unpack.exited).toBe(0)
    const sourceInventory = new Function('readdirSync', 'join', 'relative', 'sha256', 'readFileSync', javascript(section('function inventory(', 'function inside(')) + 'return inventory')(readdirSync, join, relative, sha256, readFileSync)
    const verify = new Function('tree', 'source', 'frozenSource', 'readFileSync', 'join', 'createHash', javascript(section('  const expectedPaths =', '  const wrapper =')))
    const frozenSource = sourceInventory(source)
    expect(frozenSource.rows.length).toBe(tree.split('\0').filter(Boolean).length)
    expect(frozenSource.rows.length).toBeGreaterThan(500)
    expect(() => verify(tree, source, frozenSource, readFileSync, join, createHash)).not.toThrow()
    expect(() => verify(tree, source, { rows: frozenSource.rows.slice(1) }, readFileSync, join, createHash)).toThrow('Missing or extra frozen MAIN inputs')
    writeFileSync(join(source, 'src/unexpected-benchmark-fixture.ts'), 'unexpected source')
    expect(() => verify(tree, source, sourceInventory(source), readFileSync, join, createHash)).toThrow('Missing or extra frozen MAIN inputs')
    rmSync(join(source, 'src/unexpected-benchmark-fixture.ts'))
    appendFileSync(join(source, 'package.json'), '\n')
    expect(() => verify(tree, source, frozenSource, readFileSync, join, createHash)).toThrow('Source is not frozen MAIN: package.json')
  })

  test('same-version dependency code and Source mutations reject the captured identity', () => {
    const source = temporary()
    for (const directory of ['src', 'adapters', 'stubs', 'node_modules/@anthropic-ai/sdk']) mkdirSync(join(source, directory), { recursive: true })
    for (const file of ['src/main.ts', 'adapters/main.ts', 'stubs/module.ts', 'preload.ts', 'package.json', 'bun.lock', 'tsconfig.json']) writeFileSync(join(source, file), 'frozen')
    writeFileSync(join(source, 'node_modules/@anthropic-ai/sdk/package.json'), '{"version":"0.128.0"}')
    writeFileSync(join(source, 'node_modules/@anthropic-ai/sdk/client.mjs'), 'original')
    const sourceInventory = new Function('readdirSync', 'join', 'relative', 'sha256', 'readFileSync', javascript(section('function inventory(', 'function inside(')) + 'return inventory')(readdirSync, join, relative, sha256, readFileSync)
    const frozen = sourceInventory(source), dependencies = freezeResolvedDependencies(source)
    writeFileSync(join(source, 'node_modules/@anthropic-ai/sdk/client.mjs'), 'changed code same version')
    expect(freezeResolvedDependencies(source).hash).not.toBe(dependencies.hash)
    expect(sourceInventory(source).hash).toBe(frozen.hash)
    writeFileSync(join(source, 'src/main.ts'), 'new source')
    expect(sourceInventory(source).hash).not.toBe(frozen.hash)
  })
  test('missing protected input and timed out result become failed evidence, not a scoring throw', () => {
    const task = taskFor('arithmetic', 0), home = temporary()
    const parentCopy = join(home, 'parent.jsonl'), manifest = { parentHash: '' }
    const immutable = new Function('task', 'home', 'parentCopy', 'manifest', 'readFileSync', 'existsSync', 'join', 'sha256', javascript(section('        const immutable =', '        const judged =')) + 'return immutable')(task, home, parentCopy, manifest, readFileSync, existsSync, join, sha256)
    expect(immutable).toBe(false)
    const pairs: Pair[] = Array.from({ length: 120 }, (_, index) => ({ id: taskFor(SCENARIOS[index % 6], Math.floor(index / 6)).id, source: { success: true, durationMs: 100, estimatedCost: 1, runtimeErrors: [] }, go: { success: true, durationMs: 100, estimatedCost: 1, runtimeErrors: [] } }))
    pairs[0].go = { success: false, durationMs: 100, estimatedCost: 0, runtimeErrors: ['Whole-task timeout', 'Immutable task or parent changed'] }
    const score = evaluatePairs(pairs)
    expect(score.status).toBe('failed')
    if (score.status !== 'insufficient') {
      expect(score.failedPairs).toBe(1)
      expect(score.cost).toBeNull()
      expect(score.missingMeasurements).toEqual([{ id: pairs[0].id, engine: 'go', key: 'estimatedCost' }])
    }
  })

  test('missing or directory parent transcript is failed immutability evidence', () => {
    const task = taskFor('history-fork', 0), home = temporary(), parentCopy = join(home, 'parent.jsonl')
    writeFileSync(join(home, 'seed.json'), task.files['seed.json'])
    const manifest = { parentHash: sha256('private frozen transcript') }
    const verify = new Function('task', 'home', 'parentCopy', 'manifest', 'readFileSync', 'existsSync', 'join', 'sha256', javascript(section('        const immutable =', '        const judged =')) + 'return immutable')
    expect(verify(task, home, parentCopy, manifest, readFileSync, existsSync, join, sha256)).toBe(false)
    mkdirSync(parentCopy)
    expect(verify(task, home, parentCopy, manifest, readFileSync, existsSync, join, sha256)).toBe(false)
    rmSync(parentCopy, { recursive: true })
    writeFileSync(parentCopy, 'private frozen transcript')
    expect(verify(task, home, parentCopy, manifest, readFileSync, existsSync, join, sha256)).toBe(true)
  })
})


// Execute the actual complete outer loop and real recorder/sealer/commit path.
// Only process exit/timeout/judge observations are controlled OS-result fixtures;
// HTTP errors and partial streams cross the actual loopback proxy. These are
// deliberately failed synthetic tasks, never live benchmark score evidence.
async function failedOuterBatch(tamper = false) {
  const allocation = temporary(), parent = join(allocation, 'private-parent.jsonl')
  const parentBytes = 'synthetic private frozen parent\n', manifest = { parentHash: sha256(parentBytes) }
  const manifestHash = sha256('synthetic failed-batch manifest'), pairs: ProvenPair[] = []
  writeFileSync(parent, parentBytes)
  writeFileSync(join(allocation, 'manifest.json'), 'synthetic failed-batch manifest')
  const cases = SCENARIOS.flatMap(scenario => Array.from({ length: PAIRED_PROTOCOL.repeats }, (_, index) => taskFor(scenario, index)))
  const calls: { engine: string, task: string, mode: number }[] = []
  let peerCalls = 0, checks = 0
  type ConsumerOutcome = { task: string; engine: string; eof: boolean; bytes: number; sha256: string; readError?: string; closedError?: string; unexpectedError?: string }
  const consumers: Promise<ConsumerOutcome>[] = []
  const openReaders = new Set<ReadableStreamDefaultReader<Uint8Array>>()
  let outcomes: ConsumerOutcome[] | undefined
  const joinConsumers = async () => {
    if (outcomes) return outcomes
    let drain: ReturnType<typeof setTimeout> | undefined
    try {
      const settled = await Promise.race([Promise.all(consumers), new Promise<undefined>(resolve => { drain = setTimeout(() => resolve(undefined), 2000) })])
      if (!settled) {
        const cancelled = await Promise.allSettled([...openReaders].map(reader => reader.cancel()))
        await Promise.all(consumers)
        throw new Error('Owned partial consumer did not settle after actual outer-loop close: ' + JSON.stringify(cancelled))
      }
      if (openReaders.size) throw new Error('Owned partial reader survived completed consumer joins')
      outcomes = settled
      return outcomes
    } finally { if (drain) clearTimeout(drain) }
  }
  const partialText = 'event: message_start\ndata: {"type":"message_start","message":{"id":"partial-only","model":"deepseek-flash","usage":{"input_tokens":11,"output_tokens":0}}}\n\n'
  const peer = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) {
    peerCalls++
    const body = await request.json() as { fixtureFailure: string }
    if (body.fixtureFailure === '503') return new Response(JSON.stringify({ type: 'error', error: { type: 'overloaded_error', message: 'private all-retries failure fixture' } }), { status: 503, headers: { 'content-type': 'application/json' } })
    if (body.fixtureFailure === 'immutable') return Response.json({ type: 'message', id: 'complete-fixture-' + peerCalls, model: PAIRED_PROTOCOL.model, stop_reason: 'end_turn', content: [{ type: 'text', text: 'complete private fixture' }], usage: { input_tokens: 11, output_tokens: 7, cache_read_input_tokens: 0, cache_creation_input_tokens: 0 } })
    if (body.fixtureFailure !== 'partial') throw new Error('Unexpected private failure fixture request')
    return new Response(new ReadableStream({ start(controller) {
      controller.enqueue(new TextEncoder().encode(partialText))
    } }), { headers: { 'content-type': 'text/event-stream' } })
  } })
  const controlledCommand = async (argv: string[], home: string, env: Record<string, string>) => {
    const startedMonotonicMs = performance.now()
    let stdout = '', stderr = '', code = 1, timedOut = false
    if (argv[0] === '/usr/bin/env') { code = 0; stdout = 'synthetic successful independent judge fixture\n' }
    else {
      const mode = calls.length % 4, engine = home.endsWith('/source') ? 'source' : 'go'
      const task = cases.find(task => home.endsWith('/' + task.id + '/' + engine))!
      calls.push({ engine, task: task.id, mode })
      const body = { model: PAIRED_PROTOCOL.model, max_tokens: PAIRED_PROTOCOL.maxOutputTokens, thinking: { type: 'disabled' }, stream: true, messages: [{ role: 'user', content: 'synthetic failed batch' }], fixtureFailure: mode === 1 ? '503' : mode === 2 ? 'partial' : 'immutable' }
      if (mode === 0) stderr = 'capability is not migrated: controlled zero-model startup failure\n'
      if (mode === 1) {
        for (let attempt = 0; attempt < 3; attempt++) {
          const response = await fetch(env.ANTHROPIC_BASE_URL + '/messages', { method: 'POST', body: JSON.stringify(body) })
          expect(response.status).toBe(503)
          await response.text()
        }
        stdout = JSON.stringify({ type: 'result', subtype: 'error_during_execution', is_error: true, num_turns: 0, total_cost_usd: 0, usage: { input_tokens: 0, output_tokens: 0 }, modelUsage: {} }) + '\n'
      }
      if (mode === 2) {
        const response = await fetch(env.ANTHROPIC_BASE_URL + '/messages', { method: 'POST', body: JSON.stringify(body) })
        const reader = response.body!.getReader()
        openReaders.add(reader)
        // Own both rejection channels immediately, before the original loop
        // aborts its upstream after this controlled OS timeout is returned.
        const closed = reader.closed.then(() => undefined, error => String(error))
        const chunks: Uint8Array[] = []
        try {
          const first = await reader.read()
          expect(first.done).toBe(false)
          chunks.push(new Uint8Array(first.value!))
        } catch (error) {
          await Promise.allSettled([reader.cancel(), closed])
          reader.releaseLock()
          openReaders.delete(reader)
          throw error
        }
        const completion = (async (): Promise<ConsumerOutcome> => {
          const outcome: ConsumerOutcome = { task: task.id, engine, eof: false, bytes: 0, sha256: '' }
          try {
            while (true) {
              const next = await reader.read()
              if (next.done) { outcome.eof = true; break }
              chunks.push(new Uint8Array(next.value))
            }
          } catch (error) { outcome.readError = String(error) }
          finally {
            outcome.closedError = await closed
            reader.releaseLock()
            openReaders.delete(reader)
          }
          const bytes = Buffer.concat(chunks)
          outcome.bytes = bytes.length
          outcome.sha256 = sha256(bytes)
          return outcome
        })().then(value => value, error => ({ task: task.id, engine, eof: false, bytes: 0, sha256: '', unexpectedError: String(error) }))
        consumers.push(completion)
        stdout = '{"type":"assistant"'
        code = 143
        timedOut = true
      }
      if (mode === 3) {
        const response = await fetch(env.ANTHROPIC_BASE_URL + '/messages', { method: 'POST', body: JSON.stringify(body) })
        const message = await response.json() as { id: string, model: string, usage: { input_tokens: number, output_tokens: number } }
        stdout = JSON.stringify({ type: 'assistant', parent_tool_use_id: null, message }) + '\n' + JSON.stringify({ type: 'result', subtype: 'success', is_error: false, num_turns: 1, total_cost_usd: .01, usage: message.usage, modelUsage: { [message.model]: { inputTokens: message.usage.input_tokens, outputTokens: message.usage.output_tokens, cacheReadInputTokens: 0, cacheCreationInputTokens: 0, costUSD: .01, contextWindow: 1000000, maxOutputTokens: 32000 } } }) + '\n'
        code = 0
        rmSync(join(home, task.immutable[0]))
      }
    }
    const endedMonotonicMs = performance.now()
    return { stdout, stderr, code, timedOut, startedMonotonicMs, endedMonotonicMs, durationMs: endedMonotonicMs - startedMonotonicMs }
  }
  const realFetch = globalThis.fetch
  const redirectedFetch = (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(input instanceof Request ? input.url : String(input))
    if (url.origin !== 'https://api.deepseek.com') throw new Error('Unapproved failed-batch fixture origin')
    return realFetch(new URL(url.pathname + url.search, peer.url), init)
  }
  const inside = new Function('relative', 'isAbsolute', 'sep', javascript(section('function inside(', 'async function command(')) + 'return inside')(relative, isAbsolute, sep)
  const fixtureProcess = { execPath: process.execPath, exitCode: 0 }
  const fixtureConsole = { log(row: { task?: string, engine?: string }) {
    if (tamper && row.task && calls.length === 1) appendFileSync(join(allocation, row.task, row.engine!, 'stdout.jsonl'), 'deliberate post-seal mutation')
  } }
  const bindings = { Bun, PAIRED_PROTOCOL, fetch: redirectedFetch, appendFileSync, writeFileSync, readFileSync, existsSync, mkdirSync, dirname, join, inside, redact: (value: string) => value, sha256, createStreamEvidence, sealEngineEvidence, validateEngineEvidence, commitCompletedPair, evaluatePairs, invalidateBatch, command: controlledCommand, cases, pairs, checkInputs: () => { checks++ }, allocation, manifestHash, manifest, parent, parentBytes, binary: '/fixture/ordinary-cli', wrapper: '/fixture/source-wrapper.ts', key: 'explicit-fake-not-provider-key', source: '/fixture/frozen-source', pilot: false, process: fixtureProcess, console: fixtureConsole }
  const body = section('  let active:', '\n  })\n}')
  const run = new Function(...Object.keys(bindings), 'return (async () => {\n' + javascript(body) + '\nreturn pairs\n})()')
  try {
    await run(...Object.values(bindings))
    return { allocation, cases, pairs, calls, peerCalls, checks, manifestHash, parentHash: manifest.parentHash, exitCode: fixtureProcess.exitCode, error: undefined, partialText, consumers: await joinConsumers() }
  } catch (error) {
    if (tamper) return { allocation, cases, pairs, calls, peerCalls, checks, manifestHash, parentHash: manifest.parentHash, exitCode: fixtureProcess.exitCode, error, partialText, consumers: await joinConsumers() }
    throw error
  } finally {
    try { await joinConsumers() } finally { await peer.stop(true) }
  }
}

describe('actual outer-loop failed observations and permanent evidence corruption', () => {
  test('zero-model crash, all503 retries, partial timeout and immutable deletion still run both engines for all120 pairs', async () => {
    const batch = await failedOuterBatch()
    expect(batch.calls).toHaveLength(240)
    expect(batch.pairs).toHaveLength(120)
    expect(batch.peerCalls).toBe(300)
    expect(batch.checks).toBe(241)
    expect(batch.consumers).toHaveLength(60)
    for (const consumer of batch.consumers) {
      expect(consumer.unexpectedError).toBeUndefined()
      // Bun can terminate a previously-started HTTP response as a normal EOF.
      // That transport observation cannot turn the recorder's partial body
      // into a complete provider response or reconciled accounting.
      if (consumer.eof) {
        expect(consumer.readError).toBeUndefined()
        expect(consumer.closedError).toBeUndefined()
      } else {
        expect(consumer.readError).toMatch(/cancel(?:ed|led)|abort|closed|terminated|ECONNRESET|socket/i)
        expect(consumer.closedError).toMatch(/cancel(?:ed|led)|abort|closed|terminated|ECONNRESET|socket/i)
      }
      expect(consumer.bytes).toBe(Buffer.byteLength(batch.partialText))
      expect(consumer.sha256).toBe(sha256(batch.partialText))
      const home = join(batch.allocation, consumer.task, consumer.engine)
      const raw = readFileSync(join(home, 'response-1.txt'))
      const request = JSON.parse(readFileSync(join(home, 'requests.jsonl'), 'utf8'))
      const wire = JSON.parse(readFileSync(join(home, 'response-1.json'), 'utf8'))
      expect(raw.toString('utf8')).toBe(batch.partialText)
      expect(wire).toEqual({ method: 'POST', status: 200, complete: false, error: 'Evidence upstream canceled', bytes: raw.length, sha256: sha256(raw), contentType: 'text/event-stream', requestHash: sha256(JSON.stringify(request)) })
      const command = JSON.parse(readFileSync(join(home, 'command.json'), 'utf8'))
      expect(command.timedOut).toBe(true)
      expect(command.recordingErrors).toEqual([])
      expect(command.evidenceErrors).toEqual(['Incomplete stream evidence: Evidence upstream canceled'])
      const sealed = JSON.parse(readFileSync(join(home, 'sealed-evidence.json'), 'utf8'))
      expect(sealed.actual.measurement.success).toBe(false)
      expect(sealed.actual.measurement.runtimeErrors).toContain('Whole-task timeout')
      expect(sealed.actual.measurement.estimatedCost).toBe(0)
      expect(sealed.actual.accountingComplete).toBe(false)
      expect(sealed.actual.missingMeasurements).toEqual(['estimatedCost'])
      expect(sealed.actual.unmeasuredRequests).toEqual([{ number: 1, reason: 'Evidence upstream canceled' }])
    }
    for (const [index, task] of batch.cases.entries()) {
      expect(batch.calls.slice(index * 2, index * 2 + 2).map(call => [call.task, call.engine])).toEqual(index % 2 === 0 ? [[task.id, 'source'], [task.id, 'go']] : [[task.id, 'go'], [task.id, 'source']])
    }
    for (const [index, call] of batch.calls.entries()) {
      const measurement = batch.pairs[Math.floor(index / 2)][call.engine as 'source' | 'go']
      const receipt = JSON.parse(readFileSync(join(batch.allocation, call.task, call.engine, 'receipt.json'), 'utf8'))
      expect(receipt.measurement).toEqual(measurement)
      if (call.mode === 3) {
        expect(measurement.runtimeErrors).toEqual(['Immutable task or parent changed'])
        expect(measurement.estimatedCost).toBe(.01)
        expect(receipt.accountingComplete).toBe(true)
      } else expect(measurement.estimatedCost).toBe(0)
    }
    expect(existsSync(join(batch.allocation, 'invalid-batch.json'))).toBe(false)
    expect(existsSync(join(batch.allocation, 'pair-in-progress.json'))).toBe(false)
    expect(batch.exitCode).toBe(1)
    const score = JSON.parse(readFileSync(join(batch.allocation, 'score.json'), 'utf8'))
    expect(score).toMatchObject({ status: 'failed', pairs: 120, failedPairs: 120, cost: null })
    expect(score.missingMeasurements).toHaveLength(180)
    expect(loadCompletedPairs(batch.allocation, batch.cases, batch.manifestHash, batch.parentHash)).toEqual(batch.pairs)
  }, 30000)
  test('a sealed raw mutation still stops and permanently invalidates rather than becoming an ordinary failed task', async () => {
    const batch = await failedOuterBatch(true)
    expect(String(batch.error)).toContain('seal mismatch')
    expect(batch.calls).toHaveLength(2)
    expect(batch.pairs).toHaveLength(0)
    expect(existsSync(join(batch.allocation, 'pairs.jsonl'))).toBe(false)
    expect(existsSync(join(batch.allocation, 'invalid-batch.json'))).toBe(true)
    expect(() => loadCompletedPairs(batch.allocation, batch.cases, batch.manifestHash, batch.parentHash)).toThrow('permanently')
  }, 30000)
})

// Append to scripts/perf/go-main-paired-benchmark-runner.test.ts.
// Uses existing section(), javascript(), temporary(), sha256, PAIRED_PROTOCOL,
// validateBatchStartup and fs/path imports. No new verifier export is imported:
// the old runner should fail these assertions because its guard is absent,
// never because an unavailable export or import prevented test collection.
// All paths and observations are synthetic private fixtures; no network/key read.
async function parentStartupAttempt(supplyReceipt: boolean, existingBatch = false, unscoredPilot = false) {
  const allocation = temporary()
  const parent = join(allocation, 'synthetic-parent.jsonl')
  const wrapper = join(allocation, 'synthetic-source-wrapper.ts')
  const parentReceipt = join(allocation, 'synthetic-parent-receipt.json')
  writeFileSync(parent, 'synthetic private frozen-parent fixture\n', { mode: 0o600 })
  writeFileSync(wrapper, '// synthetic source-wrapper fixture\n', { mode: 0o600 })
  writeFileSync(parentReceipt, '{}\n', { mode: 0o600 })
  if (existingBatch) writeFileSync(join(allocation, 'manifest.json'), 'synthetic prior batch\n', { mode: 0o600 })
  const calls: string[] = []
  let parentChecks = 0, keyArguments = 0, networkCalls = 0
  const candidate = { path: join(allocation, 'synthetic-candidate.json'), sha256: sha256('synthetic candidate receipt'), inputsHash: sha256('synthetic candidate inputs'), binaryHash: sha256('synthetic candidate binary'), target: process.platform + '/' + process.arch }
  const args = ['--allow-live', '--candidate-evidence', candidate.path, ...(unscoredPilot ? ['--pilot'] : []), ...(supplyReceipt ? ['--parent-receipt', parentReceipt] : [])]
  const argument = (name: string) => {
    calls.push(name)
    if (name === '--key-file') { keyArguments++; throw new Error('Synthetic key boundary was reached before parent admission') }
    const index = args.indexOf(name)
    if (index < 0 || !args[index + 1] || args[index + 1].startsWith('--')) throw new Error('Missing ' + name)
    return args[index + 1]
  }
  const observedBun = { version: Bun.version, revision: Bun.revision, serve() { networkCalls++; throw new Error('Synthetic network boundary reached before parent admission') } }
  const noFetch = () => { networkCalls++; throw new Error('Synthetic fetch reached before parent admission') }
  const bindings = {
    argv: args, argument, allocation, binaryHash: candidate.binaryHash,
    source: join(allocation, 'synthetic-source-root'), parent, parentBytes: readFileSync(parent),
    frozenSource: { hash: sha256('synthetic Source inventory'), rows: [] },
    dependencies: { hash: sha256('synthetic dependency inventory'), rows: [] },
    wrapper, wrapperHash: sha256(readFileSync(wrapper)), runnerHash: sha256('synthetic runner'), coreHash: sha256('synthetic core'), evidenceHash: sha256('synthetic evidence'),
    sdkPackage: { version: '0.128.0' }, axiosPackage: { version: '1.15.2' }, pilot: false, cases: [],
    PAIRED_PROTOCOL, process, Bun: observedBun, fetch: noFetch,
    sha256, readFileSync, writeFileSync, existsSync, join, invalidateBatch,
    console: { log() {} },
    verifyCandidateEvidence: () => candidate,
    verifySourceParentEvidence: () => { parentChecks++; throw new Error('Synthetic rejected Source parent provenance') },
  }
  // The continuation is the exact next production statement, giving the old
  // code a business failure (reaching --key-file) rather than a parse failure.
  const keyStart = runner.indexOf('  const keyFile =')
  const keyEnd = runner.indexOf('\n', keyStart)
  if (keyStart < 0 || keyEnd < 0) throw new Error('Production key boundary moved')
  const body = section('  const candidateFile =', '  const keyFile =') + runner.slice(keyStart, keyEnd) + '\nreturn keyFile'
  // candidate is declared by the actual production section, not injected.
  const parameters = bindings
  const AsyncFunction = Object.getPrototypeOf(async function () {}).constructor
  const invoke = new AsyncFunction(...Object.keys(parameters), javascript('async function sourceParentStartupSection() {\n' + body + '\n}') + '\nreturn sourceParentStartupSection()')
  let error: unknown
  try { await validateBatchStartup(allocation, async () => invoke(...Object.values(parameters))) } catch (failure) { error = failure }
  return { allocation, error, calls, parentChecks, keyArguments, networkCalls }
}

describe('Source parent provenance precedes credential and provider admission', () => {
  test('missing --parent-receipt rejects at the production startup boundary without reading a key', async () => {
    const attempt = await parentStartupAttempt(false)
    expect(attempt.error).toBeInstanceOf(Error)
    expect((attempt.error as Error).message).toContain('--parent-receipt')
    expect(attempt.parentChecks).toBe(0)
    expect(attempt.keyArguments).toBe(0)
    expect(attempt.networkCalls).toBe(0)
  })
  test('unscored --pilot still requires the real parent before provider admission', async () => {
    const attempt = await parentStartupAttempt(false, false, true)
    expect((attempt.error as Error).message).toContain('--parent-receipt')
    expect(attempt.parentChecks).toBe(0)
    expect(attempt.keyArguments).toBe(0)
    expect(attempt.networkCalls).toBe(0)
  })
  test('an explicitly supplied but rejected parent receipt also fails before key/provider admission', async () => {
    const attempt = await parentStartupAttempt(true)
    expect(attempt.error).toBeInstanceOf(Error)
    expect((attempt.error as Error).message).toBe('Synthetic rejected Source parent provenance')
    expect(attempt.parentChecks).toBe(1)
    expect(attempt.keyArguments).toBe(0)
    expect(attempt.networkCalls).toBe(0)
  })
  test('an existing batch permanently retains a startup provenance rejection', async () => {
    const attempt = await parentStartupAttempt(true, true)
    expect((attempt.error as Error).message).toBe('Synthetic rejected Source parent provenance')
    expect(existsSync(join(attempt.allocation, 'invalid-batch.json'))).toBe(true)
    let laterValidationCalls = 0
    await expect(validateBatchStartup(attempt.allocation, async () => { laterValidationCalls++; return 'synthetically restored' })).rejects.toThrow('permanently')
    expect(laterValidationCalls).toBe(0)
    expect(attempt.keyArguments).toBe(0)
    expect(attempt.networkCalls).toBe(0)
  })
})

// check:policy explicitly runs this test file. Keep the POSIX FD/process tests
// on that durable entry rather than leaving Python24 as a manual command.
test('policy lane executes all 24 offline Source parent producer regressions', async () => {
  if (process.platform === 'win32') throw new Error('Offline Source parent producer regressions require POSIX descriptors and process groups')
  const home = temporary()
  const env = createSandboxedTestEnvironment(home, {
    PATH: '/usr/bin:/bin', LANG: 'C.UTF-8', LC_ALL: 'C.UTF-8',
    PYTHONDONTWRITEBYTECODE: '1', PYTHONNOUSERSITE: '1', PYTHONPATH: '',
  }, {})
  const producer = join(import.meta.dir, 'source-parent-recorder/test_driver_offline.py')
  const result = await productionCommand(['python3', '-B', producer], home, env, 30_000)
  const output = result.stdout + result.stderr
  console.log('Offline Source parent producer regression output:\n' + output)
  expect(result.code, output).toBe(0)
  expect(result.timedOut, output).toBe(false)
  expect(result.joined).toBe(true)
  expect(result.processGroup).not.toBeNull()
  expect(result.stdoutComplete).toBe(true)
  expect(result.stderrComplete).toBe(true)
  expect(result.recordingErrors).toEqual([])
  expect(output).toMatch(/Ran 24 tests in /)
  expect(output).toMatch(/^OK\s*$/m)
  expect(output).not.toMatch(/\b(skipped|FAILED)\b/)
}, 40_000)


// Use each real producer env expression, then the unchanged Source Bash task
// output consumer in a new process. No real CLI/model/credential is involved.
const privateTempRepository = process.env.CC_HAHA_PRIVATE_TEMP_REPOSITORY_ROOT ?? resolve(import.meta.dir, '../..')
const privateTempProductionRoot = process.env.CC_HAHA_PRIVATE_TEMP_PRODUCTION_ROOT ?? privateTempRepository
const privateTempFixture = process.env.CC_HAHA_PRIVATE_TEMP_FIXTURE ?? join(import.meta.dir, 'source-parent-recorder/private-temp-output.fixture.ts')
function privateTempSection(file: string, start: string, end: string) {
  const text = readFileSync(join(privateTempProductionRoot, file), 'utf8')
  const from = text.indexOf(start), to = text.indexOf(end, from)
  if (from < 0 || to < 0) throw new Error('Actual private-temp producer seam moved: ' + file)
  return text.slice(from, to)
}
function privateTempHome() {
  const allocation = realpathSync(temporary()), home = join(allocation, 'source-home')
  chmodSync(allocation, 0o700)
  mkdirSync(join(home, '.claude'), { recursive: true, mode: 0o700 })
  chmodSync(home, 0o700)
  mkdirSync(join(home, 'tmp'), { mode: 0o700 })
  writeFileSync(join(home, '.claude/settings.json'), '{}', { mode: 0o600 })
  return { allocation, home }
}
async function privateTempProducer(profile: 'python-bootstrap' | 'parent-source' | 'formal-case', home: string, allocation: string) {
  if (profile === 'python-bootstrap') {
    const script = `import ast,json,sys\nfrom pathlib import Path\ntree=ast.parse(Path(sys.argv[1]).read_text())\nmain=next(n for n in tree.body if isinstance(n,ast.FunctionDef) and n.name=='main')\nnode=next(n for n in ast.walk(main) if isinstance(n,ast.Assign) and any(isinstance(t,ast.Name) and t.id=='env' for t in n.targets))\nvalue=eval(compile(ast.Expression(node.value),'actual-env-expression','eval'),{'str':str,'bootstrap':Path(sys.argv[2]),'bun':Path(sys.argv[3])})\nprint(json.dumps(value))\n`
    const environment = createSandboxedTestEnvironment(home, { PATH: '/usr/bin:/bin', PYTHONDONTWRITEBYTECODE: '1', PYTHONNOUSERSITE: '1', PYTHONPATH: '' }, {})
    const result = await productionCommand(['python3', '-B', '-c', script, join(privateTempProductionRoot, 'scripts/perf/source-parent-recorder/record_source_parent.py'), home, process.execPath], home, environment, 30_000)
    expect(result.code, result.stdout + result.stderr).toBe(0)
    expect(result.joined).toBe(true)
    expect(result.timedOut).toBe(false)
    return JSON.parse(result.stdout) as Record<string, string>
  }
  const file = profile === 'parent-source' ? 'scripts/perf/source-parent-recorder/record-source-parent.ts' : 'scripts/perf/go-main-paired-benchmark.ts'
  const text = privateTempSection(file, profile === 'parent-source' ? 'const env = {' : '        const env = {', profile === 'parent-source' ? '\nconst prompt =' : '\n        const args =')
  return new Function('home', 'allocation', 'process', 'dirname', 'key', 'PAIRED_PROTOCOL', 'source', 'transport', 'upstream', 'route', javascript(text) + '\nreturn env')(home, allocation, process, dirname, 'fake-private-temp-no-provider', PAIRED_PROTOCOL, privateTempRepository, { origin: 'http://127.0.0.1:1' }, { url: new URL('http://127.0.0.1:1') }, '/fixture') as Record<string, string>
}
async function privateTempConsumer(home: string, allocation: string, environment: Record<string, string>, action: 'compute' | 'write') {
  const env = { ...environment, HOME: home, USERPROFILE: home, CLAUDE_CONFIG_DIR: home + '/.claude', CC_HAHA_FIXTURE_TEMP_ROOT: allocation, CC_HAHA_PRIVATE_TEMP_REPOSITORY_ROOT: privateTempRepository, CC_HAHA_PRIVATE_TEMP_OUTPUT_FIXTURE: '1', ANTHROPIC_API_KEY: 'fake-private-task-output-only', DISABLE_TELEMETRY: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH: '1', BUN_OPTIONS: '--no-env-file' }
  const result = await productionCommand([process.execPath, '--no-env-file', privateTempFixture], home, env, 30_000, JSON.stringify({ home, action }))
  expect(result.code, result.stdout + result.stderr).toBe(0)
  expect(result.timedOut).toBe(false)
  expect(result.joined).toBe(true)
  expect(result.recordingErrors).toEqual([])
  expect(result.stderr).toBe('')
  return JSON.parse(result.stdout) as { privateDirectory: boolean, outputDirectory: string, outputPath: string | null, bytes: string | null, blocked: string[], secureReads: string[] }
}
function privateTempEntryGuard(environment: Record<string, string>, home: string) {
  const text = privateTempSection('scripts/perf/go-main-source-live.ts', 'const source =', '// An isolated test host')
  return new Function('process', 'realpathSync', 'lstatSync', 'isAbsolute', 'relative', 'sep', javascript(text))({ env: environment, argv: [], cwd: () => home, getuid: () => process.getuid?.() }, realpathSync, lstatSync, isAbsolute, relative, sep)
}
describe('real benchmark Source private task-output temporary boundary', () => {
  test('all three actual producer environments route real disk output through private CLAUDE_CODE_TMPDIR', async () => {
    for (const profile of ['python-bootstrap', 'parent-source', 'formal-case'] as const) {
      const { allocation, home } = privateTempHome()
      const env = await privateTempProducer(profile, home, allocation)
      const result = await privateTempConsumer(home, allocation, env, 'write')
      expect(result.privateDirectory, profile + ': ' + result.outputDirectory).toBe(true)
      expect(result.outputPath?.startsWith(home + '/tmp/')).toBe(true)
      expect(result.bytes).toBe('BENCHMARK_PRIVATE_TASK_OUTPUT\n')
      expect(result.blocked).toEqual([])
      expect(result.secureReads).toEqual([])
      expect(env.CLAUDE_CODE_TMPDIR).toBe(home + '/tmp')
      expect(env.TMPDIR).toBe(home + '/tmp')
    }
  }, 90_000)
  test('TMPDIR alone cannot route the real Source consumer and no global output write is attempted', async () => {
    const { allocation, home } = privateTempHome(), env = await privateTempProducer('parent-source', home, allocation)
    delete env.CLAUDE_CODE_TMPDIR
    const result = await privateTempConsumer(home, allocation, env, 'compute')
    expect(result.privateDirectory).toBe(false)
    expect(result.outputPath).toBeNull()
    expect(result.bytes).toBeNull()
  }, 40_000)
  test('formal Source entry rejects missing, foreign, aliased or nonprivate temporary roots before Source imports', async () => {
    const { allocation, home } = privateTempHome(), env = await privateTempProducer('formal-case', home, allocation)
    expect(() => privateTempEntryGuard(env, home)).not.toThrow()
    const missing = { ...env }
    delete missing.CLAUDE_CODE_TMPDIR
    expect(() => privateTempEntryGuard(missing, home)).toThrow('CLAUDE_CODE_TMPDIR')
    expect(() => privateTempEntryGuard({ ...env, CLAUDE_CODE_TMPDIR: allocation }, home)).toThrow('CLAUDE_CODE_TMPDIR')
    expect(() => privateTempEntryGuard({ ...env, TMPDIR: allocation }, home)).toThrow('CLAUDE_CODE_TMPDIR')
    chmodSync(join(home, 'tmp'), 0o750)
    expect(() => privateTempEntryGuard(env, home)).toThrow('CLAUDE_CODE_TMPDIR')
    chmodSync(join(home, 'tmp'), 0o700)
    const other = join(allocation, 'other-private-temp')
    mkdirSync(other, { mode: 0o700 })
    rmSync(join(home, 'tmp'), { recursive: true })
    symlinkSync(other, join(home, 'tmp'))
    expect(() => privateTempEntryGuard(env, home)).toThrow('CLAUDE_CODE_TMPDIR')
  }, 40_000)
})


describe('formal non-thinking environment and real Source request', () => {
  const sourceRoot = resolve(import.meta.dir, '../..')
  const parentProducer = readFileSync(join(sourceRoot, 'scripts/perf/source-parent-recorder/record-source-parent.ts'), 'utf8')
  const environment = (producer: 'paired' | 'parent', home: string, allocation: string, origin: string) => {
    const start = producer === 'paired' ? '        const env = {' : 'const env = {'
    const end = producer === 'paired' ? '\n        const args =' : '\nconst prompt ='
    const text = producer === 'paired' ? runner : parentProducer
    const from = text.indexOf(start), to = text.indexOf(end, from)
    if (from < 0 || to < 0) throw new Error('Actual formal environment declaration moved')
    return new Function('home', 'source', 'allocation', 'upstream', 'transport', 'route', 'key', 'dirname', 'process', 'PAIRED_PROTOCOL', javascript(text.slice(from, to)) + '\nreturn env')(
      home, sourceRoot, allocation, { url: { origin } }, { origin }, '/paired-wire', 'offline-benchmark-wire-key', dirname, process, PAIRED_PROTOCOL,
    ) as Record<string, string>
  }

  test('paired and Source-parent definitions explicitly carry the existing disabled-thinking seam', () => {
    for (const producer of ['paired', 'parent'] as const) {
      const home = temporary(), env = environment(producer, home, home, 'http://127.0.0.1:1')
      expect(env.CC_HAHA_SEND_DISABLED_THINKING).toBe('1')
      expect(env.ANTHROPIC_MODEL).toBe(PAIRED_PROTOCOL.model)
      expect(env.ANTHROPIC_DEFAULT_SONNET_MODEL).toBe(PAIRED_PROTOCOL.model)
    }
  })

  test('real Source CLI emits disabled thinking and complete tools through each formal environment', async () => {
    const requests: { producer: string, body: Record<string, any> }[] = []
    let currentProducer = ''
    const marker = 'OFFLINE_BENCHMARK_WIRE_READY'
    const peer = Bun.serve({ hostname: '127.0.0.1', port: 0, development: false, async fetch(request) {
      if (request.method === 'HEAD') return new Response(null)
      if (request.method !== 'POST' || !new URL(request.url).pathname.endsWith('/messages')) return new Response('Unexpected route', { status: 400 })
      if (request.headers.get('x-api-key') !== 'offline-benchmark-wire-key') return new Response('Unexpected fake credential', { status: 401 })
      const body = await request.json() as Record<string, any>
      requests.push({ producer: currentProducer, body })
      const usage = { input_tokens: 11, output_tokens: 0, cache_read_input_tokens: 0, cache_creation_input_tokens: 0 }
      const events = [
        { type: 'message_start', message: { id: 'offline-thinking-' + requests.length, type: 'message', role: 'assistant', model: PAIRED_PROTOCOL.model, content: [], stop_reason: null, stop_sequence: null, usage } },
        { type: 'content_block_start', index: 0, content_block: { type: 'text', text: '' } },
        { type: 'content_block_delta', index: 0, delta: { type: 'text_delta', text: marker } },
        { type: 'content_block_stop', index: 0 },
        { type: 'message_delta', delta: { stop_reason: 'end_turn', stop_sequence: null }, usage: { output_tokens: 7 } },
        { type: 'message_stop' },
      ]
      return new Response(events.map(event => 'event: ' + event.type + '\ndata: ' + JSON.stringify(event) + '\n\n').join(''), { headers: { 'content-type': 'text/event-stream' } })
    } })
    try {
      for (const producer of ['paired', 'parent'] as const) {
        currentProducer = producer
        const allocation = realpathSync(temporary()), home = join(allocation, 'source-home')
        mkdirSync(join(home, '.claude'), { recursive: true, mode: 0o700 })
        mkdirSync(join(home, 'tmp'), { mode: 0o700 })
        writeFileSync(join(home, '.claude/settings.json'), '{"disableAllHooks":true,"maxConcurrentSubagents":2}', { mode: 0o600 })
        const env = environment(producer, home, allocation, peer.url.origin)
        const args = ['--print', '--model', PAIRED_PROTOCOL.model, '--thinking', PAIRED_PROTOCOL.thinking, '--no-computer-use', '--dangerously-skip-permissions', '--output-format', 'stream-json', '--verbose', '--max-turns', String(PAIRED_PROTOCOL.maxTurns), 'Reply exactly ' + marker + '. Do not use tools.']
        const result = await productionCommand([process.execPath, '--no-env-file', join(sourceRoot, 'scripts/perf/go-main-source-live.ts'), ...args], home, env, 60_000)
        expect({ code: result.code, timedOut: result.timedOut, errors: result.recordingErrors, stderr: result.stderr }).toMatchObject({ code: 0, timedOut: false, errors: [] })
        expect(result.joined && result.stdoutComplete && result.stderrComplete).toBe(true)
        const rows = result.stdout.trim().split('\n').filter(Boolean).map(line => JSON.parse(line))
        const terminal = rows.filter(row => row.type === 'result')
        expect(terminal).toHaveLength(1)
        expect(terminal[0]).toMatchObject({ subtype: 'success', is_error: false, result: marker })
        const actual = requests.filter(row => row.producer === producer)
        expect(actual.length).toBeGreaterThan(0)
        for (const { body } of actual) {
          expect(body.model).toBe(PAIRED_PROTOCOL.model)
          expect(body.max_tokens).toBe(PAIRED_PROTOCOL.maxOutputTokens)
          expect(body.thinking).toEqual({ type: 'disabled' })
          expect(body.tools).toHaveLength(19)
          expect(body.tools.map((tool: { name: string }) => tool.name)).toEqual(expect.arrayContaining(['Agent', 'Bash', 'Read', 'Write']))
        }
      }
    } finally { await peer.stop(true) }
  }, 150_000)
})
