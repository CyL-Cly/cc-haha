import { expect, test } from 'bun:test'
import { createHash } from 'node:crypto'
import { chmodSync, closeSync, constants, existsSync, lstatSync, mkdirSync, mkdtempSync, openSync, readFileSync, realpathSync, rmSync, unlinkSync, writeFileSync, writeSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { gunzipSync } from 'node:zlib'
import { createGoCoverageStorage, goCoverageStorageMode } from './go-coverage-storage'

const hash = (bytes: Buffer | string) => createHash('sha256').update(bytes).digest('hex')
const line = 'fixture.test/leaf.go:3.1,3.9 1 1\n'
const bytes = (count: number) => Buffer.from('mode: atomic\n' + line.repeat(count))
const posix = test.skipIf(process.platform === 'win32')
function fixture() {
  const root = realpathSync(mkdtempSync(join(tmpdir(), 'go-profile-storage-')))
  chmodSync(root, 0o700)
  mkdirSync(join(root, 'child-coverage'), { mode: 0o700 })
  mkdirSync(join(root, 'home'), { mode: 0o700 })
  return root
}
async function emit(root: string, path: string, count: number) {
  const source = `import { writeFileSync } from 'node:fs'
const path = process.argv.at(-2)
const count = Number(process.argv.at(-1))
writeFileSync(path, 'mode: atomic\\n' + ${JSON.stringify(line)}.repeat(count))
`
  const home = join(root, 'home')
  const child = Bun.spawn([process.execPath, '--no-env-file', '-e', source, path, String(count)], {
    env: { PATH: '/usr/bin:/bin', HOME: home, USERPROFILE: home, CFFIXED_USER_HOME: home, CLAUDE_CONFIG_DIR: join(home, '.claude'), TMPDIR: root, BUN_OPTIONS: '--no-env-file' },
    stdin: 'ignore', stdout: 'pipe', stderr: 'pipe',
  })
  const [exitCode, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
  return { exitCode, stdout, stderr }
}
async function collect(input: AsyncIterable<Buffer | string>) {
  const chunks: Buffer[] = []
  for await (const chunk of input) chunks.push(Buffer.from(chunk))
  return Buffer.concat(chunks)
}

test('requires an explicit supported Go coverage storage mode', () => {
  expect(goCoverageStorageMode(undefined)).toBe('regular')
  expect(goCoverageStorageMode('regular')).toBe('regular')
  expect(goCoverageStorageMode('gzip-fifo')).toBe('gzip-fifo')
  for (const value of ['', 'gzip', 'GZIP-FIFO', 'regular,gzip-fifo']) expect(() => goCoverageStorageMode(value)).toThrow('must be regular or gzip-fifo')
})

posix('captures a real child writer, verifies every raw byte and restores without changing its format', async () => {
  const root = fixture()
  const store = createGoCoverageStorage({ directory: root })
  const path = join(root, 'coverage.out')
  try {
    const result = await store.capture(path, () => emit(root, path, 20000))
    expect(result).toEqual({ exitCode: 0, stdout: '', stderr: '' })
    expect(existsSync(path)).toBe(false)
    expect(store.has(path)).toBe(true)
    const profile = store.evidence().profiles[0].descriptor!
    expect(profile.rawBytes).toBe(bytes(20000).length)
    expect(profile.rawSHA256).toBe(hash(bytes(20000)))
    expect(profile.archiveSHA256).toBe(hash(readFileSync(profile.archive)))
    expect(await store.read(path, collect)).toEqual(bytes(20000))
    expect(gunzipSync(readFileSync(profile.archive))).toEqual(bytes(20000))
    expect(profile.restore).toMatchObject({ destination: path, mode: 0o600, sourceWasFIFO: true, originalRegularMetadataExisted: false, inodeAndCtimeRestorable: false })
    // Explicit, byte-exact restoration is separate from capture and replay.
    writeFileSync(profile.restore.destination, gunzipSync(readFileSync(profile.archive)), { flag: 'wx', mode: profile.restore.mode })
    expect(readFileSync(path)).toEqual(bytes(20000))
    expect(lstatSync(path).mode & 0o777).toBe(0o600)
    await store.close()
    expect(store.evidence()).toMatchObject({ ownedStreams: 0, ownedAnchors: 0 })
    expect(JSON.parse(readFileSync(store.evidence().manifest!, 'utf8')).profiles[0].descriptor).toEqual(profile)
  } finally {
    await store.close()
    rmSync(root, { recursive: true, force: true })
  }
}, 10000)

posix('joins the FIFO reader when the producer never opens it or throws before spawn', async () => {
  for (const throws of [false, true]) {
    const root = fixture()
    const store = createGoCoverageStorage({ directory: root })
    const path = join(root, 'coverage.out')
    try {
      await expect(store.capture(path, async () => {
        if (throws) throw new Error('owned producer could not start')
        return 0
      })).rejects.toThrow(throws ? 'owned producer could not start' : 'did not emit a complete atomic profile')
      expect(existsSync(path)).toBe(false)
      expect(store.has(path)).toBe(false)
      await store.close()
      expect(store.evidence()).toMatchObject({ ownedStreams: 0, ownedAnchors: 0, profiles: [{ status: 'failed', producerAwaited: true }] })
    } finally {
    await store.close()
    rmSync(root, { recursive: true, force: true })
  }
  }
}, 10000)

posix('retains the original producer failure after a real complete write', async () => {
  const root = fixture()
  const store = createGoCoverageStorage({ directory: root })
  const path = join(root, 'coverage.out')
  try {
    await expect(store.capture(path, async () => {
      expect((await emit(root, path, 5000)).exitCode).toBe(0)
      throw new Error('original actual command exited 7')
    })).rejects.toThrow('original actual command exited 7')
    expect(store.has(path)).toBe(false)
    expect(existsSync(join(root, 'coverage-storage/0.profile.gz.partial'))).toBe(true)
    await store.close()
    expect(store.evidence()).toMatchObject({ ownedStreams: 0, ownedAnchors: 0 })
  } finally {
    await store.close()
    rmSync(root, { recursive: true, force: true })
  }
}, 10000)

posix('drains a real large producer after sink failure and preserves that first error instead of EPIPE', async () => {
  const root = fixture()
  const store = createGoCoverageStorage({ directory: root })
  const path = join(root, 'coverage.out')
  const sentinel = join(root, 'coverage-storage/0.profile.gz.partial')
  writeFileSync(sentinel, 'preexisting owned sink sentinel')
  let actual: Awaited<ReturnType<typeof emit>> | undefined
  try {
    await expect(store.capture(path, async () => {
      actual = await emit(root, path, 100000)
      return actual
    })).rejects.toThrow('EEXIST')
    expect(actual).toEqual({ exitCode: 0, stdout: '', stderr: '' })
    expect(readFileSync(sentinel, 'utf8')).toBe('preexisting owned sink sentinel')
    expect(existsSync(path)).toBe(false)
    await store.close()
    expect(store.evidence()).toMatchObject({ ownedStreams: 0, ownedAnchors: 0 })
  } finally {
    await store.close()
    rmSync(root, { recursive: true, force: true })
  }
}, 10000)

posix('records cancellation, drains and joins the already-owned real writer without leaking a FIFO', async () => {
  const root = fixture()
  const controller = new AbortController()
  const store = createGoCoverageStorage({ directory: root, signal: controller.signal })
  const path = join(root, 'coverage.out')
  let actual: Awaited<ReturnType<typeof emit>> | undefined
  try {
    await expect(store.capture(path, async () => {
      const writing = emit(root, path, 100000)
      controller.abort(new Error('owned capture cancelled'))
      actual = await writing
      return actual
    })).rejects.toThrow('owned capture cancelled')
    expect(actual).toEqual({ exitCode: 0, stdout: '', stderr: '' })
    expect(existsSync(path)).toBe(false)
    await store.close()
    expect(store.evidence()).toMatchObject({ ownedStreams: 0, ownedAnchors: 0 })
  } finally {
    await store.close()
    rmSync(root, { recursive: true, force: true })
  }
}, 10000)

posix('rejects absent, changed or truncated archives before accepting any coverage bytes', async () => {
  for (const mutation of ['truncate', 'change', 'remove'] as const) {
    const root = fixture()
    const store = createGoCoverageStorage({ directory: root })
    const path = join(root, 'coverage.out')
    try {
      expect((await store.capture(path, () => emit(root, path, 1000))).exitCode).toBe(0)
      const archive = store.evidence().profiles[0].descriptor!.archive
      if (mutation === 'remove') unlinkSync(archive)
      else {
        const original = readFileSync(archive)
        const changed = Buffer.from(original)
        changed[Math.floor(changed.length / 2)] ^= 1
        writeFileSync(archive, mutation === 'truncate' ? original.subarray(0, original.length - 5) : changed)
      }
      await expect(store.read(path, collect)).rejects.toThrow()
      await store.close()
      expect(store.evidence()).toMatchObject({ ownedStreams: 0, ownedAnchors: 0 })
    } finally {
    await store.close()
    rmSync(root, { recursive: true, force: true })
  }
  }
}, 10000)

posix('rejects no-consumer, prefix-only and throwing consumers and joins decompression', async () => {
  const root = fixture()
  const store = createGoCoverageStorage({ directory: root })
  const path = join(root, 'coverage.out')
  try {
    expect((await store.capture(path, () => emit(root, path, 100000))).exitCode).toBe(0)
    const original = readFileSync(store.evidence().profiles[0].descriptor!.archive)
    await expect(store.read(path, async () => undefined)).rejects.toThrow('complete profile')
    await expect(store.read(path, async input => { for await (const _chunk of input) break })).rejects.toThrow()
    await expect(store.read(path, async () => { throw new Error('original compiler block conflict') })).rejects.toThrow('original compiler block conflict')
    expect(readFileSync(store.evidence().profiles[0].descriptor!.archive)).toEqual(original)
    expect(await store.read(path, collect)).toEqual(bytes(100000))
    await store.close()
    expect(store.evidence()).toMatchObject({ ownedStreams: 0, ownedAnchors: 0 })
  } finally {
    await store.close()
    rmSync(root, { recursive: true, force: true })
  }
}, 10000)

posix('refuses outside and repeated paths before launching another producer', async () => {
  const root = fixture()
  const store = createGoCoverageStorage({ directory: root })
  const path = join(root, 'coverage.out')
  let invoked = false
  try {
    await expect(store.capture(join(root, '..', 'outside.cover'), async () => { invoked = true })).rejects.toThrow('Unowned')
    expect(invoked).toBe(false)
    expect((await store.capture(path, () => emit(root, path, 1))).exitCode).toBe(0)
    await expect(store.capture(path, async () => { invoked = true })).rejects.toThrow('already exists')
    expect(invoked).toBe(false)
  } finally {
    await store.close()
    rmSync(root, { recursive: true, force: true })
  }
}, 10000)

posix('leaves a replaced FIFO target untouched and rejects the capture', async () => {
  const root = fixture()
  const store = createGoCoverageStorage({ directory: root })
  const path = join(root, 'coverage.out')
  try {
    await expect(store.capture(path, async () => {
      unlinkSync(path)
      writeFileSync(path, 'replacement is not owned by the FIFO cleanup')
    })).rejects.toThrow('FIFO was replaced')
    expect(readFileSync(path, 'utf8')).toBe('replacement is not owned by the FIFO cleanup')
    await store.close()
    expect(store.evidence()).toMatchObject({ ownedStreams: 0, ownedAnchors: 0 })
  } finally {
    await store.close()
    rmSync(root, { recursive: true, force: true })
  }
}, 10000)


posix('rejects a still-open writer after producer exit and joins its nonblocking reader within the storage bound', async () => {
  const root = fixture()
  const store = createGoCoverageStorage({ directory: root })
  const path = join(root, 'coverage.out')
  let heldWriter: number | undefined
  const started = Date.now()
  try {
    await expect(store.capture(path, async () => {
      heldWriter = openSync(path, constants.O_WRONLY | constants.O_NONBLOCK)
      writeSync(heldWriter, bytes(1))
    })).rejects.toThrow('did not reach EOF after the owned producer exited')
    expect(Date.now() - started).toBeLessThan(14000)
    expect(existsSync(path)).toBe(false)
    await store.close()
    expect(store.evidence()).toMatchObject({ ownedStreams: 0, ownedAnchors: 0, ownedReaders: 0 })
  } finally {
    if (heldWriter !== undefined) closeSync(heldWriter)
    await store.close()
    rmSync(root, { recursive: true, force: true })
  }
}, 15000)


posix('bounds settlement even while a separate real writer continuously fills the FIFO after producer return', async () => {
  const root = fixture()
  const store = createGoCoverageStorage({ directory: root })
  const path = join(root, 'coverage.out')
  let child: ReturnType<typeof Bun.spawn> | undefined
  let fixtureWatchdog: ReturnType<typeof setTimeout> | undefined
  let stdoutReader: ReadableStreamDefaultReader<Uint8Array> | undefined
  const started = Date.now()
  try {
    await expect(store.capture(path, async () => {
      const source = `import { openSync, writeSync, closeSync } from 'node:fs'
const fd = openSync(process.argv.at(-1), 'w')
writeSync(fd, 'mode: atomic\\n')
process.stdout.write('ready\\n')
const until = Date.now() + 30000
const block = ${JSON.stringify(line)}.repeat(8192)
while (Date.now() < until) writeSync(fd, block)
closeSync(fd)
`
      child = Bun.spawn([process.execPath, '--no-env-file', '-e', source, path], {
        env: { PATH: '/usr/bin:/bin', HOME: join(root, 'home'), TMPDIR: root, BUN_OPTIONS: '--no-env-file' },
        stdin: 'ignore', stdout: 'pipe', stderr: 'pipe',
      })
      // This only bounds the deliberately outliving fixture writer. The
      // coverage storage must independently reject by its own earlier bound.
      fixtureWatchdog = setTimeout(() => child?.kill(), 16000)
      stdoutReader = (child.stdout as ReadableStream<Uint8Array>).getReader()
      const first = await stdoutReader.read()
      expect(Buffer.from(first.value ?? []).toString()).toBe('ready\n')
      return 0
    })).rejects.toThrow('did not reach EOF after the owned producer exited')
    expect(Date.now() - started).toBeLessThan(14000)
    expect(existsSync(path)).toBe(false)
    await store.close()
    expect(store.evidence()).toMatchObject({ ownedStreams: 0, ownedAnchors: 0, ownedReaders: 0 })
  } finally {
    if (fixtureWatchdog) clearTimeout(fixtureWatchdog)
    if (child) {
      if (child.exitCode === null) child.kill()
      try {
        stdoutReader ??= (child.stdout as ReadableStream<Uint8Array>).getReader()
        const reader = stdoutReader
        await Promise.all([child.exited, (async () => {
          while (!(await reader.read()).done) { /* drain this already-used stream to EOF */ }
        })(), new Response(child.stderr as ReadableStream).text()])
      } finally {
        stdoutReader?.releaseLock()
      }
    }
    await store.close()
    rmSync(root, { recursive: true, force: true })
  }
}, 20000)


posix('drains the real producer when a sink fails during a write whose callback is lost', async () => {
  const root = fixture()
  // Module replacement is confined to this owned worker. It injects a missing
  // callback after a sink error; it does not assert a Bun runtime root cause.
  const source = String.raw`
import { mock } from 'bun:test'
import * as originalFS from 'node:fs'
import * as originalZlib from 'node:zlib'
import { Transform, Writable } from 'node:stream'
import { createHash } from 'node:crypto'
import { join } from 'node:path'
const fs = { ...originalFS }
const zlib = { ...originalZlib }
let compressorWrites = 0
let sinkWrites = 0
mock.module('node:fs', () => ({ ...fs, createWriteStream() {
  return new Writable({ write(_chunk, _encoding, done) {
    sinkWrites += 1
    queueMicrotask(() => done(Object.assign(new Error('owned pending sink ENOSPC'), { code: 'ENOSPC' })))
  } })
} }))
mock.module('node:zlib', () => ({ ...zlib, createGzip() {
  const compressor = new Transform({ transform(chunk, _encoding, done) { this.push(chunk); done() } })
  const write = compressor.write.bind(compressor)
  compressor.write = (chunk, _callback) => {
    compressorWrites += 1
    // The stream really transports the chunk into the failing sink; only the
    // caller's completion callback is intentionally lost in this fixture.
    return write(chunk, () => {})
  }
  return compressor
} }))
const root = process.argv.at(-2)
const { createGoCoverageStorage } = await import(process.argv.at(-1))
const path = join(root, 'coverage.out')
const line = 'fixture.test/leaf.go:3.1,3.9 1 1\n'
const raw = Buffer.from('mode: atomic\n' + line.repeat(100000))
const hash = createHash('sha256').update(raw).digest('hex')
const store = createGoCoverageStorage({ directory: root })
let producer
let producerChild
let producerCompletion
let producerExited = false
try {
let failure
try {
  await store.capture(path, async () => {
    const program = 'import { writeFileSync } from "node:fs"; writeFileSync(process.argv.at(-1), ' + JSON.stringify('mode: atomic\n') + ' + ' + JSON.stringify(line) + '.repeat(100000))'
    const home = join(root, 'home')
    const child = producerChild = Bun.spawn([process.execPath, '--no-env-file', '-e', program, path], {
      env: { PATH: '/usr/bin:/bin', HOME: home, USERPROFILE: home, CFFIXED_USER_HOME: home, CLAUDE_CONFIG_DIR: join(home, '.claude'), TMPDIR: root, BUN_OPTIONS: '--no-env-file' },
      stdin: 'ignore', stdout: 'pipe', stderr: 'pipe',
    })
    const terminal = child.exited.then(code => { producerExited = true; return code })
    producerCompletion = Promise.all([terminal, new Response(child.stdout).text(), new Response(child.stderr).text()])
    const [exitCode, stdout, stderr] = await producerCompletion
    producer = { exitCode, stdout, stderr }
    return producer
  })
} catch (error) { failure = error.message }
await store.close()
const evidence = store.evidence()
if (!failure?.includes('owned pending sink ENOSPC') || JSON.stringify(producer) !== JSON.stringify({ exitCode: 0, stdout: '', stderr: '' })
  || compressorWrites !== 1 || sinkWrites !== 1 || evidence.ownedStreams || evidence.ownedAnchors || evidence.ownedReaders
  || evidence.profiles[0]?.status !== 'failed' || evidence.profiles[0]?.producerAwaited !== true
  || evidence.profiles[0]?.observedRawBytes !== raw.length || evidence.profiles[0]?.observedRawSHA256 !== hash
  || evidence.profiles[0]?.descriptor !== undefined || fs.existsSync(path)) throw new Error('pending-write sink failure did not fully drain and join the real producer')
process.stdout.write(JSON.stringify({ producer, compressorWrites, sinkWrites, rawBytes: raw.length, ownedStreams: evidence.ownedStreams, ownedAnchors: evidence.ownedAnchors, ownedReaders: evidence.ownedReaders, qualifiedCapture: false }) + '\n')
} finally {
  // Unexpected worker exceptions still join the exact producer handle. The
  // parent watchdog owns the still-live worker's group only for old-code RED.
  if (producerChild && !producerExited) producerChild.kill('SIGKILL')
  if (producerCompletion) await Promise.allSettled([producerCompletion])
}
`
  const { spawn } = await import('node:child_process')
  const home = join(root, 'home')
  const child = spawn(process.execPath, ['--no-env-file', '-e', source, root, new URL('./go-coverage-storage.ts', import.meta.url).href], {
    detached: true,
    env: { PATH: '/usr/bin:/bin', HOME: home, USERPROFILE: home, CFFIXED_USER_HOME: home, CLAUDE_CONFIG_DIR: join(home, '.claude'), TMPDIR: root, BUN_OPTIONS: '--no-env-file' },
    stdio: ['ignore', 'pipe', 'pipe'],
  })
  let exited = false
  const terminal = new Promise<{ code: number | null; signal: string | null }>((resolve, reject) => {
    child.once('error', reject)
    child.once('exit', (code, signal) => { exited = true; resolve({ code, signal }) })
  })
  const collect = (stream: NonNullable<typeof child.stdout>) => new Promise<string>((resolve, reject) => {
    const chunks: Buffer[] = []
    stream.on('data', chunk => chunks.push(Buffer.from(chunk)))
    stream.once('error', reject)
    stream.once('end', () => resolve(Buffer.concat(chunks).toString()))
  })
  const stdout = collect(child.stdout!)
  const stderr = collect(child.stderr!)
  // Existing 10s fixture test bound remains unchanged. This only cleans the
  // deliberately hanging old-code worker and its same-group fixture writer.
  const cleanup = () => {
    if (!exited && child.pid) {
      try { process.kill(-child.pid, 'SIGKILL') } catch (error) { if ((error as NodeJS.ErrnoException).code !== 'ESRCH') throw error }
    }
  }
  const watchdog = setTimeout(cleanup, 9000)
  try {
    const [result, out, err] = await Promise.all([terminal, stdout, stderr])
    expect(result).toEqual({ code: 0, signal: null })
    expect(err).toBe('')
    expect(JSON.parse(out)).toMatchObject({ producer: { exitCode: 0, stdout: '', stderr: '' }, compressorWrites: 1, sinkWrites: 1, ownedStreams: 0, ownedAnchors: 0, ownedReaders: 0, qualifiedCapture: false })
  } finally {
    clearTimeout(watchdog)
    cleanup()
    await Promise.allSettled([terminal, stdout, stderr])
    rmSync(root, { recursive: true, force: true })
  }
}, 10000)
