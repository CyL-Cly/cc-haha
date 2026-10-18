import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as { home: string; failures: Failure[] }
assertDisposableFixtureHome(input.home, 'Retry stream')
if (process.env.CC_HAHA_RETRY_DELAY_FIXTURE !== '1' || !Array.isArray(input.failures)) throw new Error('Retry stream fixture requires its explicit offline profile')
const capabilities = await installClientFixtureCapabilities(input.home, { absentPaths: absentManagedSettingsFixturePaths() })
;(globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }).MACRO = { BUILD_TIME: '' }
const sdk = await import('@anthropic-ai/sdk')
const { getOpenAIPolicyError } = await import('../../../src/services/openaiAuth/policyError')
const { extractConnectionErrorDetails } = await import('../../../src/services/api/errorUtils')
const { isRetryableStreamError, isRetryableStreamTransportError, getStreamRetryKind } = await import('../../../src/services/api/withRetry')

type Failure = { kind: 'api' | 'syntax' | 'native' | 'nil'; sdkClass?: string; message?: string; code?: string; payload?: unknown; cause?: Failure; selfCause?: boolean }
function sdkFault(spec: Failure): Error {
  const name = spec.sdkClass ?? 'APIError'
  if (name === 'APIUserAbortError') return new sdk.APIUserAbortError()
  if (name === 'APIConnectionError') return new sdk.APIConnectionError({})
  if (name === 'APIConnectionTimeoutError') return new sdk.APIConnectionTimeoutError()
  if (name === 'AnthropicError') return new sdk.AnthropicError()
  if (name === 'Error') return new Error()
  const Type = (sdk as Record<string, unknown>)[name]
  if (typeof Type !== 'function' || !(name === 'APIError' || Type.prototype instanceof sdk.APIError)) throw new Error('Unsupported SDK fault class profile')
  return new (Type as typeof sdk.APIError)(undefined, spec.payload, undefined, undefined)
}
function fault(spec: Failure): Error | null {
  if (spec.kind === 'nil') return null
  const failure: Error & { code?: string; cause?: unknown } = spec.kind === 'api'
    ? sdkFault(spec)
    : spec.kind === 'syntax' ? new SyntaxError() : new Error()
  // These are injected upstream fault objects. Production classifiers and SDK
  // instanceof semantics remain real; no request/resubmission is simulated.
  failure.message = spec.message ?? ''
  if (spec.code !== undefined) failure.code = spec.code
  if (spec.cause) failure.cause = fault(spec.cause)
  if (spec.selfCause) failure.cause = failure
  return failure
}
const outcomes = input.failures.map(spec => {
  const error = fault(spec)
  // Exercise the current Source decision at the original transport-only caller
  // boundary: it has no non-streaming fallback or transient retry allowance.
  const states = Array.from({ length: 8 }, (_, flags) => getStreamRetryKind({ error,
    canReplay: !Boolean(flags & 1), streamIdleAborted: Boolean(flags & 2), signalAborted: Boolean(flags & 4),
    nonStreamingFallbackAvailable: false, transientRetryAllowed: false }) === 'transport')
  return { policy: getOpenAIPolicyError(error) !== null, api: isRetryableStreamError(error), transport: isRetryableStreamTransportError(error), code: extractConnectionErrorDetails(error)?.code ?? null, states }
})
process.stdout.write(JSON.stringify({ outcomes, capabilities: { blocked: capabilities.blocked, secureReads: capabilities.secureReads } }))
