import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual in-process teammate system composition')
if (process.env.CC_HAHA_INPROCESS_TEAM_PROMPT_FIXTURE !== '1') throw new Error('Missing teammate prompt fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
await import('../../../src/utils/swarm/inProcessRunner')
const { TEAMMATE_SYSTEM_PROMPT_ADDENDUM } = await import('../../../src/utils/swarm/teammatePromptAddendum')
const { TEAMMATE_MESSAGE_TAG } = await import('../../../src/constants/xml')
const { parse } = await import('acorn')
const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/utils/swarm/inProcessRunner.ts', import.meta.url)).text())
const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
const declarations = ast.body.map((node: any) => node.type === 'ExportNamedDeclaration' ? node.declaration : node)
const runner = declarations.find((node: any) => node?.type === 'FunctionDeclaration' && node.id.name === 'runInProcessTeammate')
const start = runner.body.body.findIndex((node: any) => node.type === 'VariableDeclaration' && node.declarations.some((entry: any) => entry.id.name === 'teammateSystemPrompt'))
if (start < 0 || runner.body.body[start + 1]?.type !== 'IfStatement') throw new Error('Original system composition branch changed')
const body = source.slice(runner.body.body[start].start, runner.body.body[start + 1].end)
const compose = new Function('getSystemPrompt', 'TEAMMATE_SYSTEM_PROMPT_ADDENDUM', 'systemPromptMode', 'systemPrompt', 'toolUseContext', 'agentDefinition', `return (async () => { ${body}; return teammateSystemPrompt })()`)
const formatNode = declarations.find((node: any) => node?.type === 'FunctionDeclaration' && node.id.name === 'formatAsTeammateMessage')
const format = new Function('TEAMMATE_MESSAGE_TAG', `${source.slice(formatNode.start, formatNode.end)}; return formatAsTeammateMessage`)(TEAMMATE_MESSAGE_TAG)
const rows = []
for (const row of input.rows) {
  const definition = row.custom === undefined ? undefined : { getSystemPrompt: () => row.custom }
  rows.push(await compose(async () => row.base, TEAMMATE_SYSTEM_PROMPT_ADDENDUM, row.mode, row.override, { options: { tools: [], mainLoopModel: 'fixture-model', mcpClients: [] } }, definition))
}
process.stdout.write(JSON.stringify({ rows, messages: input.messages.map((row: any) => format(row.from, row.content, row.color, row.summary)), capabilities }))
