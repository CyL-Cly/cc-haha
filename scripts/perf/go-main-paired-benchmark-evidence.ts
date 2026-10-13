import { createHash } from 'node:crypto'
import { lstatSync, readFileSync, readdirSync, readlinkSync, realpathSync } from 'node:fs'
import { dirname, isAbsolute, join, relative, resolve, sep } from 'node:path'

type DependencyRow = { path: string; type: 'directory' | 'file' | 'symlink'; mode?: number; hash?: string; target?: string; link?: string }
export type DependencyInventory = { hash: string; canonicalRoot: string; rows: DependencyRow[] }

const digest = (bytes: string | Uint8Array) => createHash('sha256').update(bytes).digest('hex')
const ordered = (a: string, b: string) => a < b ? -1 : a > b ? 1 : 0

/** Read the complete installed dependency tree, including native executables.
 * Only the explicitly resolved node_modules root can be followed. Alias rows
 * preserve resolution topology while physical files and directory cycles are
 * read once. This never writes to the shared, possibly readonly installation.
 */
export function freezeResolvedDependencies(sourceRoot: string, options: { canonicalRoot?: string } = {}): DependencyInventory {
  const installed = realpathSync(join(sourceRoot, 'node_modules'))
  const canonicalRoot = options.canonicalRoot ? realpathSync(options.canonicalRoot) : installed
  if (installed !== canonicalRoot || !lstatSync(canonicalRoot).isDirectory()) throw new Error('Unexpected dependency canonical root')
  const nameOf = (path: string) => relative(canonicalRoot, path).split(sep).join('/') || '.'
  const admit = (path: string) => {
    const name = relative(canonicalRoot, path)
    if (name === '..' || name.startsWith('..' + sep) || isAbsolute(name)) throw new Error('Dependency symlink escapes canonical root')
    return path
  }
  // Resolve links a segment at a time, so even metadata is never read through
  // an intermediate link into an unapproved external installation or HOME.
  const resolveInternal = (path: string, resolutions = new Set<string>()): string => {
    path = admit(resolve(path))
    if (resolutions.has(path)) throw new Error('Unresolvable dependency symlink cycle')
    if (resolutions.size >= 64) throw new Error('Unresolvable dependency symlink cycle or depth')
    const nextResolutions = new Set(resolutions).add(path)
    const segments = relative(canonicalRoot, path).split(sep).filter(Boolean)
    let current = canonicalRoot
    for (let i = 0; i < segments.length; i++) {
      current = join(current, segments[i])
      if (!lstatSync(current).isSymbolicLink()) continue
      const target = admit(resolve(dirname(current), readlinkSync(current)))
      return resolveInternal(join(target, ...segments.slice(i + 1)), nextResolutions)
    }
    return current
  }
  const rows: DependencyRow[] = []
  const visited = new Set<string>()
  const hashes = new Map<string, string>()
  const visit = (path: string) => {
    if (visited.has(path)) return
    visited.add(path)
    const stat = lstatSync(path, { bigint: true })
    const row = { path: nameOf(path), mode: Number(stat.mode & 0o7777n) }
    if (stat.isSymbolicLink()) {
      const link = readlinkSync(path)
      const target = resolveInternal(path)
      rows.push({ path: row.path, type: 'symlink', link, target: nameOf(target) })
      visit(target)
    } else if (stat.isDirectory()) {
      rows.push({ ...row, type: 'directory' })
      for (const name of readdirSync(path).sort(ordered)) visit(join(path, name))
    } else if (stat.isFile()) {
      const identity = `${stat.dev}:${stat.ino}`
      let hash = hashes.get(identity)
      if (!hash) { hash = digest(readFileSync(path)); hashes.set(identity, hash) }
      rows.push({ ...row, type: 'file', hash })
    } else throw new Error('Unsupported dependency tree entry: ' + row.path)
  }
  visit(canonicalRoot)
  rows.sort((a, b) => ordered(a.path, b.path))
  return { hash: digest(JSON.stringify(rows)), canonicalRoot, rows }
}

