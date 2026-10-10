import { spyOn } from 'bun:test'
import * as fs from 'node:fs/promises'
import { lstatSync } from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as { home: string, stat: Record<string, { size: number }>, liteHelper: { path: string, mode: string } }
const home = assertDisposableFixtureHome(input.home, 'Resume lite helper')
const helper = input.liteHelper, path = join(home, 'session.jsonl'), output = join(home, 'resume-state-result.json')
if (process.env.CC_HAHA_RESUME_STATE_FIXTURE !== '1' || process.env.NODE_ENV !== 'production' || Object.keys(input).some(name => !['home', 'currentSessionId', 'bare', 'checkpointing', 'environmentKind', 'policyPath', 'stat', 'holdCopies', 'liteHelper'].includes(name)) || !helper || Object.keys(helper).some(name => !['path', 'mode'].includes(name)) || helper.path !== path || !['tail-read', 'close', 'zero-head'].includes(helper.mode) || Object.keys(input.stat).length !== 1 || !input.stat[path] || input.stat[path]!.size <= 65536) throw new Error('Direct lite helper requires exact original private fault profile')
for (const target of [path, output]) { try { if (lstatSync(target).isSymbolicLink()) throw new Error('Direct lite helper requires ordinary private files') } catch (error) { if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error } }
const capabilities = await installClientFixtureCapabilities(home)
if ((await fs.stat(path)).size !== input.stat[path]!.size) throw new Error('Original lite helper stat differs')
const calls: Array<Record<string, unknown>> = [], open = fs.open
spyOn(fs, 'open').mockImplementation(async (...args) => {
  if (String(args[0]) !== path) throw new Error('Direct lite helper requires exact original path')
  const handle = await open(...args), read = handle.read.bind(handle), close = handle.close.bind(handle)
  let ordinal = 0
  spyOn(handle, 'read').mockImplementation(async (...args: Parameters<typeof handle.read>) => {
    ordinal++; calls.push({ operation: 'read', ordinal, position: Number(args[3]) })
    if (helper.mode === 'zero-head' && ordinal === 1) return { bytesRead: 0, buffer: args[0] } as Awaited<ReturnType<typeof handle.read>>
    if (helper.mode === 'tail-read' && ordinal === 2) throw Object.assign(new Error('original-private-tail-read-fault'), { code: 'EIO' })
    return read(...args)
  })
  spyOn(handle, 'close').mockImplementation(async () => {
    calls.push({ operation: 'close' }); await close()
    if (helper.mode === 'close') throw Object.assign(new Error('original-private-close-fault'), { code: 'EIO' })
  })
  return handle
})
const portable = await import('../../../src/utils/sessionStoragePortable')
const result = await portable.readHeadAndTail(path, input.stat[path]!.size, Buffer.alloc(portable.LITE_READ_BUF_SIZE))
if (capabilities.blocked.length || capabilities.secureReads.length || capabilities.absentReads.length) throw new Error('Direct lite helper used unavailable capabilities')
await fs.writeFile(output, JSON.stringify({ ...result, calls }))
console.log(JSON.stringify({ result: 'resume-state-result.json' }))
