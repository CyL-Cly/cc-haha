import { afterEach, describe, expect, test } from 'bun:test'
import { chmodSync, linkSync, mkdirSync, mkdtempSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { createStreamEvidence, freezeResolvedDependencies } from './go-main-paired-benchmark-evidence'

const roots: string[] = []
const temporary = () => { const root = mkdtempSync(join(tmpdir(), 'go-benchmark-evidence-')); roots.push(root); return root }
afterEach(() => { for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true }) })

describe('complete dependency identity', () => {
  test('physical code, native bytes, directory topology and modes are frozen', () => {
    const source = temporary(), modules = join(source, 'node_modules')
    mkdirSync(join(modules, 'sdk'), { recursive: true })
    writeFileSync(join(modules, 'sdk/package.json'), '{"version":"0.128.0"}')
    writeFileSync(join(modules, 'sdk/client.mjs'), 'export const retries=2')
    writeFileSync(join(modules, 'sdk/engine.node'), Buffer.from([0, 255, 1]))
    const first = freezeResolvedDependencies(source)
    expect(first.rows.find(row => row.path === 'sdk/engine.node')?.hash).toHaveLength(64)
    writeFileSync(join(modules, 'sdk/client.mjs'), 'export const retries=99')
    const code = freezeResolvedDependencies(source)
    expect(code.hash).not.toBe(first.hash)
    writeFileSync(join(modules, 'sdk/engine.node'), Buffer.from([0, 255, 2]))
    const native = freezeResolvedDependencies(source)
    expect(native.hash).not.toBe(code.hash)
    mkdirSync(join(modules, 'new-empty-directory'))
    const directory = freezeResolvedDependencies(source)
    expect(directory.hash).not.toBe(native.hash)
    chmodSync(join(modules, 'sdk/client.mjs'), 0o700)
    expect(freezeResolvedDependencies(source).hash).not.toBe(directory.hash)
  })
  test('readonly canonical installation, internal aliases, hardlinks and cycles are deterministic', () => {
    const source = temporary(), shared = temporary(), modules = join(shared, 'installed')
    mkdirSync(join(modules, 'sdk'), { recursive: true })
    writeFileSync(join(modules, 'sdk/client.mjs'), 'export const ok=true')
    linkSync(join(modules, 'sdk/client.mjs'), join(modules, 'sdk/client-copy.mjs'))
    symlinkSync(modules, join(source, 'node_modules'))
    symlinkSync('sdk', join(modules, 'alias'))
    symlinkSync('..', join(modules, 'sdk/root-cycle'))
    symlinkSync('sdk/root-cycle/sdk/root-cycle/sdk/client.mjs', join(modules, 'finite-repeated-alias'))
    chmodSync(join(modules, 'sdk/client.mjs'), 0o400)
    const first = freezeResolvedDependencies(source, { canonicalRoot: modules })
    expect(first).toEqual(freezeResolvedDependencies(source, { canonicalRoot: modules }))
    expect(first.rows.find(row => row.path === 'alias')).toMatchObject({ type: 'symlink', target: 'sdk' })
    expect(first.rows.find(row => row.path === 'sdk/root-cycle')).toMatchObject({ type: 'symlink', target: '.' })
    expect(first.rows.find(row => row.path === 'finite-repeated-alias')).toMatchObject({ type: 'symlink', target: 'sdk/client.mjs' })
    expect(first.rows.filter(row => row.type === 'file')).toHaveLength(2)
    expect(first.rows.find(row => row.path === 'sdk/client.mjs')?.hash).toBe(first.rows.find(row => row.path === 'sdk/client-copy.mjs')?.hash)
    expect(() => freezeResolvedDependencies(source, { canonicalRoot: source })).toThrow('canonical root')
  })
  test('external and unresolved aliases are rejected before reading protected bytes', () => {
    const source = temporary(), modules = join(source, 'node_modules'), external = temporary()
    mkdirSync(modules)
    writeFileSync(join(external, 'protected'), 'not a dependency')
    symlinkSync(join(external, 'protected'), join(modules, 'escape'))
    expect(() => freezeResolvedDependencies(source)).toThrow('escapes canonical root')
    rmSync(join(modules, 'escape'))
    symlinkSync('b', join(modules, 'a'))
    symlinkSync('a', join(modules, 'b'))
    expect(() => freezeResolvedDependencies(source)).toThrow('symlink cycle')
    rmSync(join(modules, 'a'))
    rmSync(join(modules, 'b'))
    symlinkSync('growing/descendant', join(modules, 'growing'))
    expect(() => freezeResolvedDependencies(source)).toThrow('symlink cycle or depth')
  })
})

