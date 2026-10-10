import {assertDisposableFixtureHome} from './fixture-guard'
import {absentManagedSettingsFixturePaths,installClientFixtureCapabilities} from './client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir,'Actual in-process teammate preset producer')
if(process.env.CC_HAHA_INPROCESS_TEAM_PRESET_FIXTURE!=='1')throw new Error('Missing teammate preset fixture')
const capabilities=await installClientFixtureCapabilities(input.homeDir,{absentPaths:absentManagedSettingsFixturePaths()})
await import('../../../preload')
const {buildInProcessTeammateAgentDefinition}=await import('../../../src/utils/swarm/inProcessRunner')
const rows=input.rows.map((row:any)=>{const result=buildInProcessTeammateAgentDefinition(row.name,row.system,row.definition);const {getSystemPrompt,...fields}=result;return {fields,prompt:getSystemPrompt()}})
process.stdout.write(JSON.stringify({rows,capabilities}))
