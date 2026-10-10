import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Native Question permission content')
if (process.env.CC_HAHA_NATIVE_QUESTION_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Question content requires disposable production configuration')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
;(globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }).MACRO = { BUILD_TIME: '' }
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(input.homeDir)
bootstrap.setOriginalCwd(input.homeDir)
bootstrap.setProjectRoot(input.homeDir)
bootstrap.setIsInteractive(false)
bootstrap.setInitialMainLoopModel('deepseek-flash')
bootstrap.switchSession('00000000-0000-4000-8000-000000000000' as any)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const { AskUserQuestionTool } = await import('../../../src/tools/AskUserQuestionTool/AskUserQuestionTool')
const { runToolUse } = await import('../../../src/services/tools/toolExecution')
const { getDefaultAppState } = await import('../../../src/state/AppStateStore')
const { createAssistantMessage } = await import('../../../src/utils/messages')
const { buildDenyMessage } = await import('../../../src/server/services/conversationService')
const values = []
for (const item of input.cases ?? []) {
  const toolUse = { type: 'tool_use', id: 'actual-question-content-id', name: 'AskUserQuestion', input: item.input }
  const assistant = createAssistantMessage({ content: [toolUse] } as any)
  let state = getDefaultAppState()
  const context: any = {
    options: { commands: [], debug: false, mainLoopModel: 'deepseek-flash', tools: [AskUserQuestionTool], verbose: false, thinkingConfig: { type: 'disabled' }, mcpClients: [], mcpResources: {}, isNonInteractiveSession: true, agentDefinitions: { activeAgents: [], allAgents: [] } },
    abortController: new AbortController(), readFileState: new Map(), messages: item.history ?? [],
    getAppState: () => state, setAppState: (update: any) => { state = update(state) },
    setInProgressToolUseIDs: () => {}, setResponseLength: () => {}, updateFileHistoryState: () => {}, updateAttributionState: () => {},
  }
  const captured = []
  for await (const update of runToolUse(toolUse as any, assistant, async () => item.permission, context)) {
    const message = update.message
    if (message.type === 'user') captured.push({ content: message.message.content, toolUseResult: message.toolUseResult, ...(message.imagePasteIds && { imagePasteIds: message.imagePasteIds }) })
  }
  values.push(captured)
}
const denials = (input.denials ?? []).map((item: any) => buildDenyMessage(item.tool, item.feedback))
let stripped = []
if (input.strip) {
  const { readFile } = await import('node:fs/promises')
  const original = await readFile(new URL('../../../src/server/services/conversationService.ts', import.meta.url), 'utf8')
  const start = original.indexOf('function stripAutomaticQuestionMarker(')
  const end = original.indexOf('\nfunction parseAutoQuestions(', start)
  if (start < 0 || end < 0) throw new Error('Original manual Question marker boundary changed')
  const code = new Bun.Transpiler({ loader: 'ts' }).transformSync(original.slice(start, end))
  const strip = new Function(code + '; return stripAutomaticQuestionMarker')()
  stripped = input.strip.map(strip)
}
process.stdout.write(JSON.stringify({ values, denials, stripped, capabilities }))
