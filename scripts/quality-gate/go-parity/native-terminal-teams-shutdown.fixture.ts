import fs from 'node:fs'
import {join} from 'node:path'
import {mock} from 'bun:test'
import {assertDisposableFixtureHome} from './fixture-guard'
import {absentManagedSettingsFixturePaths,installClientFixtureCapabilities} from './client-options-capabilities'

const input=await Bun.stdin.json()
const home=assertDisposableFixtureHome(input.home,'Original global Teams cleanup shutdown')
if(Bun.version!=='1.3.14'||process.env.CC_HAHA_TERMINAL_TEAMS_SHUTDOWN_SOURCE!=='1')throw new Error('Missing actual Teams shutdown guard')
const bin=join(home,'bin')
const caps=await installClientFixtureCapabilities(home,{absentPaths:absentManagedSettingsFixturePaths(),privateExecutableFiles:[join(bin,'tmux')],approvedSpawn:({command,args})=>(command==='tmux'||command===join(bin,'tmux'))&&args.length===3&&args[0]==='kill-pane'&&args[1]==='-t'&&args[2]==='%1'})
const {plainTextStorage}=await import('../../../src/utils/secureStorage/plainTextStorage')
const {createFallbackStorage}=await import('../../../src/utils/secureStorage/fallbackStorage')
const privateStorage=createFallbackStorage({name:'owned-unavailable-keychain',read:()=>null,readAsync:async()=>null,update:()=>({success:false}),delete:()=>false},plainTextStorage)
mock.module('../../../src/utils/secureStorage/index.ts',()=>({getSecureStorage:()=>privateStorage}))
;(globalThis as any).MACRO={VERSION:'999.0.0-teams-shutdown-source',BUILD_TIME:'',PACKAGE_URL:'https://fixture.invalid/package'}
await import('../../../preload')
const state=await import('../../../src/bootstrap/state')
state.setCwdState(home);state.setOriginalCwd(home);state.setProjectRoot(home);state.setAllowedSettingSources(['userSettings']);state.setIsInteractive(false)
const config=await import('../../../src/utils/config');config.enableConfigs()
const helpers=await import('../../../src/utils/swarm/teamHelpers')
const tasks=await import('../../../src/utils/tasks')
await helpers.writeTeamFileAsync(input.team,input.file)
const lifecycle=await tasks.beginTaskListLifecycle(input.team,{teamName:input.team,createdAt:input.file.createdAt})
helpers.registerTeamForSessionCleanup(input.team,lifecycle)
const {registerCleanup}=await import('../../../src/utils/cleanupRegistry')
registerCleanup(helpers.cleanupSessionTeams)
const {gracefulShutdown}=await import('../../../src/utils/gracefulShutdown')
await import('../../../src/utils/hooks')
if(caps.blocked.length)throw new Error('Original Teams shutdown startup boundary '+JSON.stringify(caps.blocked))
fs.writeFileSync(join(home,'shutdown-start.json'),JSON.stringify({pid:process.pid,at:Date.now()}))
await gracefulShutdown(0,'other')
throw new Error('Original real forceExit returned')
