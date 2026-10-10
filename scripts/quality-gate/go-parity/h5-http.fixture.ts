import { spyOn } from 'bun:test'
import * as fs from 'node:fs/promises'
import * as crypto from 'node:crypto'
import os from 'node:os'
import { isAbsolute, join, relative } from 'node:path'
import type { H5HttpOutput, H5HttpScenario } from './h5-http-fixtures'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as H5HttpScenario & { home: string }
if (!input.home || input.home !== process.env.HOME || process.env.CLAUDE_CONFIG_DIR !== join(input.home, '.claude')) throw new Error('H5 HTTP fixture requires disposable HOME/config')
const { handleH5AccessApi } = await import('../../../src/server/api/h5-access')
const { getDisconnectGraceMs, refreshDisconnectGraceMs } = await import('../../../src/server/ws/disconnectGraceConfig')
const { diagnosticsService } = await import('../../../src/server/services/diagnosticsService')
const { ProviderService } = await import('../../../src/server/services/providerService')
const { isH5AccessControlPath, requiresLocalAccessCredential } = await import('../../../src/server/h5AccessPolicy')
const output: H5HttpOutput = { results: [], trace: [], diagnostics: [], warningCount: 0, randomCalls: [], policy: [] }
if (input.trackWritePreparation) output.writePreparation = []
let apiStarted = false
for (const key of ['CLAUDE_H5_PUBLIC_BASE_URL', 'CLAUDE_H5_AUTO_PUBLIC_URL'] as const) {
  const value = input.environment?.[key]
  if (value === undefined || value === null) delete process.env[key]
  else process.env[key] = value
}
spyOn(os, 'networkInterfaces').mockImplementation(() => Object.fromEntries((input.interfaces ?? []).map(value => [value.name, value.entries])) as ReturnType<typeof os.networkInterfaces>)
spyOn(ProviderService, 'getServerPort').mockImplementation(() => input.activePort ?? 3456)
spyOn(Date, 'now').mockImplementation(() => { if (apiStarted) output.writePreparation?.push('clock'); return 1_700_000_000_000 })
let tokenGeneration = 0
spyOn(crypto, 'randomBytes').mockImplementation(((size: number) => {
  output.randomCalls.push(size)
  if (apiStarted) output.writePreparation?.push(`random:${size}`)
  if (input.randomErrorSize === size) throw new Error('fixture random denied')
  if (size === 32) { tokenGeneration++; return Buffer.from(Array.from({ length: size }, (_, index) => (index + tokenGeneration * 17) % 256)) }
  return Buffer.from(Array.from({ length: size }, (_, index) => index % 3 + 1))
}) as never)
console.warn = () => { output.warningCount++ }
const faultState = (input.faults ?? []).map(fault => ({ ...fault, used: false }))
let managedCommitted = false
function before(operation: string, paths: unknown[]) {
  const names = paths.map(path => {
    const name = relative(input.home, String(path)).replace(/\\/g, '/')
    if (name === '..' || name.startsWith('../') || isAbsolute(name)) throw new Error('H5 HTTP fixture escaped HOME')
    return name
  })
  output.trace.push({ operation, paths: names })
  if (apiStarted && operation === 'mkdir') output.writePreparation?.push(`mkdir:${names[0]}`)
  const fault = apiStarted && faultState.find(fault => fault.operation === operation && names[0]!.startsWith(fault.path) && (!fault.once || !fault.used) && (!fault.afterManagedCommit || managedCommitted))
  if (fault) { fault.used = true; throw new Error(fault.message) }
}
for (const [method, operation, count] of [['readFile', 'read', 1], ['writeFile', 'write', 1], ['mkdir', 'mkdir', 1], ['rename', 'rename', 2], ['copyFile', 'copy', 2], ['unlink', 'unlink', 1], ['access', 'access', 1]] as const) {
  const original = fs[method] as (...args: unknown[]) => Promise<unknown>
  spyOn(fs, method).mockImplementation((async (...args: unknown[]) => {
    before(operation, args.slice(0, count))
    const result = await original(...args)
    if (method === 'rename' && String(args[1]) === join(input.home, '.claude/cc-haha/settings.json')) managedCommitted = true
    return result
  }) as never)
}
const pendingDiagnostics: Promise<unknown>[] = []
const recordEvent = diagnosticsService.recordEvent.bind(diagnosticsService)
spyOn(diagnosticsService, 'recordEvent').mockImplementation(event => {
  output.diagnostics.push({ type: event.type, severity: event.severity, summary: event.summary })
  const pending = recordEvent(event)
  pendingDiagnostics.push(pending)
  return pending
})
if (input.initialRefresh) await refreshDisconnectGraceMs()
for (const step of input.steps) {
  apiStarted = true
  const headers = new Headers(step.headers ?? [])
  if (step.tokenFrom !== undefined) {
    const previous = output.results[step.tokenFrom]
    const token = previous ? (JSON.parse(Buffer.from(previous.body, 'hex').toString()) as { token?: unknown }).token : undefined
    if (typeof token !== 'string') throw new Error('H5 HTTP fixture tokenFrom is not a successful token result')
    headers.append('Authorization', `${step.bearerPrefix ?? 'Bearer '}${token}`)
  }
  let body: BodyInit | undefined = step.body === undefined ? undefined : Buffer.from(step.body, 'hex')
  if (step.readError) {
    const error = new Error(step.readError.message)
    error.name = step.readError.name
    body = new ReadableStream({ start(controller) { controller.error(error) } })
  }
  const req = new Request(`http://127.0.0.1:3456${step.path}`, { method: step.method, headers, body })
  const url = new URL(req.url)
  const segments = step.segments ?? url.pathname.split('/').filter(Boolean)
  output.policy.push({ pathname: url.pathname, resource: url.pathname.split('/').filter(Boolean)[1], control: isH5AccessControlPath(url.pathname), requiresLocalCredential: requiresLocalAccessCredential(url.pathname, { clientAddress: '127.0.0.1', localAccessTokenConfigured: true, localAccessAuthorized: false }) })
  const response = await handleH5AccessApi(req, url, segments)
  output.results.push({ status: response.status, headers: Object.fromEntries(response.headers), body: Buffer.from(await response.arrayBuffer()).toString('hex'), graceMs: getDisconnectGraceMs() })
}
await Promise.all(pendingDiagnostics)
console.log(JSON.stringify(output))
