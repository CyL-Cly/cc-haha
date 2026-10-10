import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(process.env.HOME, 'side question API Source')
if (process.env.CC_HAHA_SIDE_QUESTION_FIXTURE !== '1') throw new Error('Missing fixture marker')
await import('../../preload')
if (input.contexts) {
  const crypto = await import('crypto')
  let allocated = 0
  mock.module('crypto', () => ({ ...crypto, randomUUID: () => `allocated-${++allocated}` }))
  const OriginalDate = Date
  globalThis.Date = class extends OriginalDate { constructor(...args: any[]) { super(args.length ? args[0] : 1700000000000) } } as DateConstructor
  let captured: any
  const originalFork = await import('../../src/utils/forkedAgent')
  mock.module('../../src/utils/forkedAgent', () => ({ ...originalFork, runForkedAgent: async (options: any) => {
    captured = options
    return { messages: [{ type: 'assistant', message: { content: [{ type: 'text', text: 'side answer' }] } }], totalUsage: {} }
  } }))
  const { prepareSideQuestionContext, runSideQuestion } = await import('../../src/utils/sideQuestion')
  const { sideQuestionInputSchema } = await import('../../src/server/services/sideQuestionService')
  const rows = []
  for (const row of input.contexts) {
    allocated = 0
    const parsed = sideQuestionInputSchema.parse(row.input)
    const before = JSON.stringify(row.history)
    const prepared = prepareSideQuestionContext(row.history)
    allocated = 0
    await runSideQuestion({ question: parsed.question, history: parsed.history, cacheSafeParams: { forkContextMessages: [] } as any })
    rows.push({ prepared, prompt: captured.promptMessages, skipTranscript: captured.skipTranscript, skipCacheWrite: captured.skipCacheWrite, maxTurns: captured.maxTurns, parentUnchanged: before === JSON.stringify(row.history) })
  }
  process.stdout.write(JSON.stringify(rows)); process.exit(0)
}

// The only substituted boundary is the live CLI channel. The original route,
// schema, pending service, request signal and response race all execute.
mock.module('../../src/server/services/conversationService', () => ({ conversationService: {
  hasSession: () => true,
  requestControl: () => { throw new Error('Unexpected control in validation-only fixture') },
} }))
mock.module('../../src/server/ws/handler', () => ({ ensureCliSessionStartedForControl: () => { throw new Error('Unexpected startup in validation-only fixture') } }))
const { handleSideQuestionRoute } = await import('../../src/server/api/sideQuestions')
const rows = []
for (const step of input.steps) {
  const url = new URL(step.path, 'http://127.0.0.1:3000')
  const req = new Request(url, { method: step.method, ...(step.body !== undefined ? { body: step.body } : {}) })
  let response: Response
  try { response = await handleSideQuestionRoute(req, url, 'existing', step.questionId) }
  catch (error) { const { errorResponse } = await import('../../src/server/middleware/errorHandler'); response = errorResponse(error) }
  rows.push({ status: response.status, body: await response.json() })
}
process.stdout.write(JSON.stringify(rows))

process.exit(0)
