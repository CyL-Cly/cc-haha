import { spyOn } from 'bun:test'
import * as crypto from 'node:crypto'
import fs from 'node:fs'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from './client-options-capabilities'
import type { TerminalCase, TerminalError } from './terminal-creation-fixtures'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as { home: string; env: Record<string, string>; cases: TerminalCase[]; probe?: string; probePath?: string }
const home = assertDisposableFixtureHome(input.home, 'terminal creation')
if (Bun.version !== '1.3.14' || Bun.revision !== '0d9b296af33f2b851fcbf4df3e9ec89751734ba4' || process.env.NODE_ENV !== 'production' || process.env.CC_HAHA_TERMINAL_CREATION_FIXTURE !== '1' || !Array.isArray(input.cases) || Object.keys(input).some(key => !['home', 'env', 'cases', 'probe', 'probePath'].includes(key))) throw new Error('Terminal creation requires explicit original private profile')
const allowed = new Set(['ANTHROPIC_BASE_URL', 'ANTHROPIC_API_KEY', 'API_TIMEOUT_MS', 'CLAUDE_CODE_EXTRA_METADATA', 'CLAUDE_CODE_MAX_RETRIES', 'CLAUDE_CODE_DISABLE_FAST_MODE', 'NODE_EXTRA_CA_CERTS', 'NODE_OPTIONS'])
if (!input.env || Object.entries(input.env).some(([name, value]) => !allowed.has(name) || typeof value !== 'string') || input.env.ANTHROPIC_BASE_URL !== 'https://127.0.0.1:1' || !input.env.ANTHROPIC_API_KEY || input.env.CLAUDE_CODE_DISABLE_FAST_MODE !== '1' || input.env.NODE_EXTRA_CA_CERTS || input.env.NODE_OPTIONS) throw new Error('Terminal creation rejects unported original environment before imports')
const validate = (error: TerminalError, depth = 0): void => {
  if (!error || depth > 16 || !['connection', 'timeout', 'error', 'plain', 'abort', 'api'].includes(error.kind) || Object.keys(error).some(key => !['kind', 'message', 'code', 'cause', 'selfCause', 'status'].includes(key)) || error.message !== undefined && typeof error.message !== 'string') throw new Error('Terminal creation requires neutral original Error fields')
  if (!['error', 'plain'].includes(error.kind) && ('code' in error || error.selfCause || error.kind !== 'api' && error.status !== undefined)) throw new Error('Terminal creation rejects unported original Error fields')
  if (error.cause) validate(error.cause, depth + 1)
}
for (const item of input.cases) {
  if (!item || typeof item.name !== 'string' || Object.keys(item).some(key => !['name', 'error', 'factoryOptions', 'priorUUIDs', 'wrapRetry', 'supported'].includes(key)) || !Number.isInteger(item.priorUUIDs ?? 0) || (item.priorUUIDs ?? 0) < 0 || (item.priorUUIDs ?? 0) > 8 || !item.error && !item.factoryOptions || item.error && item.factoryOptions) throw new Error('Terminal creation rejects prepared/unknown cases')
  if (item.error) validate(item.error)
}
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
if (input.probe === 'native') Bun.spawn(['security'])
if (input.probe === 'network') await fetch('http://127.0.0.1:1')
if (input.probe === 'filesystem') fs.readFileSync(input.probePath!)
for (const [name, value] of Object.entries(input.env)) process.env[name] = value
;(globalThis as any).MACRO = { BUILD_TIME: '' }
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(home)
bootstrap.setOriginalCwd(home)
bootstrap.setProjectRoot(home)
bootstrap.setIsInteractive(false)
const config = await import('../../../src/utils/config')
config.enableConfigs()
config.saveGlobalConfig(previous => ({ ...previous, userID: 'terminal-fixture-device' }))
const sdk = await import('@anthropic-ai/sdk')
const { CannotRetryError } = await import('../../../src/services/api/withRetry')
const { getAssistantMessageFromError } = await import('../../../src/services/api/errors')
const { createAssistantAPIErrorMessage } = await import('../../../src/utils/messages')
const original = (spec: TerminalError): any => {
  const message = spec.message
  let error: any
  if (spec.kind === 'connection') error = new sdk.APIConnectionError({ message, cause: spec.cause ? original(spec.cause) : undefined })
  else if (spec.kind === 'timeout') error = new sdk.APIConnectionTimeoutError({ message })
  else if (spec.kind === 'abort') error = new sdk.APIUserAbortError({ message })
  else if (spec.kind === 'api') error = new sdk.APIError(spec.status, undefined, message, undefined)
  else error = spec.kind === 'plain' ? { message: message ?? 'Original cause' } : new Error(message ?? 'Original cause')
  if ('code' in spec) error.code = spec.code
  if (spec.selfCause) error.cause = error
  else if (spec.cause && spec.kind !== 'connection') error.cause = original(spec.cause)
  return error
}
let calls = 0
let allocations: string[] = []
spyOn(crypto, 'randomUUID').mockImplementation(() => { calls++; allocations.push('uuid:' + calls); return `terminal-id-${calls}` as ReturnType<typeof crypto.randomUUID> })
const RealDate = Date
globalThis.Date = class extends RealDate {
  constructor(...args: unknown[]) { if (!args.length) allocations.push('date'); super(...(args.length ? args : [1700000000000]) as [number]) }
  static now() { return 1700000000000 }
} as DateConstructor
const rows = input.cases.map(item => {
  calls = item.priorUUIDs ?? 0
  allocations = []
  let error = item.error ? original(item.error) : undefined
  if (item.wrapRetry) error = new CannotRetryError(error, { model: 'deepseek-flash', thinkingConfig: { type: 'disabled' } })
  // This standalone exported oracle receives the same original typed value
  // selected by claude.ts:3354. Physical caller/abort/telemetry is a later layer.
  const selected = error instanceof CannotRetryError ? error.originalError : error
  const result = item.factoryOptions ? createAssistantAPIErrorMessage(item.factoryOptions as any) : getAssistantMessageFromError(selected, 'deepseek-flash')
  return { name: item.name, result: JSON.stringify(result), allocations: [...allocations], uuidCalls: calls }
})
process.stdout.write(JSON.stringify({ rows, sourceOnly: { capabilities, bun: Bun.version, revision: Bun.revision, sdk: '0.80.0', boundary: 'actual exported mapper/shared factory; complete query caller remains separate' } }))
