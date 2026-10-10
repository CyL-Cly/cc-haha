import * as crypto from 'node:crypto'
import { spyOn } from 'bun:test'
import type { AttemptCase, AttemptFault } from './retry-attempt-fixtures'
import { attemptEnvironmentNames } from './retry-attempt-fixtures'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as AttemptCase & { home: string }
assertDisposableFixtureHome(input.home, 'Retry attempt')
if (process.env.CC_HAHA_RETRY_ATTEMPT_FIXTURE !== '1' || process.env.CLAUDE_CODE_SIMPLE !== '1') throw new Error('Retry attempt fixture requires its explicit offline profile')
for (const [name, value] of Object.entries(input.env ?? {})) if (!attemptEnvironmentNames.has(name) || typeof value !== 'string' || name === 'USER_TYPE' && value !== '' && value !== 'external') throw new Error('Retry attempt fixture rejected unsupported environment')
const capabilities = await installClientFixtureCapabilities(input.home, { absentPaths: absentManagedSettingsFixturePaths() })
for (const [name, value] of Object.entries(input.env ?? {})) process.env[name] = value
;(globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }).MACRO = { BUILD_TIME: '' }
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(input.home)
bootstrap.setOriginalCwd(input.home)
bootstrap.setProjectRoot(input.home)
const config = await import('../../../src/utils/config')
config.enableConfigs()
config.saveGlobalConfig(previous => ({ ...previous, userID: 'fixture-device', penguinModeOrgEnabled: true }))
const sdk = await import('@anthropic-ai/sdk')
const auth = await import('../../../src/utils/auth')
const fast = await import('../../../src/utils/fastMode')
const proxy = await import('../../../src/utils/proxy')
const settings = await import('../../../src/utils/settings/settings')
const retry = await import('../../../src/services/api/withRetry')
const trace: unknown[] = []
Math.random = () => input.random ?? 0.25
const clearHelper = auth.clearApiKeyHelperCache
spyOn(auth, 'clearApiKeyHelperCache').mockImplementation(() => { trace.push({ kind: 'invalidate' }); clearHelper() })
let count = 0
spyOn(crypto, 'randomUUID').mockImplementation(() => `retry-attempt-id-${++count}` as ReturnType<typeof crypto.randomUUID>)
const RealDate = Date
globalThis.Date = class extends RealDate { constructor(...args: unknown[]) { super(...(args.length ? args : [1700000000000]) as [number]) } static now() { return 1700000000000 } } as DateConstructor
fast.clearFastModeCooldown()
proxy._resetKeepAliveForTesting()
if (input.cooldown !== undefined) fast.triggerFastModeCooldown(input.cooldown, 'rate_limit')
fast.onCooldownTriggered((resetAt, reason) => trace.push({ kind: 'cooldown', resetAt, reason }))
fast.onCooldownExpired(() => trace.push({ kind: 'cooldown_expired' }))
fast.onFastModeOverageRejection(message => trace.push({ kind: 'overage', message }))
fast.onOrgFastModeChanged(enabled => trace.push({ kind: 'org_disabled', enabled }))
const controller = new AbortController()
if (input.abort === 'before') controller.abort()
const originalTimeout = globalThis.setTimeout
globalThis.setTimeout = ((callback: (...args: unknown[]) => void, ms: number, ...args: unknown[]) => {
  // Only the real sleep() timer uses (signal, onAbort, resolve). Other Source
  // timers retain their original host boundary; this is a clock, not a retry.
  if (args.length !== 3 || typeof args[2] !== 'function') return originalTimeout(callback, ms, ...args)
  trace.push({ kind: 'sleep', milliseconds: ms })
  queueMicrotask(() => input.abort === 'sleep' ? controller.abort() : callback(...args))
  return 0
}) as typeof setTimeout
function fault(spec: AttemptFault): Error {
  let error: Error & { code?: string; cause?: unknown }
  if (spec.kind === 'native') error = new Error(spec.message ?? '')
  else if (spec.kind === 'syntax') error = new SyntaxError(spec.message ?? '')
  else if (spec.sdkClass === 'APIConnectionError') error = new sdk.APIConnectionError({ cause: spec.cause ? fault(spec.cause) : undefined })
  else if (spec.sdkClass === 'APIConnectionTimeoutError') error = new sdk.APIConnectionTimeoutError()
  else if (spec.sdkClass === 'APIUserAbortError') error = new sdk.APIUserAbortError()
  else if (spec.sdkClass && spec.sdkClass !== 'APIError') throw new Error('Undeclared SDK class profile')
  else error = new sdk.APIError(spec.status, spec.payload, spec.message, spec.headers ? new Headers(spec.headers) : undefined)
  if (spec.message !== undefined) error.message = spec.message
  if (spec.code !== undefined) error.code = spec.code
  if (spec.cause && spec.sdkClass !== 'APIConnectionError') error.cause = fault(spec.cause)
  return error
}
function observedError(error: unknown): unknown {
  if (!(error instanceof Error)) return null
  const api = error instanceof sdk.APIError
  return { class: error.constructor.name, name: error.name, message: error.message, status: api ? error.status ?? null : null,
    payload: api ? error.error ?? null : null, headers: api ? [...(error.headers ?? [])] : [],
    cause: observedError(error.cause), code: (error as { code?: unknown }).code ?? null }
}
let clientCount = 0, operationCount = 0, factoryCount = 0
const generator = retry.withRetry(async () => {
  trace.push({ kind: 'client', index: ++clientCount, keepAliveDisabled: proxy.getProxyFetchOptions().keepalive === false })
  const failure = input.factoryFailures?.[factoryCount++]
  if (failure) throw fault(failure)
  return { client: clientCount } as never
}, async (client, attempt, context) => {
  trace.push({ kind: 'operation', client: (client as unknown as { client: number }).client, attempt, context: JSON.parse(JSON.stringify(context)) })
  if (input.abort === 'operation') controller.abort()
  const failure = input.failures?.[operationCount++]
  if (failure) throw fault(failure)
  return 'success'
}, { model: 'deepseek-flash', thinkingConfig: { type: 'disabled' }, maxRetries: 4, ...input.options,
  ...(input.options?.maxRetries !== undefined ? { maxRetries: Number(input.options.maxRetries) } : input.env?.CLAUDE_CODE_MAX_RETRIES !== undefined ? { maxRetries: undefined } : {}), signal: controller.signal } as never)