describe('bounded physical stream evidence', () => {
  test('complete bytes are forwarded exactly and EOF is a success', async () => {
    const bytes = new Uint8Array([0, 255, 240, 159, 152, 128])
    const evidence = createStreamEvidence(new ReadableStream({ start(controller) { controller.enqueue(bytes.subarray(0, 3)); controller.enqueue(bytes.subarray(3)); controller.close() } }), { timeoutMs: 1000 })
    expect(new Uint8Array(await new Response(evidence.consumer).arrayBuffer())).toEqual(bytes)
    expect(await evidence.completion).toEqual({ complete: true, partialBytes: bytes })
    evidence.abort()
    expect((await evidence.completion).complete).toBe(true)
  })
  test('timeout settles even if the underlying cancel never settles', async () => {
    const bytes = new TextEncoder().encode('partial')
    const evidence = createStreamEvidence(new ReadableStream({ start(controller) { controller.enqueue(bytes) }, cancel() { return new Promise(() => {}) } }), { timeoutMs: 30 })
    const consumer = new Response(evidence.consumer).arrayBuffer().catch(() => null)
    const result = await evidence.completion
    expect(result).toEqual({ complete: false, partialBytes: bytes, error: 'Evidence capture timeout' })
    expect(evidence.signal.aborted).toBe(true)
    expect(await consumer).toBeNull()
  })
  test('external abort and read/write errors preserve partial evidence without rejection', async () => {
    const caller = new AbortController()
    const evidence = createStreamEvidence(new ReadableStream({ start(controller) { controller.enqueue(new TextEncoder().encode('first')) } }), { timeoutMs: 1000, signal: caller.signal })
    const reader = evidence.consumer.getReader()
    await reader.read()
    caller.abort()
    expect(await evidence.completion).toMatchObject({ complete: false, error: 'Evidence caller canceled' })
    const failed = createStreamEvidence(new ReadableStream({ start(controller) { controller.error(new Error('raw stream fault')) } }), { timeoutMs: 1000 })
    expect(await failed.completion).toMatchObject({ complete: false, error: 'raw stream fault' })
    const write = createStreamEvidence(new ReadableStream({ start(controller) { controller.enqueue(new Uint8Array([1])) } }), { timeoutMs: 1000, onChunk() { throw new Error('evidence disk fault') } })
    expect(await write.completion).toEqual({ complete: false, partialBytes: new Uint8Array([1]), error: 'evidence disk fault' })
  })
  test('excess capture size cannot be reported as complete', async () => {
    const bytes = new Uint8Array(32 * 1024 * 1024)
    const evidence = createStreamEvidence(new ReadableStream({ start(controller) { controller.enqueue(bytes); controller.enqueue(new Uint8Array([1])); controller.close() } }), { timeoutMs: 1000 })
    expect(await evidence.completion).toMatchObject({ complete: false, error: 'Evidence capture exceeded byte limit' })
    expect((await evidence.completion).partialBytes.byteLength).toBe(bytes.byteLength)
    expect(() => createStreamEvidence(new ReadableStream(), { timeoutMs: 1000, maxBytes: 1 })).toThrow('at least 32 MiB')
  })
  test('loopback HTTP consumer cancellation aborts upstream and settles the receipt', async () => {
    let upstreamClosed = false
    const upstream = Bun.serve({ hostname: '127.0.0.1', port: 0, fetch(request) {
      request.signal.addEventListener('abort', () => { upstreamClosed = true }, { once: true })
      return new Response(new ReadableStream({ start(controller) { controller.enqueue(new TextEncoder().encode('data: live-only-fixture\n\n')) }, cancel() { upstreamClosed = true } }), { headers: { 'content-type': 'text/event-stream' } })
    } })
    let capture: ReturnType<typeof createStreamEvidence> | undefined
    const proxy = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) {
      const controller = new AbortController()
      request.signal.addEventListener('abort', () => controller.abort(), { once: true })
      const response = await fetch(upstream.url, { signal: controller.signal })
      capture = createStreamEvidence(response.body!, { abortController: controller, timeoutMs: 1000 })
      return new Response(capture.consumer)
    } })
    try {
      const caller = new AbortController()
      const response = await fetch(proxy.url, { signal: caller.signal })
      const first = await response.body!.getReader().read()
      expect(new TextDecoder().decode(first.value)).toBe('data: live-only-fixture\n\n')
      caller.abort()
      const result = await capture!.completion
      expect(result.complete).toBe(false)
      expect(new TextDecoder().decode(result.partialBytes)).toBe('data: live-only-fixture\n\n')
      expect(capture!.signal.aborted).toBe(true)
      const deadline = performance.now() + 1000
      while (!upstreamClosed && performance.now() < deadline) await Bun.sleep(5)
      expect(upstreamClosed).toBe(true)
    } finally { await proxy.stop(true); await upstream.stop(true) }
  })
})
