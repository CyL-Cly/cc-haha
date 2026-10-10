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
const track = functions.get('trackCliRunState'), translate = functions.get('translateCliMessage')
if (!track || !translate) throw new Error('Missing original SDK consumer')
const active = new Set<string>()
const call = Function('activeCliRuns', 'conversationService', 'getStreamState', 'isAgentRunMessageFrame', `${track}\n${translate}\nreturn { trackCliRunState, translateCliMessage }`)(active, { hasSession: () => true }, () => ({}), () => false)
const rows = input.rows.map((frame: any) => {
  const state = call.trackCliRunState('private-session', frame)
  return { state, running: active.has('private-session'), projection: call.translateCliMessage(frame, 'private-session') }
})
process.stdout.write(JSON.stringify({ rows }))
