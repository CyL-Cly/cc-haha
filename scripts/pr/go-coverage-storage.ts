import { createHash } from 'node:crypto'
import { closeSync, constants, createReadStream, createWriteStream, fsyncSync, fstatSync, linkSync, lstatSync, mkdirSync, openSync, readSync, realpathSync, unlinkSync, writeFileSync } from 'node:fs'
import { basename, dirname, isAbsolute, join, relative, resolve, sep } from 'node:path'
import { Readable, Transform, type TransformCallback } from 'node:stream'
import { finished, pipeline } from 'node:stream/promises'
import { spawnSync } from 'node:child_process'
import { createGunzip, createGzip } from 'node:zlib'
import { setTimeout as delay } from 'node:timers/promises'

export type GoCoverageStorageMode = 'regular' | 'gzip-fifo'
type Identity = { dev: number; ino: number; uid: number; gid: number; mode: number; nlink: number }
export type StoredGoCoverageProfile = {
  schemaVersion: 1
  logicalPath: string
  archive: string
  archiveSHA256: string
  archiveBytes: number
  archiveIdentity: Identity
  rawSHA256: string
  rawBytes: number
  fifoIdentity: Identity
  encoding: 'gzip'
  restore: { destination: string; mode: number; sourceWasFIFO: true; originalRegularMetadataExisted: false; inodeAndCtimeRestorable: false }
}
type StorageRecord = { logicalPath: string; status: 'complete' | 'failed'; descriptor?: StoredGoCoverageProfile; error?: string; producerAwaited: boolean; observedRawBytes?: number; observedRawSHA256?: string }
export type GoCoverageStorage = {
  capture<T>(path: string, producer: () => Promise<T>): Promise<T>
  read<T>(path: string, consumer: (input: Readable) => Promise<T>): Promise<T>
  has(path: string): boolean
  close(): Promise<void>
  evidence(): { schemaVersion: 1; mode: 'gzip-fifo'; profiles: StorageRecord[]; manifest?: string; ownedStreams: number; ownedAnchors: number; ownedReaders: number }
}

export function goCoverageStorageMode(value: string | undefined): GoCoverageStorageMode {
  if (value === undefined || value === 'regular') return 'regular'
  if (value === 'gzip-fifo') return value
  throw new Error('CC_HAHA_GO_PROFILE_STORAGE must be regular or gzip-fifo')
}

function identity(path: string): Identity {
  const value = lstatSync(path)
  return { dev: value.dev, ino: value.ino, uid: value.uid, gid: value.gid, mode: value.mode & 0o777, nlink: value.nlink }
}
function sameIdentity(a: Identity, b: Identity) {
  return Object.keys(a).every(key => a[key as keyof Identity] === b[key as keyof Identity])
}
function sameDirectoryIdentity(a: Identity, b: Identity) {
  // Directory link counts change as this store creates its own entries on macOS.
  return (['dev', 'ino', 'uid', 'gid', 'mode'] as const).every(key => a[key] === b[key])
}
function missing(path: string) {
  try { lstatSync(path) } catch (error) {
    if ((error as NodeJS.ErrnoException).code === 'ENOENT') return true
    throw error
  }
  return false
}
async function fileSHA(path: string) {
  const hash = createHash('sha256')
  for await (const chunk of createReadStream(path)) hash.update(chunk)
  return hash.digest('hex')
}
function syncFile(path: string) {
  const fd = openSync(path, constants.O_RDONLY | constants.O_NOFOLLOW)
  try { fsyncSync(fd) } finally { closeSync(fd) }
}
function message(error: unknown) { return error instanceof Error ? error.message : String(error) }
function failure(errors: unknown[], context: string): Error {
  return new AggregateError(errors, `${context}: ${errors.map(message).join('; ')}`)
}

