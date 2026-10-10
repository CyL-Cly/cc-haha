export type QueryPolicyNumber = number | { source: string }
export type QueryPolicyTracker = {
  continuationCount: QueryPolicyNumber
  lastDeltaTokens: QueryPolicyNumber
  lastGlobalTurnTokens: QueryPolicyNumber
  startedAt: QueryPolicyNumber
}
export type QueryPolicyStep =
  | { operation: 'reset'; now: QueryPolicyNumber }
  | { operation: 'restore'; tracker: QueryPolicyTracker }
  | { operation: 'check'; agentId?: string; budget: QueryPolicyNumber | null; tokens: QueryPolicyNumber; now: QueryPolicyNumber }
  | { operation: 'message'; pct: QueryPolicyNumber; tokens: QueryPolicyNumber; budget: QueryPolicyNumber }
export type QueryPolicyRequest = { homeDir: string; startedAt: QueryPolicyNumber; steps: QueryPolicyStep[] }
export type QueryPolicyScenario = { startedAt?: QueryPolicyNumber; steps: QueryPolicyStep[] }

const special = (source: string): QueryPolicyNumber => ({ source })
const check = (tokens: QueryPolicyNumber, budget: QueryPolicyNumber | null = 10_000, now: QueryPolicyNumber = 1_700_000_001_000, agentId?: string): QueryPolicyStep => ({ operation: 'check', tokens, budget, now, agentId })
const restore = (continuationCount: QueryPolicyNumber, lastDeltaTokens: QueryPolicyNumber = 0, lastGlobalTurnTokens: QueryPolicyNumber = 0, startedAt: QueryPolicyNumber = 1_700_000_000_000): QueryPolicyStep => ({ operation: 'restore', tracker: { continuationCount, lastDeltaTokens, lastGlobalTurnTokens, startedAt } })
const message = (pct: QueryPolicyNumber, tokens: QueryPolicyNumber, budget: QueryPolicyNumber): QueryPolicyStep => ({ operation: 'message', pct, tokens, budget })

// These are ordered calls into the real source policy, not a fixture-owned
// decision model. NaN and overflowing JSON spellings survive the input seam.
export const queryPolicyScenarios: Array<[string, QueryPolicyScenario]> = [
  ['disabled budgets and subagents leave tracker intact', { steps: [restore(4, 10, 120), check(130, null), check(130, 0), check(130, -1), check(130, special('-0')), check(130, 10_000, 200, 'child'), check(130, 10_000, 300, ' '), check(130, 10_000, 400, '')] }],
  ['initial near target stop has no completion event', { steps: [check(9000), check(10000), check(15000), check(special('Infinity'))] }],
  ['ninety percent threshold and immutable stop tracker', { steps: [check(8999.999), check(9000), check(8999.999), check(9000, 10_000, 1_700_000_003_000)] }],
  ['diminishing returns needs three continuations and two small deltas', { steps: [check(0), check(100), check(200), check(300), check(301)] }],
  ['exact 500 delta does not count as diminishing', { steps: [check(1000), check(1500), check(2000), check(2499), check(2999), check(3498), check(3997)] }],
  ['declining cumulative usage follows existing arithmetic', { steps: [check(3000), check(2000), check(1000), check(0), check(-100)] }],
  ['target and elapsed time can change without resetting tracker', { steps: [check(4000), check(5000, 6000, 1_699_999_999_000), check(5500, 10_000), { operation: 'reset', now: 10 }, check(6000, 6000, 20)] }],
  ['percentage rounding preserves ties and negative zero', { steps: [-100.5, -50, -49.999, special('-0'), 0.49999999999999994, 49.999, 50, 50.00000000000001, 150, 250].map(tokens => check(tokens)) }],
  ['nonfinite source numbers keep comparisons and mutations', { steps: [check(1, special('NaN')), check(1, special('Infinity')), check(special('Infinity'), special('Infinity')), check(special('NaN'), 100), check(special('-Infinity'), 100), check(2, special('-Infinity'))] }],
  ['restored nonfinite tracker and completion duration', { steps: [restore(special('NaN'), special('NaN'), special('NaN'), special('NaN')), check(9000), check(1), restore(special('Infinity'), 1, 1, special('-Infinity')), check(2, 100, special('Infinity'))] }],
  ['restored count fractional precision and timestamp', { steps: [restore(2.5, 499, 100), check(200), check(201), restore(9_007_199_254_740_992, 500, 0, special('-0')), check(1000), check(9500, 10_000, special('-0'))] }],
  ['huge and subnormal budgets retain original multiplication overflow', { steps: [check(1e307, 1e308), check(1e308, 1e308), { operation: 'reset', now: 0 }, check(0, Number.MIN_VALUE, 1), check(Number.MIN_VALUE, Number.MIN_VALUE, 2), check(0, { source: '1e-400' }, 3)] }],
  ['Intl decimal ties grouping signed zero and carry', { steps: [message(1, 1234.5675, 1_000_000), message(special('-0'), special('-0'), 0), message(0, 1.0005, -1.0005), message(0, 0.00049999999999999995, -0.0005), message(0, 999.9995, 9_999_999.9995), message(0, 0.001, 0.01)] }],
  ['Intl large decimal spelling and JavaScript percentage interpolation', { steps: [message(1e20, { source: '1000000000000000100' }, 1e21), message(1e-7, 1e-7, 1e-6), message(-1.25, 12345.5, -12345.5), message(-1e21, 1e308, Number.MIN_VALUE), message(1.25, 1e-6, 1e20), message(1e-6, 1.25, 12.125), message(1.25e21, 1.25e21, 10_000)] }],
  ['Intl nonfinite values are model-visible strings', { steps: [message(special('NaN'), special('NaN'), special('Infinity')), message(special('Infinity'), special('-Infinity'), 0), message(special('-Infinity'), 1, special('NaN'))] }],
]
