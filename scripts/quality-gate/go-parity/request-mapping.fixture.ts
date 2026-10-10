import { spyOn } from 'bun:test'
import { join } from 'node:path'
import type { MappingMutation, RequestMappingRequest, RequestMappingStep } from './request-mapping-fixtures'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as RequestMappingRequest
if (!input.homeDir || input.homeDir !== process.env.HOME || process.env.CLAUDE_CONFIG_DIR !== join(input.homeDir, '.claude')) throw new Error('Request mapping fixture requires explicit disposable HOME/config')
;(globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }).MACRO = { BUILD_TIME: '' }
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(input.homeDir)
bootstrap.setOriginalCwd(input.homeDir)
bootstrap.setProjectRoot(input.homeDir)
const legacy = await import('../../../src/services/api/claude')
const analytics = await import('../../../src/services/analytics/index')

const events: unknown[] = []
const eligibility = bootstrap.getPromptCache1hEligible
// Observe the real factory's pre-latched configuration read. Mapping, TTL
// selection, and allowlist matching all execute their original production code.
spyOn(bootstrap, 'getPromptCache1hEligible').mockImplementation(() => { events.push({ operation: 'cache-control' }); return eligibility() })
analytics.attachAnalyticsSink({ logEvent: (name, metadata) => { events.push({ operation: 'event', name, metadata }) }, logEventAsync: async () => {} })

function referenceFacts(original: any, mapped: any): unknown {
  if (Array.isArray(original) && Array.isArray(mapped)) return original.map((entry, index) => referenceFacts(entry, mapped[index]))
  const before = original?.message?.content
  const after = mapped?.content
  return {
    contentSame: before === after,
    blockSame: Array.isArray(before) && Array.isArray(after) ? before.map((entry, index) => entry === after[index]) : null,
    nestedSame: Array.isArray(before) && Array.isArray(after) ? before.map((entry, index) => entry?.nested === after[index]?.nested) : null,
  }
}
function copy(value: unknown): unknown { return JSON.parse(JSON.stringify(value)) }
function mutate(roots: Record<MappingMutation['target'], any>, action: MappingMutation) {
  let target = roots[action.target]
  for (const key of action.path) target = target[key]
  if (action.operation === 'append') target.push(action.value)
  else target[action.key!] = action.value
}
function call(step: RequestMappingStep) {
  if (step.operation === 'breakpoints') return legacy.addCacheBreakpoints(step.messages as Parameters<typeof legacy.addCacheBreakpoints>[0], step.enabled ?? false, step.querySource, false, undefined, undefined, step.skipCacheWrite ?? false)
  const mapper = step.operation === 'user' ? legacy.userMessageToMessageParam : legacy.assistantMessageToMessageParam
  return mapper(step.message as never, step.addCache ?? false, step.enabled ?? false, step.querySource)
}
const results: unknown[] = []
for (const step of input.steps) {
  if (step.messageSource !== undefined) step.message = JSON.parse(step.messageSource)
  bootstrap.setPromptCache1hEligible(step.eligible ?? false)
  bootstrap.setPromptCache1hAllowlist(step.allowlist ?? [])
  const eventStart = events.length
  const original = step.operation === 'breakpoints' ? step.messages : step.message
  try {
    const output = call(step)
    const initial = copy(output)
    const initialWire = JSON.stringify(output)
    const references = referenceFacts(original, output)
    const repeat = step.repeat ? call(step) : undefined
    const repeatReferences = step.repeat ? referenceFacts(original, repeat) : null
    for (const action of step.mutations ?? []) mutate({ input: original, output, repeat }, action)
    results.push({ initial, initialWire, references, repeatReferences, outputAfter: copy(output), ...(step.repeat ? { repeatAfter: copy(repeat) } : {}), inputAfter: copy(original), events: events.slice(eventStart) })
  } catch (error) {
    results.push({ error: { name: (error as Error).name, message: (error as Error).message }, inputAfter: copy(original), events: events.slice(eventStart) })
  }
}
process.stdout.write(JSON.stringify({ results }))
