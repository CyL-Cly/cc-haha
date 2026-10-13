import { afterEach, describe, expect, test } from 'bun:test'
import { appendFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { join, relative, resolve } from 'node:path'
import { createHash } from 'node:crypto'
import { tmpdir } from 'node:os'
import { createStreamEvidence, freezeResolvedDependencies } from './go-main-paired-benchmark-evidence'
import { PAIRED_PROTOCOL, SCENARIOS, evaluatePairs, sha256, taskFor, type Pair } from './go-main-paired-benchmark-core'

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

type Capture = { path: string, route: string, calls: number, writes: Promise<void>[], controllers: Set<AbortController>, errors: string[] }
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
  const build = new Function('Bun', 'fetch', 'appendFileSync', 'writeFileSync', 'createStreamEvidence', 'PAIRED_PROTOCOL', 'redact', javascript(proxy) + `
return { server: upstream, setActive(value) { active=value },
  async finish(result) { ${javascript(finish)} return {calls, evidenceErrors} }
}`)
  const result = build(observedBun, redirected, appendFileSync, writeFileSync, createStreamEvidence, PAIRED_PROTOCOL, (value: string) => value) as {
    server: ReturnType<typeof Bun.serve>
    setActive(value: Capture | undefined): void
    finish(result: { timedOut: boolean }): Promise<{ calls: number, evidenceErrors: string[] }>
  }
  return { ...result, observed(path: string) { return new Promise<void>(resolve => { seen.set(path, resolve) }) } }
}

const productionCommand = new Function('Bun', javascript(section('async function command(', '\nasync function main()')) + 'return command')(Bun) as (command: string[], cwd: string, env: Record<string, string>, timeout: number) => Promise<{ stdout: string, stderr: string, code: number, timedOut: boolean, durationMs: number }>

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
        expect(result.evidenceErrors.join(' ')).toContain('Request evidence failed')
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
      proxy.setActive(current)
      releaseHeaders()
      expect((await request).status).toBe(410)
      expect(readdirSync(old.path)).toEqual(['requests.jsonl'])
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
})

describe('production frozen-input and failed-metric decisions', () => {
  test('full actual frozen MAIN Source tree rejects changed, missing and extra inputs', async () => {
    const source = temporary()
    const env = { HOME: source, USERPROFILE: source, XDG_CONFIG_HOME: source, PATH: '/usr/bin:/bin', GIT_CONFIG_NOSYSTEM: '1', GIT_CONFIG_GLOBAL: '/dev/null', GIT_CONFIG_SYSTEM: '/dev/null' }
    const commands = [
      Bun.spawn(['/usr/bin/git', 'ls-tree', '-rz', PAIRED_PROTOCOL.main, '--', 'src', 'adapters', 'preload.ts', 'package.json', 'bun.lock'], { cwd: resolve(import.meta.dir, '../..'), env, stdout: 'pipe', stderr: 'pipe' }),
      Bun.spawn(['/usr/bin/git', 'archive', '--format=tar', PAIRED_PROTOCOL.main, 'src', 'adapters', 'preload.ts', 'package.json', 'bun.lock'], { cwd: resolve(import.meta.dir, '../..'), env, stdout: 'pipe', stderr: 'pipe' }),
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
    for (const directory of ['src', 'adapters', 'node_modules/@anthropic-ai/sdk']) mkdirSync(join(source, directory), { recursive: true })
    for (const file of ['src/main.ts', 'adapters/main.ts', 'preload.ts', 'package.json', 'bun.lock']) writeFileSync(join(source, file), 'frozen')
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
