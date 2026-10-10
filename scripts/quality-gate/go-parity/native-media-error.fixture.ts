import { spyOn, mock } from 'bun:test'
import * as crypto from 'node:crypto'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = await Bun.stdin.json()
const home=assertDisposableFixtureHome(input.homeDir,'Native media error Source')
if(process.env.CC_HAHA_NATIVE_MEDIA_ERROR_FIXTURE!=='1')throw new Error('Unowned media error fixture')
const capabilities=await installClientFixtureCapabilities(home,{absentPaths:[...absentManagedSettingsFixturePaths(),'/home/claude/.claude/remote/.api_key','/home/claude/.claude/remote/.oauth_token']})
// The external legacy keychain observation is empty. No security process runs;
// original auth and error functions remain the actual Source implementations.
mock.module(new URL('../../../src/utils/secureStorage/keychainPrefetch.ts',import.meta.url).pathname,()=>({getLegacyApiKeyPrefetchResult:()=>({stdout:null}),clearLegacyApiKeyPrefetch:()=>{},ensureKeychainPrefetchCompleted:async()=>{},startKeychainPrefetch:()=>{}}))
;(globalThis as any).MACRO={BUILD_TIME:'',FEEDBACK_CHANNEL:'private'}
const state=await import('../../../src/bootstrap/state')
state.setCwdState(home);state.setOriginalCwd(home);state.setProjectRoot(home)
const config=await import('../../../src/utils/config');config.enableConfigs()
config.saveGlobalConfig(previous=>({...previous,customApiKeyResponses:{approved:[process.env.ANTHROPIC_API_KEY!.slice(-20)],rejected:[]}}))
const sdk=await import('@anthropic-ai/sdk')
const {getAssistantMessageFromError}=await import('../../../src/services/api/errors')
const {ImageResizeError}=await import('../../../src/utils/imageResizer')
const {ImageSizeError}=await import('../../../src/utils/imageValidation')
let allocations:string[]=[];let calls=0
spyOn(crypto,'randomUUID').mockImplementation(()=>{allocations.push('uuid:'+ ++calls);return 'media-id-'+calls as any})
const RealDate=Date
globalThis.Date=class extends RealDate {constructor(...args:any[]){if(!args.length)allocations.push('date');super(...(args.length?args:[1700000000000]) as [number])}static now(){return 1700000000000}} as any
const rows=[]
for(const row of input.rows){
 state.setIsInteractive(!row.nonInteractive)
 let error:Error
 if(row.kind==='api')error=new sdk.APIError(row.status,row.payload,row.message,undefined)
 else if(row.kind==='resize')error=new ImageResizeError(row.message)
 else if(row.kind==='size')error=new ImageSizeError([{index:1,size:5242881}],5242880)
 else if(row.kind==='connection')error=new sdk.APIConnectionError({message:row.message})
 else error=new Error(row.message)
 allocations=[];calls=0
 const value=getAssistantMessageFromError(error,row.model,{messagesForAPI:row.history})
 rows.push({message:error.message,value:JSON.stringify(value),allocations:[...allocations]})
}
if(capabilities.blocked.length)throw new Error('Blocked media Source '+capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({rows,capabilities}))
