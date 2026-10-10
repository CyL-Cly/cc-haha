import { readFileSync,writeFileSync,mkdirSync } from 'node:fs'
import { join,dirname } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths,installClientFixtureCapabilities } from './client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir,'Permission persistence')
if(process.env.CC_HAHA_PERMISSION_PERSISTENCE_FIXTURE!=='1')throw new Error('Missing persistence fixture')
const absent=[...absentManagedSettingsFixturePaths()];for(let d=dirname(input.homeDir);;d=dirname(d)){absent.push(join(d,'.git'));if(d===dirname(d))break}
const capabilities=await installClientFixtureCapabilities(input.homeDir,{absentPaths:absent})
await import('../../../preload')
const state=await import('../../../src/bootstrap/state');const cwd=join(input.homeDir,'project');mkdirSync(cwd,{recursive:true});state.setCwdState(cwd);state.setOriginalCwd(cwd);state.setProjectRoot(cwd);state.setIsInteractive(false)
const config=await import('../../../src/utils/config');config.enableConfigs()
const {persistPermissionUpdates}=await import('../../../src/utils/permissions/PermissionUpdate')
const {resetSettingsCache}=await import('../../../src/utils/settings/settingsCache')
const {getSettingsFilePathForSource}=await import('../../../src/utils/settings/settings')
const {permissionRuleValueFromString,permissionRuleValueToString}=await import('../../../src/utils/permissions/permissionRuleParser')
const rows=[]
for(const row of input.rows){const paths=['userSettings','projectSettings','localSettings'].map(source=>getSettingsFilePathForSource(source)!);for(let i=0;i<paths.length;i++){mkdirSync(dirname(paths[i]),{recursive:true});writeFileSync(paths[i],row.files?.[i]??'{}')};resetSettingsCache();let error:unknown;try{persistPermissionUpdates(row.updates)}catch(e){error=(e as Error).message};await new Promise(resolve=>setTimeout(resolve,0));rows.push({files:paths.map(path=>readFileSync(path,'utf8')),error})}
const normalized=(input.normalize??[]).map((rule:string)=>permissionRuleValueToString(permissionRuleValueFromString(rule)))
process.stdout.write(JSON.stringify({rows,normalized,capabilities}))
