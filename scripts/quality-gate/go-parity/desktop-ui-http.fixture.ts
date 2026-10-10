import { spyOn } from 'bun:test'
import * as fs from 'node:fs/promises'
import * as crypto from 'node:crypto'
import { isAbsolute, join, relative } from 'node:path'

type Step = { method: string; path: string; segments?: string[]; headers?: Array<[string, string]>; body?: string; readError?: { name: string; message: string } }
type Fault = { operation: string; path: string; message: string; once?: boolean; used?: boolean }
const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as { home: string; petToken?: string; steps: Step[]; faults?: Fault[] }
if (!input.home || input.home !== process.env.HOME || process.env.CLAUDE_CONFIG_DIR !== join(input.home, '.claude')) {
  throw new Error('Desktop UI HTTP fixture requires disposable HOME/config')
}
process.env.CC_HAHA_PET_ACCESS_TOKEN = input.petToken ?? ''
const { handleDesktopUiApi } = await import('../../../src/server/api/desktop-ui')
const { diagnosticsService } = await import('../../../src/server/services/diagnosticsService')
const trace: Array<{ operation: string; paths: string[] }> = []
function before(operation: string, paths: string[]) {
  const names = paths.map(path => {
    const name = relative(input.home, path).replace(/\\/g, '/')
    if (name === '..' || name.startsWith('../') || isAbsolute(name)) throw new Error('Desktop UI HTTP fixture escaped HOME')
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
const diagnostics: Array<{ type: string; severity?: string; summary: string }> = []
const pendingDiagnostics: Promise<unknown>[] = []
const recordEvent = diagnosticsService.recordEvent.bind(diagnosticsService)
spyOn(diagnosticsService, 'recordEvent').mockImplementation(event => {
  diagnostics.push({ type: event.type, severity: event.severity, summary: event.summary })
  const operation = recordEvent(event)
  pendingDiagnostics.push(operation)
  return operation
})
let warningCount = 0
console.warn = () => { warningCount += 1 }
const results = []
for (const step of input.steps) {
  const headers = new Headers(step.headers ?? [])
  let body: BodyInit | undefined = step.body === undefined ? undefined : Buffer.from(step.body, 'hex')
  if (step.readError) {
    const failure = new Error(step.readError.message)
    failure.name = step.readError.name
    body = new ReadableStream({ start(controller) { controller.error(failure) } })
  }
  const req = new Request(`http://fixture.invalid${step.path}`, { method: step.method, headers, body })
  const url = new URL(req.url)
  const response = await handleDesktopUiApi(req, url, step.segments ?? url.pathname.split('/').filter(Boolean))
  results.push({ status: response.status, headers: Object.fromEntries(response.headers), body: Buffer.from(await response.arrayBuffer()).toString('hex') })
}
await Promise.all(pendingDiagnostics)
console.log(JSON.stringify({ results, trace, warningCount, diagnostics }))
