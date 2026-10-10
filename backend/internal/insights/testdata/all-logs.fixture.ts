import {mock} from 'bun:test'
import {isAbsolute,relative,join,dirname} from 'node:path'
import {lstat,realpath} from 'node:fs/promises'
import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import {absentManagedSettingsFixturePaths,installClientFixtureCapabilities} from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
const home=assertDisposableFixtureHome(input.home,'Insights all branch logs')
if(process.env.CC_HAHA_INSIGHTS_ALL_LOGS_FIXTURE!=='1')throw new Error('Unowned all logs fixture')
const canonical=await realpath(home),path=await realpath(input.path),child=relative(canonical,path)
if(isAbsolute(child)||child==='..'||child.startsWith('../')||!(await lstat(path)).isFile())throw new Error('Ordinary private transcript required')
const absent=[...absentManagedSettingsFixturePaths(),'/home/claude/.claude/remote/.api_key','/home/claude/.claude/remote/.oauth_token',join(dirname(process.execPath),'rg'),'/usr/bin/rg','/bin/rg']
const capabilities=await installClientFixtureCapabilities(home,{absentPaths:absent})
mock.module('../../../../src/utils/secureStorage/keychainPrefetch',()=>({getLegacyApiKeyPrefetchResult:()=>({stdout:''}),getLegacyApiKeyPrefetchPromise:()=>undefined,startKeychainPrefetch:()=>{},clearLegacyApiKeyPrefetch:()=>{}}))
await import('../../../../preload')
const state=await import('../../../../src/bootstrap/state');state.setIsInteractive(false);state.setCwdState(home);state.setOriginalCwd(home)
const config=await import('../../../../src/utils/config');config.enableConfigs()
const {loadAllLogsFromSessionFile}=await import('../../../../src/utils/sessionStorage')
const logs=await loadAllLogsFromSessionFile(path,input.project)
if(capabilities.blocked.length)throw new Error('Blocked all session logs:'+capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({logs:logs.map(log=>({messages:log.messages,sessionId:log.sessionId,leafUuid:log.leafUuid,projectPath:log.projectPath,firstPrompt:log.firstPrompt,summary:log.summary,createdMillis:log.created.getTime(),modifiedMillis:log.modified.getTime()}))})+'\n')
