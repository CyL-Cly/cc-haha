import { spyOn } from 'bun:test'
import { lstatSync, realpathSync, type Stats } from 'node:fs'
import * as fs from 'node:fs/promises'
import { createHash } from 'node:crypto'
import { isAbsolute, join, relative, sep } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as {
  home: string
  steps: Array<{ operation: string; cursorFrom?: number; previous?: boolean; cursor?: string; limit?: number; full?: boolean; startOffset?: number; endOffset?: number; maxRecordBytes?: number; failConsumer?: boolean; bytes?: string; raw?: string; modifyCursor?: Record<string, unknown>; legacy?: boolean }>
  maxRead?: number
  mutateOnRead?: { index: number; operation: 'append' | 'rewrite'; bytes: string; preserveTime?: boolean }
  hashResults?: boolean
  failCloseCount?: number
}
const home = assertDisposableFixtureHome(input.home, 'Session reader')
const canonicalHome = realpathSync(home)
function assertKnownFixtureFile(path: string) {
  let snapshot: Stats
  try {
    snapshot = lstatSync(path)
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === 'ENOENT') return
    throw error
  }
  if (snapshot.isSymbolicLink() || !snapshot.isFile()) throw new Error('Session reader fixture requires ordinary private files')
  const child = relative(canonicalHome, realpathSync(path))
  if (child === '..' || child.startsWith(`..${sep}`) || isAbsolute(child)) throw new Error('Session reader fixture requires ordinary private files')
}
const path = join(home, 'session.jsonl')
const resultPath = join(home, 'reader-result.json')
assertKnownFixtureFile(path)
assertKnownFixtureFile(resultPath)
const capabilities = await installClientFixtureCapabilities(home)
const { readBoundedHistoryPage, streamBoundedHistory, displayPreview, withHistoryReadBudget } = await import('../../../src/server/services/boundedSessionHistory')
const results: Array<{ bytes?: string; error?: { code: string; status?: number; message?: string } }> = []
const pages: Array<Awaited<ReturnType<typeof readBoundedHistoryPage>> | undefined> = []
const trace: Array<{ operation: string; offset?: number; length?: number; bytesRead?: number }> = []
const originalOpen = fs.open
let reads = 0
let closes = 0
spyOn(fs, 'open').mockImplementation((async (...args: Parameters<typeof fs.open>) => {
  const filePath = String(args[0])
  const subpath = relative(home, filePath)
  if (subpath === '..' || subpath.startsWith('../') || isAbsolute(subpath)) throw new Error('Session reader escaped HOME')
  assertKnownFixtureFile(filePath)
  trace.push({ operation: 'open' })
  const handle = await originalOpen(...args)
  const read = handle.read.bind(handle)
  const stat = handle.stat.bind(handle)
  const close = handle.close.bind(handle)
  handle.read = (async (buffer: Buffer, offset: number, length: number, position: number) => {
    reads++
    if (input.mutateOnRead?.index === reads) {
      const mutation = input.mutateOnRead
      assertKnownFixtureFile(path)
      await (mutation.operation === 'append' ? fs.appendFile : fs.writeFile)(path, Buffer.from(mutation.bytes, 'hex'))
      const stamp = mutation.preserveTime ? 1_700_000_000 : 1_700_000_020
      await fs.utimes(path, stamp, stamp)
    }
    const result = await read(buffer, offset, Math.min(length, input.maxRead ?? length), position)
    trace.push({ operation: 'read', offset: position, length, bytesRead: result.bytesRead })
    return result
  }) as typeof handle.read
  handle.stat = (async (...statArgs: Parameters<typeof handle.stat>) => { trace.push({ operation: 'stat' }); return stat(...statArgs) }) as typeof handle.stat
  handle.close = async () => { trace.push({ operation: 'close' }); await close(); closes++; if (closes <= (input.failCloseCount ?? 0)) throw new Error('fixture close failed') }
  return handle
}) as typeof fs.open)
for (const [index, step] of input.steps.entries()) {
  try {
    if (step.operation === 'append' || step.operation === 'rewrite') {
      assertKnownFixtureFile(path)
      await (step.operation === 'append' ? fs.appendFile : fs.writeFile)(path, Buffer.from(step.bytes ?? '', 'hex'))
      await fs.utimes(path, 1_700_000_010 + index, 1_700_000_010 + index)
      results.push({ bytes: Buffer.from(JSON.stringify({ ok: true })).toString('hex') })
      continue
    }
    let value: unknown
    if (step.operation === 'page') {
      let cursor = step.cursor
      if (step.cursorFrom !== undefined) cursor = (step.previous ? pages[step.cursorFrom]?.page.previousCursor : pages[step.cursorFrom]?.page.nextCursor) ?? undefined
      if (cursor && (step.modifyCursor || step.legacy)) {
        const decoded = JSON.parse(Buffer.from(cursor, 'base64url').toString('utf8'))
        if (step.legacy) delete decoded.fingerprints
        Object.assign(decoded, step.modifyCursor ?? {})
        cursor = Buffer.from(JSON.stringify(decoded)).toString('base64url')
      }
      const page = await readBoundedHistoryPage(path, { ...(cursor !== undefined ? { cursor } : {}), ...(step.limit !== undefined ? { limit: step.limit } : {}), full: step.full })
      pages[index] = page
      value = page
    } else if (step.operation === 'stream') {
      const entries: Array<{ entry: Record<string, unknown>; complete: boolean; start: number }> = []
      let skipped = 0
      const scan = await streamBoundedHistory(path, (entry, complete, start) => {
        if (step.failConsumer) throw new Error('fixture consumer failed')
        entries.push({ entry, complete, start })
      }, undefined, { startOffset: step.startOffset, endOffset: step.endOffset, maxRecordBytes: step.maxRecordBytes, onSkipped: () => { skipped++ } })
      value = { entries, scan, skipped }
    } else if (step.operation === 'cancelled') {
      const controller = new AbortController()
      controller.abort()
      try { await readBoundedHistoryPage(path, { signal: controller.signal }) }
      catch (error) { const failure = error as DOMException; value = { name: failure.name, message: failure.message, code: failure.code } }
    } else if (step.operation === 'admission') {
      const lane = step.raw as 'page' | 'context' | 'recovery' | 'metadata'
      const [capacity, queue] = { page: [2, 8], context: [1, 4], recovery: [1, 2], metadata: [2, 128] }[lane]!
      const started: number[] = []
      const failures: Array<{ name: string; message: string; code?: string; status?: number }> = []
      const controllers = Array.from({ length: capacity! + queue! + 1 }, () => new AbortController())
      const releases: Array<() => void> = []
      const entered: Array<() => void> = []
      const holds = Array.from({ length: controllers.length + 2 }, (_, index) => new Promise<void>(resolve => { releases[index] = resolve }))
      const begins = Array.from({ length: controllers.length + 2 }, (_, index) => new Promise<void>(resolve => { entered[index] = resolve }))
      const begin = (index: number, signal?: AbortSignal) => withHistoryReadBudget(signal, async () => { started.push(index); entered[index]!(); await holds[index] }, lane).catch(error => {
        failures.push({ name: error.name, message: error.message, ...(typeof error.code === 'string' ? { code: error.code } : {}), ...(error.statusCode ? { status: error.statusCode } : {}) })
      })
      const jobs = controllers.map((controller, index) => begin(index, controller.signal))
      await new Promise(resolve => setTimeout(resolve, 0))
      controllers[capacity!]!.abort(new DOMException('tab closed', 'AbortError'))
      await new Promise(resolve => setTimeout(resolve, 0))
      jobs.push(begin(controllers.length))
      // Keep the other active slots held so callback scheduling cannot obscure
      // FIFO grants; release one queued read at a time on both runtimes.
      let previous = 0
      for (const index of [...Array.from({ length: queue! - 1 }, (_, index) => capacity! + 1 + index), controllers.length]) {
        releases[previous]!()
        await begins[index]
        previous = index
      }
      releases[previous]!()
      for (let index = 1; index < capacity!; index++) releases[index]!()
      await Promise.all(jobs)
      releases[controllers.length + 1]!()
      await begin(controllers.length + 1)
      value = { started, failures }
    } else if (step.operation === 'preview') value = displayPreview(JSON.parse(step.raw ?? '{}'))
    else throw new Error('Invalid session reader operation')
    const serialized = Buffer.from(JSON.stringify(value))
    results.push({ bytes: input.hashResults ? JSON.stringify({ bytes: serialized.length, sha256: createHash('sha256').update(serialized).digest('hex') }) : serialized.toString('hex') })
  } catch (error) {
    const failure = error as { code?: string; statusCode?: number; message?: string }
    if (failure.code) results.push({ error: { code: failure.code, ...(failure.statusCode ? { status: failure.statusCode, message: failure.message } : {}) } })
    else results.push({ error: { code: 'ERROR', message: failure.message } })
  }
}
assertKnownFixtureFile(resultPath)
if (capabilities.blocked.length) throw new Error(`Session reader fixture blocked external capability: ${capabilities.blocked.join(', ')}`)
await fs.writeFile(resultPath, JSON.stringify({ results, trace }))
console.log(JSON.stringify({ result: 'reader-result.json' }))