let result: unknown
try {
  for (;;) {
    const step = await generator.next()
    if (step.done) { result = { kind: 'success', value: step.value }; break }
    const notice = step.value
    trace.push({ kind: 'yield', notice: { ...notice, error: observedError(notice.error), cause: observedError(notice.cause) }, wire: JSON.stringify(notice) })
    if (input.abort === 'yield') controller.abort()
    if (input.stopAfterYield) { await generator.return('consumer-stopped' as never); result = { kind: 'consumer-stopped' }; break }
  }
} catch (error) {
  result = error instanceof retry.CannotRetryError ? { kind: 'cannot-retry', name: error.name, message: error.message, context: error.retryContext, original: observedError(error.originalError) }
    : error instanceof retry.FallbackTriggeredError ? { kind: 'fallback', name: error.name, message: error.message, originalModel: error.originalModel, fallbackModel: error.fallbackModel }
    : { kind: 'raw-error', error: observedError(error) }
} finally { globalThis.setTimeout = originalTimeout }
process.stdout.write(JSON.stringify({ observation: { trace, result, state: { fast: fast.getFastModeRuntimeState(), settingsFastMode: settings.getSettingsForSource('userSettings')?.fastMode ?? null,
  penguinModeOrgEnabled: config.getGlobalConfig().penguinModeOrgEnabled ?? null, keepAliveDisabled: proxy.getProxyFetchOptions().keepalive === false }, uuidCalls: count }, sourceOnly: { capabilities } }))
