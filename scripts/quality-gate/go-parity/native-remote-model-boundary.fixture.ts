import fs from 'node:fs'
import {join} from 'node:path'
import {mock} from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from './client-options-capabilities'
const input=await Bun.stdin.json()
const home=assertDisposableFixtureHome(input.home,'Actual internal remote model boundary')
if(Bun.version!=='1.3.14'||process.env.CC_HAHA_REMOTE_MODEL_BOUNDARY_SOURCE!=='1'||process.env.USER_TYPE!=='ant')throw new Error('Owned pinned internal boundary required')
const absentPaths=absentManagedSettingsFixturePaths()
for(const path of ['/home/claude/.claude/remote/.api_key','/home/claude/.claude/remote/.oauth_token'])if(!fs.existsSync(path))absentPaths.push(path)
const caps=await installClientFixtureCapabilities(home,{absentPaths,loopbackFetchOrigin:input.peer})
await import('../../../preload')
const secure=await import('../../../src/utils/secureStorage/index')
mock.module('../../../src/utils/secureStorage/index',()=>({...secure,getSecureStorage:()=>({read:()=>{caps.secureReads.push('owned-remote-model-credentials');return JSON.parse(fs.readFileSync(join(home,'.claude','.credentials.json'),'utf8'))}})}))
const portable=await import('../../../src/utils/execFileNoThrow')
mock.module('../../../src/utils/execFileNoThrow',()=>({...portable,execSyncWithDefaults_DEPRECATED(command:string){if(!command.startsWith('security find-generic-password '))throw new Error('Unowned Source OS primitive');caps.secureReads.push('owned-empty-legacy');return null}}))
const state=await import('../../../src/bootstrap/state');state.setCwdState(home);state.setOriginalCwd(home);state.setProjectRoot(home);state.setAllowedSettingSources(['userSettings']);state.setIsInteractive(true);state.setMainLoopModelOverride('sonnet')
const config=await import('../../../src/utils/config');config.enableConfigs()
const model=await import('../../../src/utils/model/model')
const context=await import('../../../src/utils/context')
const {queryHaiku}=await import('../../../src/services/api/claude')
const {asSystemPrompt}=await import('../../../src/utils/systemPromptType')
const {generateSessionTitle}=await import('../../../src/utils/sessionTitle')
const rows=[]
for(const [name,call]of Object.entries({sonnet:()=>model.parseUserSpecifiedModel('sonnet'),default:()=>model.getDefaultMainLoopModelSetting(),custom:()=>model.parseUserSpecifiedModel('owned-custom'),maxOutput:()=>context.getModelMaxOutputTokens('claude-haiku-4-5-20251001'),queryHaiku:()=>queryHaiku({systemPrompt:asSystemPrompt([]),userPrompt:'Actual remote title requested task',signal:new AbortController().signal,options:{querySource:'teleport_generate_title',agents:[],isNonInteractiveSession:false,hasAppendSystemPrompt:false,mcpTools:[]}}),generatedTitle:()=>generateSessionTitle('Actual remote first human',new AbortController().signal)})){
 try{rows.push({name,value:await call()})}catch(error){rows.push({name,error:{name:error?.name,message:error?.message}})}
}
if(caps.blocked.length)throw new Error('Actual model boundary escaped private capabilities '+JSON.stringify(caps.blocked))
await new Promise<void>((resolve,reject)=>process.stdout.write(JSON.stringify({rows,blocked:caps.blocked})+'\n',error=>error?reject(error):resolve()))
process.exit(0)
