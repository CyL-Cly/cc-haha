import { spyOn } from 'bun:test'
import { lstatSync, realpathSync } from 'node:fs'
import * as fs from 'node:fs/promises'
import { isAbsolute, join, relative, sep } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as {
  home: string
  maxRead?: number
  failCloseCount?: number
  mutateOnRead?: {
    index: number
    operation: 'append' | 'rewrite'
    bytes: string
    preserveTime?: boolean
  }
  steps: Array<{
    operation: 'stream' | 'append' | 'rewrite'
    bytes?: string
    startOffset?: number
    endOffset?: number
    failConsumer?: boolean
    failSkipped?: boolean
    aborted?: boolean
    cancelAfterRead?: number
  }>
}
const home = assertDisposableFixtureHome(input.home, 'Session metadata')
const canonicalHome = realpathSync(home)
const path = join(home, 'metadata.jsonl')
const output = join(home, 'metadata-result.json')
function assertFile(file: string) {
  let info
  try {
    info = lstatSync(file)
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === 'ENOENT') return
    throw error
  }
  if (info.isSymbolicLink() || !info.isFile()) throw new Error('Session metadata fixture requires ordinary private files')
  const child = relative(canonicalHome, realpathSync(file))
  if (child === '..' || child.startsWith('..' + sep) || isAbsolute(child)) throw new Error('Session metadata fixture escaped HOME')
}
assertFile(path)
assertFile(output)
const capabilities = await installClientFixtureCapabilities(home)
const { streamSessionMetadata, isSessionMetadataTextTruncated } = await import('../../../src/server/services/sessionMetadataReader')
const originalOpen = (fs.open as any).getMockImplementation?.() as typeof fs.open
if (typeof originalOpen !== 'function') throw new Error('Guarded session metadata open implementation missing')
const trace: Array<{
  operation: string
  offset?: number
  length?: number
  bytesRead?: number
}> = []
let reads = 0
let closes = 0
let controller: AbortController | undefined
let cancelAt: number | undefined
spyOn(fs, 'open').mockImplementation((async (...args: Parameters<typeof fs.open>) => {
  if (String(args[0]) !== path) throw new Error('Session metadata fixture supports only its transcript')
  assertFile(path)
  trace.push({ operation: 'open' })
  const handle = await originalOpen(...args)
  const read = handle.read.bind(handle)
  const stat = handle.stat.bind(handle)
  const close = handle.close.bind(handle)
  handle.read = (async (buffer: Buffer, offset: number, length: number, position: number) => {
    reads++
    if (input.mutateOnRead?.index === reads) {
      assertFile(path)
      await (input.mutateOnRead.operation === 'append' ? fs.appendFile : fs.writeFile)(path, Buffer.from(input.mutateOnRead.bytes, 'hex'))
      const seconds = input.mutateOnRead.preserveTime ? 1_700_000_000 : 1_700_000_020
      await fs.utimes(path, seconds, seconds)
    }
    const value = await read(buffer, offset, Math.min(length, input.maxRead ?? length), position)
    trace.push({ operation: 'read', offset: position, length, bytesRead: value.bytesRead })
    if (reads === cancelAt) controller?.abort()
    return value
  }) as typeof handle.read
  handle.stat = (async (...args: Parameters<typeof handle.stat>) => {
    trace.push({ operation: 'stat' })
    return stat(...args)
  }) as typeof handle.stat
  handle.close = async () => {
    trace.push({ operation: 'close' })
    await close()
    closes++
    if (closes <= (input.failCloseCount ?? 0)) throw new Error('fixture close failed')
  }
  return handle
}) as typeof fs.open)
const results: unknown[] = []
for (const [index, step] of input.steps.entries()) {
  try {
    if (step.operation === 'append' || step.operation === 'rewrite') {
      assertFile(path)
      await (step.operation === 'append' ? fs.appendFile : fs.writeFile)(path, Buffer.from(step.bytes ?? '', 'hex'))
      await fs.utimes(path, 1_700_000_010 + index, 1_700_000_010 + index)
      results.push({ bytes: Buffer.from('{"ok":true}').toString('hex') })
      continue
    }
    controller = new AbortController()
    cancelAt = step.cancelAfterRead
    if (step.aborted) controller.abort()
    const entries: unknown[] = []
    let skipped = 0
    const scan = await streamSessionMetadata(path, (entry, complete, start) => {
      if (step.failConsumer) throw new Error('fixture consumer failed')
      entries.push({ entry, complete, start, truncated: isSessionMetadataTextTruncated(entry) })
    }, controller.signal, {
      startOffset: step.startOffset,
      endOffset: step.endOffset,
      onSkipped: () => {
        if (step.failSkipped) throw new Error('fixture skipped failed')
        skipped++
      },
    })
    results.push({ bytes: Buffer.from(JSON.stringify({ entries, scan, skipped })).toString('hex') })
  } catch (error) {
    const failure = error as {
      code?: string | number
      statusCode?: number
      message?: string
      name?: string
    }
    results.push({ error: { name: failure.name, code: failure.code ?? 'ERROR', ...(failure.statusCode ? { status: failure.statusCode } : {}), message: failure.message } })
  }
}
assertFile(output)
if (capabilities.blocked.length) throw new Error('Session metadata fixture blocked external capability: ' + capabilities.blocked.join(', '))
await fs.writeFile(output, JSON.stringify({ results, trace }))
console.log(JSON.stringify({ result: 'metadata-result.json' }))
