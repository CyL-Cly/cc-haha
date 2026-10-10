import { spyOn } from 'bun:test'
import * as crypto from 'node:crypto'
import { join } from 'node:path'

type Step = { operation: string; options?: Record<string, unknown>; toolUse?: boolean; input?: string; preceding?: unknown[]; a?: unknown; b?: unknown; blocksA?: unknown[]; blocksB?: unknown[]; universal?: boolean; history?: unknown[]; strict?: boolean }
const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as { homeDir: string; steps: Step[] }
if (!input.homeDir || input.homeDir !== process.env.HOME || process.env.CLAUDE_CONFIG_DIR !== join(input.homeDir, '.claude')) {
  throw new Error('message fixture requires explicit disposable HOME/config')
}
;(globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }).MACRO = { BUILD_TIME: '' }
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(input.homeDir)
bootstrap.setOriginalCwd(input.homeDir)
bootstrap.setProjectRoot(input.homeDir)
const legacy = await import('../../../src/utils/messages')
const growthbook = await import('../../../src/services/analytics/growthbook')
const analytics = await import('../../../src/services/analytics')
const logging = await import('../../../src/utils/log')
let repairEvent: unknown
let diagnostic: string | undefined
const originalEvent = analytics.logEvent
const originalLog = logging.logError
spyOn(analytics, 'logEvent').mockImplementation((name, metadata) => {
  if (name === 'tengu_tool_result_pairing_repaired') repairEvent = metadata
  originalEvent(name, metadata)
})
spyOn(logging, 'logError').mockImplementation(error => {
  if (error instanceof Error && error.message.startsWith('ensureToolResultPairing:')) diagnostic = error.message
  originalLog(error)
})
const originalGate = growthbook.checkStatsigFeatureGate_CACHED_MAY_BE_STALE
let universalSmoosh = false
// Freeze the single configuration input used by the actual merge functions.
// Message merging itself remains the unmodified production implementation.
spyOn(growthbook, 'checkStatsigFeatureGate_CACHED_MAY_BE_STALE').mockImplementation(gate => gate === 'tengu_chair_sermon' ? universalSmoosh : originalGate(gate))
let uuidCalls = 0
spyOn(crypto, 'randomUUID').mockImplementation(() => `fixture-id-${++uuidCalls}` as ReturnType<typeof crypto.randomUUID>)
const OriginalDate = Date
globalThis.Date = class extends OriginalDate {
  constructor(value?: string | number) { super(value === undefined ? 1_700_000_000_000 : value) }
  static now() { return 1_700_000_000_000 }
} as DateConstructor
const results: unknown[] = []
for (const step of input.steps) {
  universalSmoosh = step.universal === true
  switch (step.operation) {
    case 'user': results.push(legacy.createUserMessage(step.options as Parameters<typeof legacy.createUserMessage>[0])); break
    case 'assistant': results.push(legacy.createAssistantMessage(step.options as Parameters<typeof legacy.createAssistantMessage>[0])); break
    case 'api-error': results.push(legacy.createAssistantAPIErrorMessage(step.options as Parameters<typeof legacy.createAssistantAPIErrorMessage>[0])); break
    case 'interrupt': results.push(legacy.createUserInterruptionMessage({ toolUse: step.toolUse })); break
    case 'prepare': results.push(legacy.prepareUserContent({ inputString: step.input!, precedingInputBlocks: step.preceding as Parameters<typeof legacy.prepareUserContent>[0]['precedingInputBlocks'] })); break
    case 'merge-content': results.push({ merged: legacy.mergeUserContentBlocks(step.blocksA as Parameters<typeof legacy.mergeUserContentBlocks>[0], step.blocksB as Parameters<typeof legacy.mergeUserContentBlocks>[1]), blocksAAfter: step.blocksA, blocksBAfter: step.blocksB }); break
    case 'reorder-tools': results.push({ merged: legacy.reorderAssistantToolUseBlocks(step.blocksA as Parameters<typeof legacy.reorderAssistantToolUseBlocks>[0]), blocksAAfter: step.blocksA }); break
    case 'merge-user': results.push({ merged: legacy.mergeUserMessages(step.a as Parameters<typeof legacy.mergeUserMessages>[0], step.b as Parameters<typeof legacy.mergeUserMessages>[1]), aAfter: step.a, bAfter: step.b }); break
    case 'merge-user-tools': results.push({ merged: legacy.mergeUserMessagesAndToolResults(step.a as Parameters<typeof legacy.mergeUserMessagesAndToolResults>[0], step.b as Parameters<typeof legacy.mergeUserMessagesAndToolResults>[1]), aAfter: step.a, bAfter: step.b }); break
    case 'merge-assistant': results.push({ merged: legacy.mergeAssistantMessages(step.a as Parameters<typeof legacy.mergeAssistantMessages>[0], step.b as Parameters<typeof legacy.mergeAssistantMessages>[1]), aAfter: step.a, bAfter: step.b }); break
    case 'pairing': {
      bootstrap.setStrictToolResultPairing(step.strict === true)
      repairEvent = undefined
      diagnostic = undefined
      let messages: unknown
      let error: string | undefined
      try { messages = legacy.ensureToolResultPairing(step.history as Parameters<typeof legacy.ensureToolResultPairing>[0]) } catch (failure) { if (!(failure instanceof Error)) throw failure; error = failure.message }
      results.push({ messages, error, repairEvent, diagnostic, historyAfter: step.history })
      break
    }
    default: throw new Error(`unsupported message operation: ${step.operation}`)
  }
}
// Object property order is model-visible through the final request's CCH.
// Put the unmodified production content bytes inside strings so trace object
// canonicalization cannot hide a field-order difference.
const contentBytes = results.map((result, index) => {
  const step = input.steps[index]!
  const row = result as { merged?: { message?: { content?: unknown } } | unknown[]; message?: { content?: unknown }; messages?: { message: { content: unknown } }[] }
  let content: unknown
  if (step.operation === 'prepare') content = result
  else if (step.operation === 'merge-content' || step.operation === 'reorder-tools') content = row.merged
  else if (step.operation === 'pairing') content = row.messages?.map(message => message.message.content)
  else if (step.operation.startsWith('merge-')) content = (row.merged as { message: { content: unknown } }).message.content
  else content = row.message?.content
  return JSON.stringify(content) ?? null
})
process.stdout.write(`${JSON.stringify({ results, uuidCalls, contentBytes })}\n`)
