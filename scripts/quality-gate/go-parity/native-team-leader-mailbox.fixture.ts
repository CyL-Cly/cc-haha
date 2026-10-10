import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual leader SendMessage tool and physical inbox')
if (process.env.CC_HAHA_TEAM_LEADER_MAILBOX_FIXTURE !== '1') throw new Error('Missing leader mailbox fixture')
const absent = [...absentManagedSettingsFixturePaths()]
for (let p=dirname(input.homeDir);;p=dirname(p)) {absent.push(join(p,'.git'));if(dirname(p)===p)break}
const capabilities=await installClientFixtureCapabilities(input.homeDir,{absentPaths:absent})
await import('../../../preload')
const { writeTeamFileAsync }=await import('../../../src/utils/swarm/teamHelpers')
const { readMailbox }=await import('../../../src/utils/teammateMailbox')
const { SendMessageTool }=await import('../../../src/tools/SendMessageTool/SendMessageTool')
const bootstrap=await import('../../../src/bootstrap/state');bootstrap.setCwdState(input.homeDir);bootstrap.setOriginalCwd(input.homeDir);bootstrap.setProjectRoot(input.homeDir);bootstrap.setIsInteractive(false)
const config=await import('../../../src/utils/config');config.enableConfigs()
const rows=[]
for(const [index,row] of input.rows.entries()) {
  const team=row.team ? `actual-team-${index}`:undefined
  if(team)await writeTeamFileAsync(team,{name:team,leadSessionId:'actual',createdAt:1,members:row.members} as any)
  const state:any={agentNameRegistry:new Map(),tasks:{},...(team?{teamContext:{teamName:team,teammates:row.teammates??{}}}:{})}
  let data:any,error:any=null
  try{data=(await SendMessageTool.call(row.input,{getAppState:()=>state,options:{},abortController:new AbortController()} as any,undefined as any,undefined as any)).data}catch(failure){error=(failure as Error).message}
  const inboxes:any={}
  for(const recipient of row.recipients??[])inboxes[recipient]=(await readMailbox(recipient,team)).map(({timestamp,id,...message}:any)=>message)
  rows.push({data:data??null,error,inboxes})
}
process.stdout.write(JSON.stringify({rows,capabilities}))
