import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.homeDir, 'Native Session Inbox')
if (process.env.CC_HAHA_NATIVE_SESSION_INBOX_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Unowned session inbox fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
;(globalThis as any).MACRO = { BUILD_TIME: '' }
const { createSessionMessageInbox, sessionMessageInputSchema, sessionMessageUuid, consumeSessionMessage } = await import('../../../src/utils/sessionMessageInbox.js')
const { sessionCollaborationTools } = await import('../../../src/tools/SessionCollaborationTool/SessionCollaborationTool.js')
const { toolToAPISchema } = await import('../../../src/utils/api.js')
const { getEmptyToolPermissionContext } = await import('../../../src/Tool.js')
const { normalizeMessage } = await import('../../../src/utils/queryHelpers.js')
const queuedNormalized = (input.queued ?? []).map((message: any) => [...normalizeMessage(message)])
const parsed = []
for (const row of input.inputs ?? []) {
  const result = sessionMessageInputSchema.safeParse(row)
  parsed.push(result.success ? { value: result.data } : { issues: result.error.issues })
}
const toolRows = []
for (const row of input.toolInputs ?? []) {
  const tool = sessionCollaborationTools.find(tool => tool.name === row.name)!
  const result = tool.inputSchema.safeParse(row.input)
  toolRows.push(result.success ? { value: result.data } : { issues: result.error.issues })
}
const schemas = []
for (const tool of sessionCollaborationTools) schemas.push(await toolToAPISchema(tool, { tools: sessionCollaborationTools, agents: [], model: 'deepseek-flash', getToolPermissionContext: async () => getEmptyToolPermissionContext() }))
const cases = []
for (const row of input.cases ?? []) {
  let queue: any[] = [], receipts: any[] = [], failEnqueue = false
  const inbox = createSessionMessageInbox(command => { if (failEnqueue) throw new Error('physical queue failure'); queue.push(command) }, receipt => receipts.push(receipt), row.history)
  const observations = []
  for (const op of row.operations) {
    try {
      let result: unknown
      if (op.action === 'accept') result = inbox.accept(op.input, op.persisted)
      else if (op.action === 'consume') result = consumeSessionMessage(sessionMessageUuid(op.messageId))
      else if (op.action === 'cancel') result = inbox.cancelQueued(predicate => { const removed = queue.filter(predicate); queue = queue.filter(command => !predicate(command)); return removed })
      else if (op.action === 'fail') failEnqueue = op.enabled
      else if (op.action === 'dispose') result = inbox.dispose()
      observations.push({ result: result ?? null })
    } catch (error: any) { observations.push({ error: error.message }) }
  }
  cases.push({ observations, queue, receipts })
  inbox.dispose()
}
process.stdout.write(JSON.stringify({ parsed, toolRows, schemas, cases, uuids: (input.ids ?? []).map(sessionMessageUuid), queuedNormalized, capabilities }) + '\n')
