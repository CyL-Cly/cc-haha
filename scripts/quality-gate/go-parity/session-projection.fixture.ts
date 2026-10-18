import { spyOn } from 'bun:test'
import { lstatSync, realpathSync } from 'node:fs'
import * as fs from 'node:fs/promises'
import { dirname, isAbsolute, join, relative, sep } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities } from './client-options-capabilities'

type Step = {
  operation: 'projection' | 'launch' | 'append' | 'rewrite' | 'restart' | 'concurrent'
  bytes?: string
  projectDir?: string
  memory?: Record<string, unknown>
  count?: number
}
const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as { home: string, projectDir: string, steps: Step[] }
const home = assertDisposableFixtureHome(input.home, 'Session projection')
const canonicalHome = realpathSync(home)
const id = 'aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee'
const file = join(home, '.claude', 'projects', input.projectDir, id + '.jsonl')
const output = join(home, 'projection-result.json')
function assertFile(path: string) {
  let info
  try {
    info = lstatSync(path)
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error
    let ancestor = dirname(path)
    for (;;) {
      try {
        lstatSync(ancestor)
        const child = relative(canonicalHome, realpathSync(ancestor))
        if (child === '..' || child.startsWith('..' + sep) || isAbsolute(child)) throw new Error('Session projection fixture requires ordinary private files')
        return
      } catch (failure) {
        if ((failure as NodeJS.ErrnoException).code !== 'ENOENT' || dirname(ancestor) === ancestor) throw failure
        ancestor = dirname(ancestor)
      }
    }
  }
  const child = relative(canonicalHome, realpathSync(path))
  if (info.isSymbolicLink() || !info.isFile() || child === '..' || child.startsWith('..' + sep) || isAbsolute(child)) throw new Error('Session projection fixture requires ordinary private files')
}
assertFile(file)
assertFile(output)
const capabilities = await installClientFixtureCapabilities(home)
const { SessionService } = await import('../../../src/server/services/sessionService')
type ProjectionService = Pick<InstanceType<typeof SessionService>, 'getSessionLaunchInfo'> & {
  getMetadataProjection(file: string, project: string): Promise<unknown>
  memoryLaunchInfo: Map<string, Record<string, unknown>>
}
let service = new SessionService() as unknown as ProjectionService
const originalOpen = (fs.open as any).getMockImplementation?.() as typeof fs.open
const originalStat = (fs.stat as any).getMockImplementation?.() as typeof fs.stat
if (typeof originalOpen !== 'function' || typeof originalStat !== 'function') throw new Error('Guarded session projection implementation missing')
const trace: Array<{ operation: string, offset?: number, length?: number, bytesRead?: number }> = []
spyOn(fs, 'stat').mockImplementation((async (...args: Parameters<typeof fs.stat>) => {
  if (String(args[0]) === file) trace.push({ operation: 'pathStat' })
  return originalStat(...args)
}) as typeof fs.stat)
spyOn(fs, 'open').mockImplementation((async (...args: Parameters<typeof fs.open>) => {
  if (String(args[0]) !== file) throw new Error('Session projection fixture supports only its transcript')
  assertFile(file)
  trace.push({ operation: 'open' })
  const handle = await originalOpen(...args)
  const read = handle.read.bind(handle)
  const stat = handle.stat.bind(handle)
  const close = handle.close.bind(handle)
  handle.read = (async (buffer: Buffer, offset: number, length: number, position: number) => {
    const value = await read(buffer, offset, length, position)
    trace.push({ operation: 'read', offset: position, length, bytesRead: value.bytesRead })
    return value
  }) as typeof handle.read
  handle.stat = (async (...args: Parameters<typeof handle.stat>) => {
    trace.push({ operation: 'stat' })
    return stat(...args)
  }) as typeof handle.stat
  handle.close = async () => {
    trace.push({ operation: 'close' })
    await close()
  }
  return handle
}) as typeof fs.open)
const results: unknown[] = []
for (const [index, step] of input.steps.entries()) {
  try {
    let value: unknown
    if (step.operation === 'append' || step.operation === 'rewrite') {
      assertFile(file)
      await (step.operation === 'append' ? fs.appendFile : fs.writeFile)(file, Buffer.from(step.bytes ?? '', 'hex'))
      await fs.utimes(file, 1_700_000_010 + index, 1_700_000_010 + index)
      value = { ok: true }
    } else if (step.operation === 'restart') {
      service = new SessionService() as unknown as ProjectionService
      value = { ok: true }
    } else if (step.operation === 'launch') {
      if (step.memory) service.memoryLaunchInfo.set(join(home, '.claude') + ':' + id, step.memory)
      value = await service.getSessionLaunchInfo(id)
    } else if (step.operation === 'concurrent') {
      value = await Promise.all(Array.from({ length: step.count ?? 4 }, () => service.getMetadataProjection(file, step.projectDir ?? input.projectDir)))
    } else {
      value = await service.getMetadataProjection(file, step.projectDir ?? input.projectDir)
    }
    results.push({ bytes: Buffer.from(JSON.stringify(value)).toString('hex') })
  } catch (error) {
    const failure = error as { name?: string, code?: string, statusCode?: number, message?: string }
    results.push({ error: { name: failure.name, code: failure.code ?? 'ERROR', ...(failure.statusCode ? { status: failure.statusCode } : {}), message: failure.message } })
  }
}
assertFile(output)
if (capabilities.blocked.length) throw new Error('Session projection fixture blocked external capability: ' + capabilities.blocked.join(', '))
await fs.writeFile(output, JSON.stringify({ results, trace }))
console.log(JSON.stringify({ result: 'projection-result.json' }))
