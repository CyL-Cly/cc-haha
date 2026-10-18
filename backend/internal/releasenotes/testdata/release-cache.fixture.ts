import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import {absentManagedSettingsFixturePaths,installClientFixtureCapabilities} from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'
import {join} from 'node:path'
import {mkdir,readFile,writeFile} from 'node:fs/promises'
import {dirname} from 'node:path'
import {mock} from 'bun:test'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
const home=assertDisposableFixtureHome(input.home,'Release cache')
if(process.env.CC_HAHA_RELEASE_CACHE_FIXTURE!=='1')throw new Error('Unowned release cache fixture')
if(!input.url.startsWith('http://127.0.0.1:'))throw new Error('Loopback HTTP only')
const physicalFetch=globalThis.fetch
const capabilities=await installClientFixtureCapabilities(home,{absentPaths:[...absentManagedSettingsFixturePaths(),...(input.slash?['/home/claude/.claude/remote/.api_key','/home/claude/.claude/remote/.oauth_token',join(dirname(process.execPath),'rg'),'/usr/bin/rg','/bin/rg']:[])]})
if(input.slash)mock.module('../../../../src/utils/secureStorage/keychainPrefetch',()=>({getLegacyApiKeyPrefetchResult:()=>({stdout:''}),getLegacyApiKeyPrefetchPromise:()=>undefined,startKeychainPrefetch:()=>{},clearLegacyApiKeyPrefetch:()=>{}}))
const dir=process.env.CLAUDE_CONFIG_DIR!
await mkdir(join(dir,'cache'),{recursive:true})
if(input.cached!==null)await writeFile(join(dir,'cache/changelog.md'),input.cached)
await writeFile(join(dir,'.claude.json'),JSON.stringify({hasCompletedOnboarding:true,futureGlobal:'preserved'}))
await import('../../../../preload')
const config=await import('../../../../src/utils/config');config.enableConfigs()
const state=await import('../../../../src/bootstrap/state');state.setIsInteractive(!input.nonInteractive)
if(input.essential)process.env.CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=input.essential;else delete process.env.CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC
// Replace only Axios's physical HTTP target, retaining response data/status
// and real pending Promise semantics. All changelog/config business is Source.
const axios=(await import('axios')).default
axios.get=async ()=>{const response=await physicalFetch(input.url);let data:any=await response.text();try{data=JSON.parse(data)}catch{};if(response.status>=400)throw new Error('fixture HTTP '+response.status);return {status:response.status,data} as any}
Date.now=()=>1712345678000
const source=await import('../../../../src/utils/releaseNotes')
if(input.prime)await source.getStoredChangelog()
const started=performance.now()
const first=input.preload
 ? await source.checkForReleaseNotes(input.previousVersion,input.version)
 : await (await import('../../../../src/commands/release-notes/release-notes')).call()
const elapsed=performance.now()-started
const initialMemory=source.getStoredChangelogFromMemory()
let slashResult:any
if(input.slash){
 const {default:definition}=await import('../../../../src/commands/release-notes')
 const {getEmptyToolPermissionContext}=await import('../../../../src/Tool')
 const {processSlashCommand}=await import('../../../../src/utils/processUserInput/processSlashCommand')
 const context:any={getAppState:()=>({toolPermissionContext:getEmptyToolPermissionContext(),mcp:{commands:[]}}),options:{commands:[definition],tools:[],agents:[],agentDefinitions:{activeAgents:[]},mainLoopModel:'fixture-model'},messages:[],abortController:new AbortController()}
 const result=await processSlashCommand(input.slash,[],[],[],context,()=>{throw new Error('Unexpected JSX')})
 slashResult={messages:result.messages.map((m:any)=>{const {uuid,timestamp,...rest}=m;return rest}),shouldQuery:result.shouldQuery,resultText:result.resultText}
}
if(input.waitAfter)await new Promise(r=>setTimeout(r,input.waitAfter))
let disk:null|string=null;try{disk=await readFile(join(dir,'cache/changelog.md'),'utf8')}catch{}
if(capabilities.blocked.length)throw new Error('Blocked original release cache:'+capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({first,elapsed,initialMemory,disk,memory:source.getStoredChangelogFromMemory(),timestamp:config.getGlobalConfig().changelogLastFetched??null,future:config.getGlobalConfig().futureGlobal,slashResult})+'\n')
