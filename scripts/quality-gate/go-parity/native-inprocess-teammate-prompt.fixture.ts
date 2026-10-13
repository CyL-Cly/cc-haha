import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual in-process teammate system composition')
if (process.env.CC_HAHA_INPROCESS_TEAM_PROMPT_FIXTURE !== '1') throw new Error('Missing teammate prompt fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
await import('../../../src/utils/swarm/inProcessRunner')
const { TEAMMATE_SYSTEM_PROMPT_ADDENDUM } = await import('../../../src/utils/swarm/teammatePromptAddendum')
const { parse } = await import('acorn')
const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/utils/swarm/inProcessRunner.ts', import.meta.url)).text())
const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
const declarations = ast.body.map((node: any) => node.type === 'ExportNamedDeclaration' ? node.declaration : node)
const builder = declarations.find((node: any) => node?.type === 'FunctionDeclaration' && node.id.name === 'buildTeammateSystemPrompt')
if (!builder) throw new Error('Original system composition declaration changed')
const compose = new Function('getSystemPrompt', 'TEAMMATE_SYSTEM_PROMPT_ADDENDUM', `${source.slice(builder.start, builder.end)}; return buildTeammateSystemPrompt`)
const { formatTeammateMessage } = await import('../../../src/utils/teammateMailbox')
const rows = []
for (const row of input.rows) {
  const definition = row.custom === undefined ? undefined : { getSystemPrompt: () => row.custom }
  rows.push(await compose(async () => row.base, TEAMMATE_SYSTEM_PROMPT_ADDENDUM)({systemPromptMode:row.mode,systemPrompt:row.override,toolUseContext:{options:{tools:[],mainLoopModel:'fixture-model',mcpClients:[]}},agentDefinition:definition}))
}
process.stdout.write(JSON.stringify({ rows, messages: input.messages.map((row: any) => formatTeammateMessage({from:row.from,text:row.content,color:row.color,summary:row.summary})), capabilities }))
