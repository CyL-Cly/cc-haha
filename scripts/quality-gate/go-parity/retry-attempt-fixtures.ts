export type AttemptFault = { kind?: 'api' | 'native' | 'syntax'; sdkClass?: string; status?: number; message?: string; payload?: unknown; headers?: Record<string, string>; code?: string; cause?: AttemptFault }
export type AttemptCase = {
  name: string
  options?: { model?: string; thinkingConfig?: unknown; fastMode?: boolean; maxRetries?: number | string; fallbackModel?: string; querySource?: string; initialConsecutive529Errors?: number }
  env?: Record<string, string>
  failures?: AttemptFault[]
  factoryFailures?: Array<AttemptFault | null>
  abort?: 'before' | 'operation' | 'yield' | 'sleep'
  stopAfterYield?: boolean
  random?: number
  cooldown?: number
}
const api = (status: number, message = 'upstream', headers?: Record<string, string>): AttemptFault => ({ status, message, headers })
const overflow = (input: number, max: number, limit: number) => api(400, `input length and \`max_tokens\` exceed context limit: ${input} + ${max} > ${limit}`)
export const attemptEnvironmentNames = new Set(['CLAUDE_CODE_MAX_RETRIES', 'CLAUDE_CODE_DISABLE_FAST_MODE', 'FALLBACK_FOR_ALL_PRIMARY_MODELS', 'USER_TYPE', 'IS_SANDBOX', 'CLAUDE_CODE_REMOTE'])
export const retryAttemptCases: AttemptCase[] = [
  { name: 'success without retry' },
  { name: 'retry notice retains original SDK payload and request ID', failures: [{ status: 500, message: 'diagnostic', headers: { 'request-id': '<original-request>' }, payload: { error: { message: '原始字段 <>&', count: 9007199254740993, value: null } } }] },
  ...[400, 403, 404, 408, 409, 422, 429, 500, 529].map(status => ({ name: `HTTP ${status}`, failures: [api(status)] })),
  { name: '401 invalidates helper then rebuilds client', failures: [api(401)] },
  { name: '403 revoked rebuilds client', failures: [api(403, 'OAuth token has been revoked')] },
  { name: '401 wrapped context overflow is final', failures: [api(401, 'K3 supports only 256K context')] },
  { name: 'non-API native error is final', failures: [{ kind: 'native', message: 'transport text' }] },
  { name: 'syntax error is final', failures: [{ kind: 'syntax', message: 'unexpected input' }] },
  { name: 'connection error rebuild and sticky keepalive', failures: [{ sdkClass: 'APIConnectionError', cause: { kind: 'native', code: 'ECONNRESET', message: 'peer reset' } }] },
  { name: 'connection EPIPE disables keepalive', failures: [{ sdkClass: 'APIConnectionError', cause: { kind: 'native', code: 'EPIPE' } }] },
  { name: 'connection timeout reuses client', failures: [{ sdkClass: 'APIConnectionTimeoutError' }] },
  { name: 'factory rejection enters same retry loop', factoryFailures: [api(500)] },
  { name: 'failed authentication client rebuild retains previous client', factoryFailures: [null, api(500)], failures: [api(401)] },
  { name: '401 unicode whitespace overflow prevents cache invalidation', failures: [api(401, 'K3 SUPPORTS ONLY 256\u00a0K\ufefftokens of\u2002context')] },
  { name: '401 Kelvin sign is not ASCII K in Source regex', failures: [api(401, 'K3 supports only 256\u212a context')] },
  { name: 'API subclass Abort thrown by operation wraps final error', failures: [{ sdkClass: 'APIUserAbortError' }] },
  { name: 'explicit false overrides server error', failures: [api(500, 'upstream', { 'x-should-retry': 'false' })] },
  { name: 'explicit true permits invalid-request retry', failures: [api(400, 'upstream', { 'x-should-retry': 'true' })] },
  { name: 'overload body precedes explicit false', failures: [api(400, '{"type":"overloaded_error"}', { 'x-should-retry': 'false' })] },
  { name: 'unrecognized query source drops 529', options: { querySource: 'title' }, failures: [api(529)] },
  { name: 'untyped query source conservatively retries 529', options: { querySource: undefined }, failures: [api(529)] },
  { name: 'pre-seeded 529 counter triggers fallback even at retry limit', options: { maxRetries: 0, fallbackModel: 'fallback', initialConsecutive529Errors: 2 }, env: { FALLBACK_FOR_ALL_PRIMARY_MODELS: '0' }, failures: [api(529)] },
  { name: 'Opus primary third overload triggers fallback', options: { model: 'claude-opus-4-7', fallbackModel: 'fallback' }, failures: [api(529), api(500), api(529), api(529)] },
  { name: 'canonical Opus external repeated overload', options: { model: 'claude-opus-5-5' }, env: { USER_TYPE: 'external' }, failures: [api(529), api(529), api(529)] },
  { name: 'sandbox bypasses repeated-overload custom text', options: { model: 'claude-opus-5-5' }, env: { USER_TYPE: 'external', IS_SANDBOX: '0' }, failures: [api(529), api(529), api(529)] },
  { name: 'alias does not impersonate canonical Opus', options: { model: 'opus' }, failures: [api(529), api(529), api(529)] },
  { name: 'policy rejection precedes 401 refresh and fast mode', options: { fastMode: true }, failures: [{ status: 401, message: '{"error":{"code":"content_policy"}}' }] },
  { name: 'policy rejection precedes capacity handling', options: { fastMode: true }, failures: [{ status: 429, message: 'upstream', payload: { error: { code: 'cyber_policy' } } }] },
  { name: 'legacy overflow adjusts budget without delay', failures: [overflow(15000, 10000, 24000)] },
  { name: 'legacy overflow cannot reduce again', failures: [overflow(15000, 10000, 24000), overflow(14000, 10000, 24000)] },
  { name: 'legacy overflow below floor exposes original SDK failure', failures: [overflow(20500, 10000, 24000)] },
  { name: 'legacy malformed overflow remains nonretryable', failures: [api(400, 'input length and `max_tokens` exceed context limit: nope')] },
  { name: 'zero retry limit excludes backoff', options: { maxRetries: 0 }, failures: [api(500)] },
  { name: 'exhausted counter preserves final context', options: { maxRetries: 1 }, failures: [api(500), api(500)] },
  ...['0', '-7', 'not-a-number', '2tail', '9'.repeat(400)].map(value => ({ name: `environment retry limit ${value.slice(0, 24)}`, env: { CLAUDE_CODE_MAX_RETRIES: value }, failures: [api(500)] })),
  { name: 'fractional explicit limit preserves JS loop boundary', options: { maxRetries: 0.5 }, failures: [api(500)] },
  ...['0', '-25', '2.5', 'nonsense', '3600'].map(value => ({ name: `ordinary retry-after ${value}`, failures: [api(429, 'upstream', { 'retry-after': value })] })),
  ...['0', '19', '20', 'nonsense'].map(value => ({ name: `fast retry-after ${value}`, options: { fastMode: true, maxRetries: 1 }, failures: [api(529, 'upstream', { 'retry-after': value })] })),
  { name: 'fast short retries can terminate with last raw error', options: { fastMode: true, maxRetries: 0 }, failures: [api(529, 'upstream', { 'retry-after': '0' })] },
  { name: 'fast disabled omits context fastMode', options: { fastMode: true }, env: { CLAUDE_CODE_DISABLE_FAST_MODE: '\ufeffYES' }, failures: [api(529)] },
  { name: 'active cooldown bypasses fast fallback', options: { fastMode: true }, cooldown: 1700000300000, failures: [api(529)] },
  { name: 'expired cooldown notifies before first active-fast attempt', options: { fastMode: true }, cooldown: 1699999999000 },
  { name: 'expired cooldown stays lazy until final state read', options: { fastMode: false }, cooldown: 1699999999000 },
  { name: 'disabled fast expires cooldown without notification', env: { CLAUDE_CODE_DISABLE_FAST_MODE: '1' }, cooldown: 1699999999000 },
  ...['out_of_credits', 'org_level_disabled_until', 'org_level_disabled', 'org_service_level_disabled', 'member_level_disabled', 'seat_tier_level_disabled', 'seat_tier_zero_credit_limit', 'member_zero_credit_limit', 'overage_not_provisioned', 'no_limits_configured', '', 'unknown'].map(reason => ({ name: `overage ${reason}`, options: { fastMode: true }, failures: [api(429, 'upstream', { 'anthropic-ratelimit-unified-overage-disabled-reason': reason })] })),
  { name: 'fast org rejection persists and retries', options: { fastMode: true }, failures: [api(400, 'Fast mode is not enabled for your organization')] },
  { name: 'remote 401 bypasses false header and overflow guard', env: { CLAUDE_CODE_REMOTE: 'true' }, failures: [api(401, 'prompt is too long', { 'x-should-retry': 'false' })] },
  ...['before', 'operation', 'yield', 'sleep'].map(abort => ({ name: `caller abort ${abort}`, abort: abort as AttemptCase['abort'], failures: [api(500)] })),
  { name: 'consumer stops before backoff or next attempt', failures: [api(500)], stopAfterYield: true },
]
