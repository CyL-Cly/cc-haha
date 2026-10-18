import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import {absentManagedSettingsFixturePaths,installClientFixtureCapabilities} from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'
import {mkdir,writeFile} from 'node:fs/promises'
import {join,dirname} from 'node:path'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
const home=assertDisposableFixtureHome(input.home,'Session slash commands')
if(process.env.CC_HAHA_SLASH_COMMANDS_FIXTURE!=='1')throw new Error('Unowned slash commands fixture')
const ancestors:string[]=[]
for(let current=input.cwd??home;;current=join(current,'..')){ancestors.push(join(current,'.git'));if(current===join(current,'..'))break}
// The external Source build cannot override its managed root. Model only the
// exact empty collection directories as absent, matching the Go caller's
// empty managed fixture without reading an OS-owned directory.
const managedRoot=dirname(absentManagedSettingsFixturePaths()[0]!)
const capabilities=await installClientFixtureCapabilities(home,{absentPaths:[...absentManagedSettingsFixturePaths(),join(managedRoot,'.claude','skills'),join(managedRoot,'.claude','commands'),join(dirname(process.execPath),'rg'),...(process.env.PATH??'').split(':').map(path=>join(path,'rg')),...ancestors.filter(path=>!path.startsWith(home+'/'))]})
await import('../../../../preload')
await mkdir(join(home,'.claude'),{recursive:true});await writeFile(join(home,'.claude/settings.json'),JSON.stringify(input.settings??{}))
const state=await import('../../../../src/bootstrap/state');state.setCwdState(input.cwd??home);state.setOriginalCwd(input.cwd??home);state.setProjectRoot(input.projectRoot??input.cwd??home)
const config=await import('../../../../src/utils/config');config.enableConfigs()
if(input.list||input.merge){
 const {mock}=await import('bun:test')
 if(input.compiledFailure){
  const commands=await import('../../../../src/commands')
  mock.module('../../../../src/commands',()=>({...commands,getCompiledInCommands(){throw new Error('fixture compiled catalog failure')}}))
 }
 const loader=await import('../../../../src/utils/plugins/pluginLoader')
 mock.module('../../../../src/utils/plugins/pluginLoader',()=>({...loader,clearPluginCache(){},loadAllPluginsCacheOnly:async()=>({enabled:input.plugins??[],disabled:[],errors:[]}),loadAllPlugins:async()=>({enabled:input.plugins??[],disabled:[],errors:[]})}))
 const {listSkillSlashCommands}=await import('../../../../src/server/api/skills')
 if(input.merge){
  // Isolate only the transcript workdir lookup. The actual request router,
  // cache normalization, merge and file/compiled collectors run unchanged.
  const original=await import('../../../../src/server/services/sessionService')
  let missing=Boolean(input.missing)
  mock.module('../../../../src/server/services/sessionService',()=>({...original,sessionService:{getSessionWorkDir:async()=>missing?null:input.cwd}}))
  const {updateSessionSlashCommands}=await import('../../../../src/server/ws/handler')
  const {handleSessionsApi}=await import('../../../../src/server/api/sessions')
  const results=[]
  for(const scenario of input.scenarios??[input]){
   missing=Boolean(scenario.missing)
   updateSessionSlashCommands('source-private-session',scenario.preferred??[],{notifyClient:false})
   const url=new URL('http://fixture.local/api/sessions/source-private-session/slash-commands')
   const response=await handleSessionsApi(new Request(url,{method:scenario.method??'GET'}),url,url.pathname.split('/').filter(Boolean))
   results.push({status:response.status,body:await response.json()})
  }
  if(capabilities.blocked.length)throw new Error('Blocked slash fixture:'+capabilities.blocked.join(', '))
  process.stdout.write(JSON.stringify(input.scenarios?{results}:results[0])+'\n');process.exit(0)
 }
 const commands=await listSkillSlashCommands(input.cwd,{includeCompiledIn:input.includeCompiled})
 if(capabilities.blocked.length)throw new Error('Blocked slash fixture:'+capabilities.blocked.join(', '))
 process.stdout.write(JSON.stringify({commands})+'\n');process.exit(0)
}
const {getCompiledInCommands}=await import('../../../../src/commands')
const {supportsHeadlessSlashCommand}=await import('../../../../src/commands/headless')
const commands=getCompiledInCommands().filter(command=>command.userInvocable!==false&&supportsHeadlessSlashCommand(command)).map(command=>({name:command.name,description:command.description||'',...(command.argumentHint?{argumentHint:command.argumentHint}:{})}))
if(capabilities.blocked.length)throw new Error('Blocked slash fixture:'+capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({commands})+'\n')