// This store owns only one fresh quality artifact directory and its declared
// parent/child profile paths. The real Go commands remain owned by the caller.
export function createGoCoverageStorage(options: {
  directory: string
  parentProfile?: string
  childDirectory?: string
  signal?: AbortSignal
}): GoCoverageStorage {
  if (process.platform === 'win32') throw new Error('gzip-fifo Go coverage storage requires POSIX named pipes')
  const directory = resolve(options.directory)
  const parent = resolve(options.parentProfile ?? join(directory, 'coverage.out'))
  const children = resolve(options.childDirectory ?? join(directory, 'child-coverage'))
  const root = lstatSync(directory)
  if (!root.isDirectory() || root.isSymbolicLink() || realpathSync(directory) !== directory || root.uid !== process.getuid!()) throw new Error('Go coverage storage requires an owned physical artifact directory')
  const inside = (path: string) => isAbsolute(path) && path.startsWith(directory + sep) && !relative(directory, path).split(sep).includes('..')
  if (!inside(parent) || !inside(children)) throw new Error('Go coverage profile paths must stay inside the artifact directory')
  const storageDirectory = join(directory, 'coverage-storage')
  mkdirSync(storageDirectory, { mode: 0o700 })
  const rootIdentity = identity(directory)
  const storageIdentity = identity(storageDirectory)
  const records: StorageRecord[] = []
  const descriptors = new Map<string, StoredGoCoverageProfile>()
  const streams = new Set<Readable | NodeJS.WritableStream>()
  const anchors = new Set<number>()
  const readers = new Set<number>()
  const settlementTimeoutMs = 10000
  let active: Promise<unknown> | undefined
  let closed = false
  let manifest: string | undefined
  const guard = () => {
    if (!sameDirectoryIdentity(rootIdentity, identity(directory)) || realpathSync(directory) !== directory
      || !sameDirectoryIdentity(storageIdentity, identity(storageDirectory)) || realpathSync(storageDirectory) !== storageDirectory) throw new Error('Go coverage storage directory identity changed')
  }
  const profile = (input: string) => {
    guard()
    const path = resolve(input)
    if (path !== input || !inside(path) || realpathSync(dirname(path)) !== dirname(path)
      || (path !== parent && (dirname(path) !== children || !/^child-[A-Za-z0-9._-]+\.cover$/.test(basename(path))))) throw new Error('Unowned Go coverage profile path')
    return path
  }
  const track = <T extends Readable | NodeJS.WritableStream>(stream: T): T => {
    streams.add(stream)
    stream.once('close', () => streams.delete(stream))
    return stream
  }
  const operation = async <T>(run: () => Promise<T>): Promise<T> => {
    if (closed || active) throw new Error('Go coverage storage is closed or already active')
    const task = Promise.resolve().then(run)
    active = task
    try { return await task } finally { active = undefined }
  }
  const save = (path: string, value: unknown) => {
    writeFileSync(path, JSON.stringify(value, null, 2) + '\n', { flag: 'wx', mode: 0o600 })
    syncFile(path)
    syncFile(storageDirectory)
  }
  const verifyArchive = async (descriptor: StoredGoCoverageProfile) => {
    guard()
    const info = lstatSync(descriptor.archive)
    if (!info.isFile() || info.isSymbolicLink() || info.nlink !== 1 || info.size !== descriptor.archiveBytes
      || !sameIdentity(identity(descriptor.archive), descriptor.archiveIdentity)
      || realpathSync(descriptor.archive) !== descriptor.archive || !descriptor.archive.startsWith(storageDirectory + sep)
      || await fileSHA(descriptor.archive) !== descriptor.archiveSHA256) throw new Error('Go coverage archive identity or SHA changed')
  }
  const checkedRead = async <T>(descriptor: StoredGoCoverageProfile, consumer: (input: Readable) => Promise<T>) => {
    await verifyArchive(descriptor)
    if (options.signal?.aborted) throw options.signal.reason ?? new Error('Go coverage read cancelled')
    const errors: unknown[] = []
    const source = track(createReadStream(descriptor.archive))
    const unzip = track(createGunzip())
    let bytes = 0
    let eof = false
    const hash = createHash('sha256')
    const output = track(new Transform({
      transform(chunk: Buffer, _encoding: BufferEncoding, done: TransformCallback) {
        bytes += chunk.length
        hash.update(chunk)
        done(null, chunk)
      },
      flush(done: TransformCallback) {
        eof = true
        done()
      },
    }))
    const cancel = () => {
      const error = options.signal?.reason ?? new Error('Go coverage read cancelled')
      errors.push(error)
      source.destroy(error instanceof Error ? error : new Error(String(error)))
      output.destroy(error instanceof Error ? error : new Error(String(error)))
    }
    options.signal?.addEventListener('abort', cancel, { once: true })
    const pumped = pipeline(source, unzip, output).catch(error => { errors.push(error) })
    if (options.signal?.aborted) cancel()
    let value: T | undefined
    try {
      try { value = await consumer(output) } catch (error) { errors.push(error) }
      // A successful consumer must drain the complete original bytes. Never
      // accept a valid prefix, and do not leave a blocked decompressor behind.
      if (!eof || !output.readableEnded) errors.push(new Error('Go coverage consumer did not consume the complete profile'))
    } finally {
      options.signal?.removeEventListener('abort', cancel)
      if (!eof || !output.readableEnded) {
        output.destroy()
        source.destroy()
      }
      await pumped
      await Promise.all([finished(source).catch(() => {}), finished(output).catch(() => {}), finished(unzip).catch(() => {})])
    }
    if (!eof || bytes !== descriptor.rawBytes || hash.digest('hex') !== descriptor.rawSHA256) errors.push(new Error('Go coverage decompressed SHA or byte count mismatch'))
    if (errors.length) throw failure(errors, 'Go coverage read failed')
    return value as T
  }
  return {
    capture<T>(input: string, producer: () => Promise<T>) {
      return operation(async () => {
        const path = profile(input)
        if (options.signal?.aborted) throw options.signal.reason ?? new Error('Go coverage capture cancelled')
        if (!missing(path) || descriptors.has(path)) throw new Error('Go coverage profile already exists')
        const ordinal = records.length
        const partial = join(storageDirectory, `${ordinal}.profile.gz.partial`)
        const archive = join(storageDirectory, `${ordinal}.profile.gz`)
        const record: StorageRecord = { logicalPath: path, status: 'failed', producerAwaited: false }
        records.push(record)
        const errors: unknown[] = []
        let fifo: Identity | undefined
        let anchor: number | undefined
        let readerFD: number | undefined
        let inputStream: Readable | undefined
        let gzip: ReturnType<typeof createGzip> | undefined
        let output: ReturnType<typeof createWriteStream> | undefined
        let pumping: Promise<void> | undefined
        let zipped: Promise<void> | undefined
        let compressionFailed = false
        let rejectPendingWrite: ((error: unknown) => void) | undefined
        let producerTerminalAt: number | undefined
        let rawBytes = 0
        let header = Buffer.alloc(0)
        const hash = createHash('sha256')
        let value: T | undefined
        const stopCompression = (error: unknown) => {
          errors.push(error)
          compressionFailed = true
          const rejectWrite = rejectPendingWrite
          rejectPendingWrite = undefined
          rejectWrite?.(error)
          gzip?.destroy(error instanceof Error ? error : new Error(String(error)))
        }
        const cancel = () => stopCompression(options.signal?.reason ?? new Error('Go coverage capture cancelled'))
        try {
          const made = spawnSync('/usr/bin/mkfifo', ['-m', '600', path], { env: { PATH: '/usr/bin:/bin', LANG: 'C' }, encoding: 'utf8', timeout: 10000, maxBuffer: 65536 })
          if (made.error || made.status !== 0 || made.stdout || made.stderr) throw made.error ?? new Error(`Cannot create Go coverage FIFO: ${made.status} ${made.stderr}`)
          fifo = identity(path)
          if (!lstatSync(path).isFIFO() || fifo.nlink !== 1 || fifo.uid !== root.uid || fifo.mode !== 0o600) throw new Error('Unexpected Go coverage FIFO identity')
          anchor = openSync(path, constants.O_RDWR | constants.O_NONBLOCK | constants.O_NOFOLLOW)
          anchors.add(anchor)
          const fd = openSync(path, constants.O_RDONLY | constants.O_NONBLOCK | constants.O_NOFOLLOW)
          readerFD = fd
          readers.add(fd)
          if (!fstatSync(fd).isFIFO()) throw new Error('Go coverage reader is not a FIFO')
          // No blocking fs read may survive the original producer. This reader
          // still drains during archive failure/cancellation, then requires EOF
          // within a finite storage-only settlement period after producer exit.
          inputStream = track(Readable.from((async function* () {
            try {
              while (true) {
                if (producerTerminalAt !== undefined && Date.now() - producerTerminalAt >= settlementTimeoutMs) throw new Error('Go coverage FIFO did not reach EOF after the owned producer exited')
                const chunk = Buffer.allocUnsafe(64 * 1024)
                let count: number
                try { count = readSync(fd, chunk) } catch (error) {
                  const code = (error as NodeJS.ErrnoException).code
                  if (code !== 'EAGAIN' && code !== 'EWOULDBLOCK') throw error
                  await delay(2)
                  continue
                }
                if (count === 0) return
                yield chunk.subarray(0, count)
              }
            } finally {
              closeSync(fd)
              readers.delete(fd)
            }
          })(), { objectMode: false }))
          gzip = track(createGzip({ level: 1 }))
          output = track(createWriteStream(partial, { flags: 'wx', mode: 0o600 }))
          zipped = pipeline(gzip, output).catch(error => { stopCompression(error) })
          const reader = inputStream
          const compressor = gzip
          pumping = (async () => {
            try {
              for await (const part of reader) {
                const chunk = Buffer.isBuffer(part) ? part : Buffer.from(part)
                rawBytes += chunk.length
                hash.update(chunk)
                if (header.length < 13) header = Buffer.concat([header, chunk.subarray(0, 13 - header.length)])
                if (!compressionFailed) {
                  try {
                    await new Promise<void>((resolveWrite, rejectWrite) => {
                      let settled = false
                      const rejectThisWrite = (error: unknown) => settle(error)
                      const settle = (error?: unknown) => {
                        if (settled) return
                        settled = true
                        if (rejectPendingWrite === rejectThisWrite) rejectPendingWrite = undefined
                        if (error) rejectWrite(error)
                        else resolveWrite()
                      }
                      rejectPendingWrite = rejectThisWrite
                      try { compressor.write(chunk, settle) } catch (error) { settle(error) }
                    })
                  } catch (error) { stopCompression(error) }
                }
                // When the archive sink fails, keep draining the original Go
                // writer until its unchanged owned command is terminal.
              }
            } catch (error) { stopCompression(error) }
            finally {
              if (!compressionFailed) compressor.end()
              await zipped
            }
          })()
          options.signal?.addEventListener('abort', cancel, { once: true })
          if (options.signal?.aborted) cancel()
          try { value = await producer() } catch (error) { errors.push(error) }
          finally {
            record.producerAwaited = true
            producerTerminalAt = Date.now()
          }
        } catch (error) { errors.push(error) }
        finally {
          options.signal?.removeEventListener('abort', cancel)
          if (anchor !== undefined) {
            try {
              closeSync(anchor)
              anchors.delete(anchor)
            } catch (error) { errors.push(error) }
          }
          producerTerminalAt ??= Date.now()
          if (!pumping) inputStream?.destroy()
          if (!zipped) {
            gzip?.destroy()
            output?.destroy()
          }
          if (pumping) await pumping
          if (inputStream) await finished(inputStream).catch(error => { errors.push(error) })
          // An async generator destroyed before its first next() does not run
          // its finally block, so setup failure also closes the reader here.
          if (readerFD !== undefined && readers.has(readerFD)) {
            try {
              closeSync(readerFD)
              readers.delete(readerFD)
            } catch (error) { errors.push(error) }
          }
          if (zipped) await zipped
          if (gzip) await finished(gzip).catch(error => { if (!compressionFailed) errors.push(error) })
          if (output) await finished(output).catch(error => { if (!compressionFailed) errors.push(error) })
          if (fifo) {
            try {
              if (!sameIdentity(fifo, identity(path))) errors.push(new Error('Go coverage FIFO was replaced'))
              else unlinkSync(path)
            } catch (error) { errors.push(error) }
          }
        }
        const rawSHA256 = hash.digest('hex')
        record.observedRawBytes = rawBytes
        record.observedRawSHA256 = rawSHA256
        try {
          if (!record.producerAwaited || rawBytes === 0 || header.toString() !== 'mode: atomic\n') errors.push(new Error('Go coverage producer did not emit a complete atomic profile'))
          if (errors.length) throw failure(errors, 'Go coverage capture failed')
          syncFile(partial)
          const archiveSHA256 = await fileSHA(partial)
          const archiveBytes = lstatSync(partial).size
          linkSync(partial, archive)
          unlinkSync(partial)
          syncFile(storageDirectory)
          const descriptor: StoredGoCoverageProfile = {
            schemaVersion: 1, logicalPath: path, archive, archiveSHA256, archiveBytes, archiveIdentity: identity(archive),
            rawSHA256, rawBytes, fifoIdentity: fifo!, encoding: 'gzip',
            restore: { destination: path, mode: fifo!.mode, sourceWasFIFO: true, originalRegularMetadataExisted: false, inodeAndCtimeRestorable: false },
          }
          await checkedRead(descriptor, async stream => { for await (const _chunk of stream) { /* complete CRC/SHA validation */ } })
          record.status = 'complete'
          record.descriptor = descriptor
          save(join(storageDirectory, `${ordinal}.profile.json`), record)
          descriptors.set(path, descriptor)
          return value as T
        } catch (error) {
          record.status = 'failed'
          record.error = message(error)
          // If persistence itself failed, keep its exact original error. The
          // final manifest still records failure; never replace it with EEXIST.
          const receipt = join(storageDirectory, `${ordinal}.profile.json`)
          if (missing(receipt)) {
            try { save(receipt, record) } catch (saveError) { throw failure([error, saveError], 'Go coverage failure evidence could not be saved') }
          }
          throw error
        }
      })
    },
    read<T>(input: string, consumer: (input: Readable) => Promise<T>) {
      return operation(async () => {
        const path = profile(input)
        const descriptor = descriptors.get(path)
        if (!descriptor) throw new Error('Go coverage profile has no completed archive')
        return checkedRead(descriptor, consumer)
      })
    },
    has(input: string) { return descriptors.has(profile(input)) },
    async close() {
      if (closed) return
      closed = true
      if (active) await active.catch(() => {})
      guard()
      if (anchors.size || readers.size || streams.size) throw new Error('Go coverage storage still owns open streams or descriptors')
      manifest = join(storageDirectory, 'manifest.json')
      save(manifest, { schemaVersion: 1, mode: 'gzip-fifo', profiles: records, settlementTimeoutMs, restorationRequiresExplicitDecode: true })
    },
    evidence() { return { schemaVersion: 1, mode: 'gzip-fifo', profiles: structuredClone(records), manifest, ownedStreams: streams.size, ownedAnchors: anchors.size, ownedReaders: readers.size } },
  }
}
