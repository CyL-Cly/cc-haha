import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities,absentManagedSettingsFixturePaths } from './client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir,'Actual Team runtime snapshot')
if(process.env.CC_HAHA_TEAM_SNAPSHOT_FIXTURE!=='1')throw new Error('Missing Team snapshot fixture')
const capabilities=await installClientFixtureCapabilities(input.homeDir,{absentPaths:absentManagedSettingsFixturePaths()})
await import('../../../preload')
const {buildTeamRuntimeSnapshot}=await import('../../../src/utils/swarm/teamRuntimeSnapshot')
const rows=input.cases.map((row:any)=>{try{return {value:buildTeamRuntimeSnapshot(row.team,row.createdAt,row.sessionId,row.path),error:null}}catch(error){return {value:null,error:(error as Error).message}}})
process.stdout.write(JSON.stringify({rows,capabilities}))
