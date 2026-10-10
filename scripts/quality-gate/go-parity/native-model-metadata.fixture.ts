import { mkdirSync, readFileSync, writeFileSync, rmSync } from 'node:fs'
import { join } from 'node:path'
import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir,'Model metadata')
if(process.env.CC_HAHA_MODEL_METADATA_FIXTURE!=='1')throw new Error('Missing model metadata fixture')
const capabilities=await installClientFixtureCapabilities(input.homeDir,{absentPaths:[...absentManagedSettingsFixturePaths(),'/home/claude/.claude/remote/.oauth_token','/home/claude/.claude/remote/.api_key']})
const credentials=join(input.homeDir,'.claude','.credentials.json')
// This seam is the external credential storage read, backed by real private
// fixture files. All auth and model selection business stays original Source.
mock.module('../../../src/utils/secureStorage/index',()=>({getSecureStorage:()=>({read:()=>{try{return JSON.parse(readFileSync(credentials,'utf8'))}catch{return null}},readAsync:async()=>{try{return JSON.parse(readFileSync(credentials,'utf8'))}catch{return null}}})}))
mock.module('../../../src/utils/secureStorage/keychainPrefetch',()=>({getLegacyApiKeyPrefetchResult:()=>({stdout:''}),getLegacyApiKeyPrefetchPromise:()=>undefined,startKeychainPrefetch:()=>{},clearLegacyApiKeyPrefetch:()=>{}}))
await import('../../../preload')
const state=await import('../../../src/bootstrap/state');state.setCwdState(input.homeDir);state.setOriginalCwd(input.homeDir);state.setProjectRoot(input.homeDir);state.setIsInteractive(false);state.setAllowedSettingSources(['userSettings'])
const config=await import('../../../src/utils/config');config.enableConfigs()
const settingsCache=await import('../../../src/utils/settings/settingsCache')
const auth=await import('../../../src/utils/auth')
const openai=await import('../../../src/services/openaiAuth/storage')
const model=await import('../../../src/utils/model/model')
const keys=['GROK_OAUTH_FILE','CC_HAHA_GROK_OAUTH_PROVIDER','ANTHROPIC_MODEL','ANTHROPIC_BASE_URL','ANTHROPIC_API_KEY','ANTHROPIC_AUTH_TOKEN','CLAUDE_CODE_OAUTH_TOKEN','CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR','CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR','CLAUDE_CODE_REMOTE','CLAUDE_CODE_ENTRYPOINT','CLAUDE_CODE_SIMPLE','ANTHROPIC_UNIX_SOCKET','CLAUDE_CODE_DISABLE_1M_CONTEXT','ANTHROPIC_DEFAULT_OPUS_MODEL','ANTHROPIC_DEFAULT_SONNET_MODEL','CLAUDE_CODE_USE_BEDROCK','CLAUDE_CODE_USE_VERTEX','CLAUDE_CODE_USE_FOUNDRY','CLAUDE_CODE_USE_AZURE_OPENAI']
const rows=[]
for(const row of input.cases){
 for(const key of keys)delete process.env[key];for(const [key,value]of Object.entries(row.env??{})){if(!keys.includes(key)||typeof value!=='string')throw new Error('Unsafe metadata environment');process.env[key]=value}
 if(row.grokTokens!==undefined){const file=row.env?.GROK_OAUTH_FILE;if(!file?.startsWith(input.homeDir+'/'))throw new Error('Unowned Grok model metadata file');writeFileSync(file,JSON.stringify(row.grokTokens),{mode:0o600})}else{rmSync(join(input.homeDir,'private-grok.json'),{force:true})}
 mkdirSync(join(input.homeDir,'.claude'),{recursive:true});writeFileSync(credentials,JSON.stringify(row.credentials??{}));writeFileSync(join(input.homeDir,'.claude','settings.json'),JSON.stringify(row.settings??{}))
 settingsCache.resetSettingsCache();auth.clearOAuthTokenCache();openai.clearOpenAIOAuthTokenCache();state.resetModelStringsForTestingOnly();state.setMainLoopModelOverride(undefined)
 rows.push({openaiActive:auth.isOpenAIAuthActive(),model:model.getMainLoopModel(),defaultSetting:model.getDefaultMainLoopModelSetting(),subscriber:auth.isClaudeAISubscriber(),type:auth.getSubscriptionType(),rateLimitTier:auth.getRateLimitTier(),enabled:auth.isAnthropicAuthEnabled(),merged:model.isOpus1mMergeEnabled()})
}
process.stdout.write(JSON.stringify({rows,capabilities}))