export type StreamEvidenceResult = { complete: boolean; partialBytes: Uint8Array; error?: string }
export type StreamEvidenceOptions = {
  timeoutMs: number
  maxBytes?: number
  signal?: AbortSignal
  abortController?: AbortController
  onChunk?: (chunk: Uint8Array) => void
}

/** Forward and record one upstream reader. A native tee can leave its evidence
 * reader pending after the HTTP consumer disconnects, so this owns both sides.
 * completion always settles, preserving partial bytes on cancellation/failure.
 * Pass abortController.signal to the upstream fetch before calling this helper
 * when cancellation must also abort an outstanding physical HTTP request.
 */
export function createStreamEvidence(body: ReadableStream<Uint8Array>, options: StreamEvidenceOptions) {
  if (!Number.isSafeInteger(options.timeoutMs) || options.timeoutMs <= 0 || options.timeoutMs > 2_147_483_647) throw new Error('Invalid evidence timeout')
  const maxBytes = options.maxBytes ?? 32 * 1024 * 1024
  if (!Number.isSafeInteger(maxBytes) || maxBytes < 32 * 1024 * 1024) throw new Error('Evidence limit must be at least 32 MiB')
  const abortController = options.abortController ?? new AbortController()
  const reader = body.getReader()
  const chunks: Uint8Array[] = []
  let length = 0
  let settled = false
  let output!: ReadableStreamDefaultController<Uint8Array>
  let finish!: (result: StreamEvidenceResult) => void
  let timer: ReturnType<typeof setTimeout> | undefined
  const completion = new Promise<StreamEvidenceResult>(resolve => { finish = resolve })
  const safeError = (failure: unknown) => failure instanceof Error ? failure.message : typeof failure === 'string' ? failure : 'Evidence stream failed'
  const settle = (error?: string, consumerCanceled = false) => {
    if (settled) return
    if (error === '') error = 'Evidence stream failed'
    settled = true
    if (timer) clearTimeout(timer)
    options.signal?.removeEventListener('abort', externalAbort)
    abortController.signal.removeEventListener('abort', controllerAbort)
    const partialBytes = new Uint8Array(length)
    let offset = 0
    for (const chunk of chunks) { partialBytes.set(chunk, offset); offset += chunk.length }
    chunks.length = 0
    if (!consumerCanceled) {
      try { if (error) output.error(new Error(error)); else output.close() } catch { /* Consumer may already have closed. */ }
    }
    finish({ complete: error === undefined, partialBytes, ...(error ? { error } : {}) })
    if (error) {
      // Neither a stuck underlying cancel nor an already failed reader may
      // prevent the evidence receipt from settling within the deadline.
      try { void reader.cancel(error).catch(() => {}) } catch { /* Already closed. */ }
      if (!abortController.signal.aborted) abortController.abort(new Error(error))
    }
  }
  const abort = (reason = 'Evidence capture canceled') => settle(reason)
  const externalAbort = () => abort('Evidence caller canceled')
  const controllerAbort = () => abort('Evidence upstream canceled')
  const consumer = new ReadableStream<Uint8Array>({
    start(controller) { output = controller },
    cancel() { settle('Evidence consumer canceled', true) },
  })
  options.signal?.addEventListener('abort', externalAbort, { once: true })
  abortController.signal.addEventListener('abort', controllerAbort, { once: true })
  timer = setTimeout(() => abort('Evidence capture timeout'), options.timeoutMs)
  if (options.signal?.aborted) externalAbort()
  else if (abortController.signal.aborted) controllerAbort()
  else void (async () => {
    try {
      while (!settled) {
        const read = await reader.read()
        if (settled) return
        if (read.done) { settle(); return }
        if (length + read.value.byteLength > maxBytes) { abort('Evidence capture exceeded byte limit'); return }
        const chunk = new Uint8Array(read.value)
        chunks.push(chunk)
        length += chunk.length
        options.onChunk?.(chunk)
        output.enqueue(read.value)
      }
    } catch (error) { settle(safeError(error)) }
  })()
  return { consumer, completion, abort, signal: abortController.signal }
}
