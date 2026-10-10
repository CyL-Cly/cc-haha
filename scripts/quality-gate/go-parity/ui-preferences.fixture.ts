import { spyOn } from 'bun:test'
import * as fs from 'node:fs/promises'
import * as crypto from 'node:crypto'
import { join, relative, isAbsolute } from 'node:path'

type Step = { operation: string; patch?: unknown; bytes?: number[]; contentType?: string }
type Fault = { operation: string; path: string; message: string; once?: boolean; used?: boolean }
type Request = { home: string; steps: Step[]; faults?: Fault[] }
const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as Request
if (!input.home || input.home !== process.env.HOME || process.env.CLAUDE_CONFIG_DIR !== join(input.home, '.claude')) {
  throw new Error('UI fixture requires disposable HOME/config')
}
// Production imports follow the environment guard; no managed-settings or
// upgrader mock can turn incomplete business into a passing service result.
const { DesktopUiPreferencesService } = await import('../../../src/server/services/desktopUiPreferencesService')
const { ApiError } = await import('../../../src/server/middleware/errorHandler')
const trace: Array<{ operation: string; paths: string[] }> = []
function before(operation: string, paths: string[]) {
  const names = paths.map(path => {
    const name = relative(input.home, path).replace(/\\/g, '/')
    if (name === '..' || name.startsWith('../') || isAbsolute(name)) throw new Error('UI fixture escaped disposable HOME')
    return name
  })
  trace.push({ operation, paths: names })
  const fault = input.faults?.find(fault => fault.operation === operation && names[0]!.startsWith(fault.path) && (!fault.once || !fault.used))
  if (fault) { fault.used = true; throw new Error(fault.message) }
}
for (const [method, operation, count] of [
  ['readFile', 'read', 1], ['writeFile', 'write', 1], ['mkdir', 'mkdir', 1],
  ['rename', 'rename', 2], ['copyFile', 'copy', 2], ['unlink', 'unlink', 1], ['access', 'access', 1],
] as const) {
  const original = fs[method] as (...args: unknown[]) => Promise<unknown>
  spyOn(fs, method).mockImplementation((async (...args: unknown[]) => {
    before(operation, args.slice(0, count).map(String))
    return original(...args)
  }) as never)
}
const OriginalDate = globalThis.Date
globalThis.Date = class extends OriginalDate {
  constructor(value?: string | number) { super(value === undefined ? 1_700_000_000_000 : value) }
  static now() { return 1_700_000_000_000 }
} as DateConstructor
spyOn(crypto, 'randomBytes').mockImplementation(((size: number) => Buffer.from(Array.from({ length: size }, (_, index) => index % 3 + 1))) as never)
const warnings: string[] = []
console.warn = (...args: unknown[]) => { warnings.push(args.map(String).join(' ')) }
const service = new DesktopUiPreferencesService()
const results: unknown[] = []
for (const step of input.steps) {
  try {
    let value: unknown = null
    switch (step.operation) {
      case 'read': value = await service.readPreferences(); break
      case 'sidebar': value = await service.updateSidebarPreferences(step.patch); break
      case 'profile': value = await service.updateProfilePreferences(step.patch); break
      case 'project-name': value = await service.updateProjectDisplayName(step.patch); break
      case 'pet': value = await service.updatePetPreferences(step.patch); break
      case 'voice': value = await service.updateVoiceInputPreferences(step.patch); break
      case 'upload-avatar': value = await service.updateProfileAvatar(Uint8Array.from(step.bytes ?? []), step.contentType ?? null); break
      case 'clear-avatar': value = await service.clearProfileAvatar(); break
      case 'read-avatar': {
        const avatar = await service.readProfileAvatar()
        value = avatar ? { bytes: Buffer.from(avatar.bytes).toString('hex'), contentType: avatar.contentType } : null
        break
      }
      default: throw new Error(`Unknown UI fixture operation ${step.operation}`)
    }
    results.push({ value })
  } catch (error) {
    results.push({ error: {
      status: error instanceof ApiError ? error.statusCode : 500,
      code: error instanceof ApiError ? error.code : 'INTERNAL_ERROR',
      message: (error instanceof Error ? error.message : String(error)).replaceAll(input.home, '<HOME>'),
    } })
  }
}
console.log(JSON.stringify({ results, trace, warningCount: warnings.length }))
