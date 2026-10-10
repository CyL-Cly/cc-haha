import {mock} from 'bun:test'
import {join,dirname} from 'node:path'
import {mkdir,writeFile,realpath} from 'node:fs/promises'
import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import {absentManagedSettingsFixturePaths,installClientFixtureCapabilities} from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
const home=assertDisposableFixtureHome(input.home,'Goal command')
if(process.env.CC_HAHA_GOAL_COMMAND_FIXTURE!=='1')throw new Error('Unowned goal command fixture')
const absent=[...absentManagedSettingsFixturePaths(),'/home/claude/.claude/remote/.api_key','/home/claude/.claude/remote/.oauth_token',join(dirname(process.execPath),'rg'),'/usr/bin/rg','/bin/rg']
for(const base of new Set([home,await realpath(home)]))for(let p=base;;p=dirname(p)){absent.push(join(p,'.git'));if(dirname(p)===p)break}
const capabilities=await installClientFixtureCapabilities(home,{absentPaths:absent})
mock.module('../../../../src/utils/secureStorage/keychainPrefetch',()=>({getLegacyApiKeyPrefetchResult:()=>({stdout:''}),getLegacyApiKeyPrefetchPromise:()=>undefined,startKeychainPrefetch:()=>{},clearLegacyApiKeyPrefetch:()=>{}}))
const dir=process.env.CLAUDE_CONFIG_DIR!
await mkdir(dir,{recursive:true});await writeFile(join(dir,'settings.json'),JSON.stringify(input.settings??{}));await writeFile(join(dir,'.claude.json'),JSON.stringify({hasCompletedOnboarding:true,projects:{[home]:{hasTrustDialogAccepted:input.trusted}}}))
await import('../../../../preload')
const state=await import('../../../../src/bootstrap/state');state.setIsInteractive(!!input.interactive);state.setCwdState(home);state.setOriginalCwd(home);state.setProjectRoot(home);state.switchSession('goal-command-source')
const config=await import('../../../../src/utils/config');config.enableConfigs()
if(input.builtinNames){
 const {builtInCommandNames}=await import('../../../../src/commands')
 const names=[...builtInCommandNames()]
 if(capabilities.blocked.length)throw new Error('Blocked original builtin names:'+capabilities.blocked.join(', '))
 process.stdout.write(JSON.stringify({names})+'\n');process.exit(0)
}
if(input.firstPrompts){
 const {parse}=await import('acorn')
 const storage=await import('../../../../src/utils/sessionStorage')
 const code=new Bun.Transpiler({loader:'ts'}).transformSync(await Bun.file(new URL('../../../../src/utils/sessionStorage.ts',import.meta.url)).text()),ast:any=parse(code,{ecmaVersion:'latest',sourceType:'module'})
 const node=ast.body.find((n:any)=>n.type==='FunctionDeclaration'&&n.id.name==='extractFirstPrompt')
 if(!node)throw new Error('Original first prompt function missing')
 const original=new Function('getFirstMeaningfulUserMessageTextContent',code.slice(node.start,node.end)+'\nreturn extractFirstPrompt')(storage.getFirstMeaningfulUserMessageTextContent)
 const values=input.firstPrompts.map((messages:any)=>{try{return {value:original(messages),error:null}}catch(e:any){return {value:null,error:e.name}}})
 if(capabilities.blocked.length)throw new Error('Blocked original first prompt:'+capabilities.blocked.join(', '))
 process.stdout.write(JSON.stringify({values})+'\n');process.exit(0)
}
const {default:definition}=await import('../../../../src/commands/goal')
const {getEmptyToolPermissionContext}=await import('../../../../src/Tool')
const {processSlashCommand}=await import('../../../../src/utils/processUserInput/processSlashCommand')
const app:any={toolPermissionContext:getEmptyToolPermissionContext(),mcp:{commands:[]},sessionHooks:new Map()}
const context:any={getAppState:()=>app,setAppState:(updater:any)=>Object.assign(app,updater(app)),options:{commands:[definition],tools:[],agents:[],agentDefinitions:{activeAgents:[]},mainLoopModel:'fixture-model',isNonInteractiveSession:!input.interactive},messages:[],abortController:new AbortController()}
const rows=[]
for(const args of input.arguments){
 const result=await processSlashCommand('/goal'+(args?' '+args:''),[],[],[],context,()=>{throw new Error('Goal returns no JSX')})
 context.messages.push(...result.messages)
 rows.push({messages:result.messages.map((m:any)=>{const {uuid,timestamp,...rest}=m;return rest}),shouldQuery:result.shouldQuery})
}
if(capabilities.blocked.length)throw new Error('Blocked Source Goal command:'+capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({rows})+'\n')
