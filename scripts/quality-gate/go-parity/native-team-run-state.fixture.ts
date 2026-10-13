import { parse } from 'acorn'
import { assertDisposableFixtureHome } from './fixture-guard'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual desktop SDK run-state observer')
if (process.env.CC_HAHA_TEAM_RUN_STATE_FIXTURE !== '1') throw new Error('Missing run-state fixture')
const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/server/ws/handler.ts', import.meta.url)).text())
const tree = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
const functions = new Map<string, string>()
for (const outer of tree.body as any[]) {
  const node = outer.type === 'ExportNamedDeclaration' ? outer.declaration : outer
  if (node?.type === 'FunctionDeclaration') functions.set(node.id.name, source.slice(node.start, node.end))
}
const consumerNames = ['trackCliRunState', 'translateCliMessage', 'trackCliRunForegroundBoundary', 'resumeCliRunForeground', 'isRootModelActivity', 'clearBackgroundTaskDisconnectCeiling']
const consumerFunctions = consumerNames.map(name => {
  const body = functions.get(name)
  if (!body) throw new Error(`Missing original SDK consumer ${name}`)
  return body
})
const parsingSource = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/server/ws/cliMessageParsing.ts', import.meta.url)).text())
const parsingTree = parse(parsingSource, { ecmaVersion: 'latest', sourceType: 'module' })
const parentNode = (parsingTree.body as any[]).map(outer => outer.type === 'ExportNamedDeclaration' ? outer.declaration : outer).find(node => node?.type === 'FunctionDeclaration' && node.id.name === 'cliParentToolUseId')
if (!parentNode) throw new Error('Missing source stream parent parser')
const active = new Set<string>()
const background = new Set<string>()
const ceilings = new Map<string, ReturnType<typeof setTimeout>>()
const call = Function('activeCliRuns', 'cliRunsAwaitingBackgroundTasks', 'backgroundTaskCleanupTimers', 'conversationService', 'getStreamState', 'isAgentRunMessageFrame', `${parsingSource.slice(parentNode.start, parentNode.end)}\n${consumerFunctions.join('\n')}\nreturn { trackCliRunState, translateCliMessage }`)(active, background, ceilings, { hasSession: () => true }, () => ({}), () => false)
const rows = input.rows.map((frame: any) => {
  const state = call.trackCliRunState('private-session', frame)
  return { state, running: active.has('private-session'), projection: call.translateCliMessage(frame, 'private-session') }
})
process.stdout.write(JSON.stringify({ rows }))
