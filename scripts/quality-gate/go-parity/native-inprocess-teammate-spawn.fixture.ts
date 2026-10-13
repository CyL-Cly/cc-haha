import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual native teammate spawn producer')
if (process.env.CC_HAHA_INPROCESS_TEAM_SPAWN_FIXTURE !== '1') throw new Error('Missing teammate spawn fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
await import('../../../src/utils/swarm/inProcessRunner')
const state = await import('../../../src/bootstrap/state')
state.setOriginalCwd(input.cwd)
state.setCwdState(input.cwd)
state.setIsInteractive(false)
state.switchSession(input.sessionId)
const { enableConfigs } = await import('../../../src/utils/config')
enableConfigs()
const helpers = await import('../../../src/utils/swarm/teamHelpers')
const { spawnInProcessTeammate } = await import('../../../src/utils/swarm/spawnInProcess')
const { resolveTeammateModel } = await import('../../../src/utils/swarm/resolveTeammateModel')
const { assignTeammateColor, clearTeammateColors } = await import('../../../src/utils/swarm/teammateLayoutManager')
const { formatAgentId } = await import('../../../src/utils/agentId')
const { sanitizeAgentName } = helpers
const { isCustomAgent, isPluginAgent } = await import('../../../src/tools/AgentTool/loadAgentsDir')
const { logForDebugging } = await import('../../../src/utils/debug')
const { getCwd } = await import('../../../src/utils/cwd')
const { drainSdkEvents } = await import('../../../src/utils/sdkEventQueue')
const { parse } = await import('acorn')
const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/tools/shared/spawnMultiAgent.ts', import.meta.url)).text())
const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
const declarations = ast.body.map((node: any) => node.type === 'ExportNamedDeclaration' ? node.declaration : node)
const unique = declarations.find((node: any) => node?.type === 'FunctionDeclaration' && node.id.name === 'generateUniqueTeammateName')
const pick = declarations.find((node:any)=>node?.type==='FunctionDeclaration' && node.id.name==='pickUniqueTeammateName')
const pickUniqueTeammateName=new Function(`${source.slice(pick.start,pick.end)}; return pickUniqueTeammateName`)()
const handler = declarations.find((node: any) => node?.type === 'FunctionDeclaration' && node.id.name === 'handleSpawnInProcess')
if (!unique || !handler) throw new Error('Original native teammate spawn declarations changed')
const generateUniqueTeammateName = new Function('readTeamFileAsync', `${source.slice(unique.start, unique.end)}; return generateUniqueTeammateName`)(helpers.readTeamFileAsync)
const make = new Function('pickUniqueTeammateName','resolveTeammateModel','generateUniqueTeammateName','sanitizeAgentName','formatAgentId','assignTeammateColor','isCustomAgent','isPluginAgent','logForDebugging','spawnInProcessTeammate','startInProcessTeammate','TEAM_LEAD_NAME','getCwd','mutateTeamFileAsync', `${source.slice(handler.start, handler.end)}; return handleSpawnInProcess`)
const rows = []
for (const row of input.rows) {
  clearTeammateColors()
  await helpers.writeTeamFileAsync(row.team.name, row.team)
  let current: any = { mainLoopModel: row.leaderModel, tasks: {}, teamContext: row.teamContext }
  let launched: any
  const run = make(pickUniqueTeammateName,resolveTeammateModel,generateUniqueTeammateName,sanitizeAgentName,formatAgentId,assignTeammateColor,isCustomAgent,isPluginAgent,logForDebugging,spawnInProcessTeammate,(config: any) => { launched = config },'team-lead',getCwd,helpers.mutateTeamFileAsync)
  const result = await run(row.input, { options:{agentDefinitions:{activeAgents:row.definitions ?? []}}, messages:[{hugeOriginalHistory:true}], toolUseId:row.toolUseId, getAppState:()=>current,setAppState:(update:any)=>{current=update(current)} })
  const task: any = Object.values(current.tasks)[0]
  for (const key of ['abortController','unregisterCleanup']) delete task[key]
  rows.push({ result, task, context:current.teamContext, team:await helpers.readTeamFileAsync(row.team.name), started:drainSdkEvents().map(({uuid,session_id,...event}:any)=>event), launch:launched ? { identity:launched.identity, taskId:launched.taskId, prompt:launched.prompt, description:launched.description, model:launched.model, agentType:launched.agentDefinition?.agentType, messages:launched.toolUseContext.messages } : null })
}
process.stdout.write(JSON.stringify({rows,capabilities}))
