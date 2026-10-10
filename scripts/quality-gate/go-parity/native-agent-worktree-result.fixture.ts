import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths,installClientFixtureCapabilities } from './client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir,'Agent worktree result')
if(process.env.CC_HAHA_AGENT_WORKTREE_RESULT_FIXTURE!=='1')throw new Error('Missing Agent Worktree result fixture')
const capabilities=await installClientFixtureCapabilities(input.homeDir,{absentPaths:absentManagedSettingsFixturePaths()})
await import('../../../preload')
const { AgentTool }=await import('../../../src/tools/AgentTool/AgentTool')
const rows=input.cases.map((data:any)=>AgentTool.mapToolResultToToolResultBlockParam(data,'original-agent-use'))
process.stdout.write(JSON.stringify({rows,capabilities}))
