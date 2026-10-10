import { spyOn } from 'bun:test'
import { join } from 'node:path'
import type { QueryPolicyNumber, QueryPolicyRequest, QueryPolicyTracker } from './query-policy-fixtures'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as QueryPolicyRequest
if (!input.homeDir || input.homeDir !== process.env.HOME || process.env.CLAUDE_CONFIG_DIR !== join(input.homeDir, '.claude')) {
  throw new Error('Query policy fixture requires explicit disposable HOME/config')
}
const { createBudgetTracker, checkTokenBudget } = await import('../../../src/query/tokenBudget')
const { getBudgetContinuationMessage } = await import('../../../src/utils/tokenBudget')

function numeric(value: QueryPolicyNumber): number {
  if (typeof value === 'number') return value
  if (value.source === 'NaN') return NaN
  if (value.source === 'Infinity') return Infinity
  if (value.source === '-Infinity') return -Infinity
  return JSON.parse(value.source) as number
}
function trackerFrom(value: QueryPolicyTracker) {
  return { continuationCount: numeric(value.continuationCount), lastDeltaTokens: numeric(value.lastDeltaTokens), lastGlobalTurnTokens: numeric(value.lastGlobalTurnTokens), startedAt: numeric(value.startedAt) }
}
// JSON.stringify alone would erase NaN/Infinity and the sign of zero. Observe
// those internal values explicitly without changing the production function.
function project(value: unknown): unknown {
  if (typeof value === 'number') {
    if (!Number.isFinite(value)) return { number: String(value) }
    return { number: value === 0 ? 0 : value, negativeZero: Object.is(value, -0) }
  }
  if (Array.isArray(value)) return value.map(project)
  if (value !== null && typeof value === 'object') return Object.fromEntries(Object.entries(value).map(([key, entry]) => [key, project(entry)]))
  return value
}
const clock = spyOn(Date, 'now').mockReturnValue(numeric(input.startedAt))
let tracker = createBudgetTracker()
const results: unknown[] = [{ tracker: project(tracker) }]
for (const step of input.steps) {
  switch (step.operation) {
    case 'reset': clock.mockReturnValue(numeric(step.now)); tracker = createBudgetTracker(); results.push({ tracker: project(tracker) }); break
    case 'restore': tracker = trackerFrom(step.tracker); results.push({ tracker: project(tracker) }); break
    case 'check': {
      clock.mockReturnValue(numeric(step.now))
      const decision = checkTokenBudget(tracker, step.agentId, step.budget === null ? null : numeric(step.budget), numeric(step.tokens))
      results.push({ decision: project(decision), tracker: project(tracker) })
      break
    }
    case 'message': results.push({ message: getBudgetContinuationMessage(numeric(step.pct), numeric(step.tokens), numeric(step.budget)) }); break
  }
}
clock.mockRestore()
process.stdout.write(JSON.stringify({ results }))
