import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities,absentManagedSettingsFixturePaths } from './client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir,'Actual Team proposal/preset snapshots')
if(process.env.CC_HAHA_TEAM_CONTEXT_FIXTURE!=='1')throw new Error('Missing Team context fixture')
const capabilities=await installClientFixtureCapabilities(input.homeDir,{absentPaths:absentManagedSettingsFixturePaths()})
await import('../../../preload')
const bootstrap=await import('../../../src/bootstrap/state');bootstrap.setCwdState(input.homeDir);bootstrap.setOriginalCwd(input.homeDir);bootstrap.setProjectRoot(input.homeDir)
const config=await import('../../../src/utils/config');config.enableConfigs()
const {parseAgentFromJson}=await import('../../../src/tools/AgentTool/loadAgentsDir')
const {getBuiltInAgentsWithoutOverrides}=await import('../../../src/tools/AgentTool/builtInAgents')
const source=await import('../../../src/tools/TeamPlanTool/context')
const {getTeamLeaderRuntime}=await import('../../../src/utils/swarm/teamPlanPolicy')
const rows=[]
for(const row of input.cases){
 if(row.runtime===undefined)delete process.env.CC_HAHA_TEAM_LEADER_RUNTIME;else process.env.CC_HAHA_TEAM_LEADER_RUNTIME=row.runtime
 if(row.subagent===undefined)delete process.env.CLAUDE_CODE_SUBAGENT_MODEL;else process.env.CLAUDE_CODE_SUBAGENT_MODEL=row.subagent
 const active=getBuiltInAgentsWithoutOverrides().filter(agent=>['general-purpose','Explore'].includes(agent.agentType))
 for(const [name,definition]of Object.entries(row.agents??{})){const agent=parseAgentFromJson(name,definition as any,'flagSettings');if(agent)active.push(agent)}
 const context:any={options:{mainLoopModel:row.model,agentDefinitions:{activeAgents:active}},getAppState:()=>({mainLoopModel:null})}
 try{const leader=getTeamLeaderRuntime(row.model??'');const catalog=source.snapshotTeamAgents(context);const proposal=source.resolveProposedTeamPlan(row.proposal,context);const plan={teamName:'actual-context',planId:'durable-plan',revision:3,state:row.state??'draft',members:proposal.members,tasks:proposal.tasks};rows.push({leader,catalog,proposal,result:source.teamPlanToolResult(plan as any),error:null})}
 catch(error){rows.push({error:(error as Error).message})}
}
process.stdout.write(JSON.stringify({rows,capabilities}))
