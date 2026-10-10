import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as {
  home: string
  environment?: string
  delays: Array<{ attempt: number | string; header: string | null; maximum: number | string; random: number | string }>
}
assertDisposableFixtureHome(input.home, 'Retry delay')
if (process.env.CC_HAHA_RETRY_DELAY_FIXTURE !== '1' || !Array.isArray(input.delays)) throw new Error('Retry delay fixture requires its explicit offline profile')
const capabilities = await installClientFixtureCapabilities(input.home, { absentPaths: absentManagedSettingsFixturePaths() })
;(globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }).MACRO = { BUILD_TIME: '' }
const { getRetryDelay, getDefaultMaxRetries, getMaxStreamTransientRetries } = await import('../../../src/services/api/withRetry')
function number(value: number | string) {
  return typeof value === 'number' ? value : value === 'NaN' ? NaN : value === 'Infinity' ? Infinity : value === '-Infinity' ? -Infinity : value === '-0' ? -0 : Number(value)
}
function observed(value: number) {
  return { value: Number.isFinite(value) ? value : String(value), negativeZero: Object.is(value, -0) }
}
if (input.environment === undefined) {
  delete process.env.CLAUDE_CODE_MAX_RETRIES
  delete process.env.CLAUDE_STREAM_TRANSIENT_RETRY_MAX
} else {
  process.env.CLAUDE_CODE_MAX_RETRIES = input.environment
  process.env.CLAUDE_STREAM_TRANSIENT_RETRY_MAX = input.environment
}
let randomCalls = 0
const outputs = input.delays.map(step => {
  const previous = randomCalls
  const random = spyOn(Math, 'random').mockImplementation(() => { randomCalls++; return number(step.random) })
  try {
    return { result: observed(getRetryDelay(number(step.attempt), step.header, number(step.maximum))), randomCalls: randomCalls - previous }
  } finally { random.mockRestore() }
})
process.stdout.write(JSON.stringify({ outputs, maximum: observed(getDefaultMaxRetries()), streamMaximum: observed(getMaxStreamTransientRetries()), capabilities: { blocked: capabilities.blocked, secureReads: capabilities.secureReads } }))
