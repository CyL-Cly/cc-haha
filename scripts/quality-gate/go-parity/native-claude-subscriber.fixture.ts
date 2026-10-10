import { assertDisposableFixtureHome } from './fixture-guard'
import { mock } from 'bun:test'
assertDisposableFixtureHome(process.env.HOME, 'Claude subscriber predicate')
if (process.env.CC_HAHA_CLAUDE_SUBSCRIBER_FIXTURE !== '1') throw new Error('Unowned subscriber fixture')
const rows = JSON.parse(await new Response(Bun.stdin.stream()).text())
let current: any
let reads = 0
const config = await import('../../../src/utils/config')
const settings = await import('../../../src/utils/settings/settings')
const secure = await import('../../../src/utils/secureStorage/index')
const portable = await import('../../../src/utils/execFileNoThrow')
const descriptors = await import('../../../src/utils/authFileDescriptor')
mock.module('../../../src/utils/config', () => ({ ...config, getGlobalConfig: () => ({ customApiKeyResponses: { approved: [], rejected: [] }, ...(current.global ?? {}) }) }))
mock.module('../../../src/utils/settings/settings', () => ({ ...settings, getSettings_DEPRECATED: () => current.settings ?? {} }))
mock.module('../../../src/utils/secureStorage/index', () => ({ ...secure, getSecureStorage: () => ({ read: () => { reads++; return current.secure ?? null } }) }))
mock.module('../../../src/utils/execFileNoThrow', () => ({ ...portable, execSyncWithDefaults_DEPRECATED: () => current.legacy ?? null }))
mock.module('../../../src/utils/authFileDescriptor', () => ({ ...descriptors, getApiKeyFromFileDescriptor: () => null, getOAuthTokenFromFileDescriptor: () => null }))
const bootstrap = await import('../../../src/bootstrap/state')
const auth = await import('../../../src/utils/auth')
const openAIStorage = await import('../../../src/services/openaiAuth/storage')
const names = ['CLAUDE_CODE_SIMPLE', 'ANTHROPIC_UNIX_SOCKET', 'CLAUDE_CODE_OAUTH_TOKEN', 'CLAUDE_CODE_USE_BEDROCK', 'CLAUDE_CODE_USE_VERTEX', 'CLAUDE_CODE_USE_FOUNDRY', 'CLAUDE_CODE_USE_AZURE_OPENAI', 'ANTHROPIC_AUTH_TOKEN', 'ANTHROPIC_API_KEY', 'CLAUDE_CODE_REMOTE', 'CLAUDE_CODE_ENTRYPOINT', 'CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR', 'CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR', 'CI', 'NODE_ENV', 'USER_TYPE']
const output = []
for (const row of rows) {
  current = row
  reads = 0
  for (const name of names) delete process.env[name]
  for (const [name, value] of Object.entries(row.env ?? {})) process.env[name] = String(value)
  bootstrap.setIsInteractive(!row.preferThirdParty)
  bootstrap.setClientType('cli')
  auth.getClaudeAIOAuthTokens.cache.clear()
  auth.getApiKeyFromConfigOrMacOSKeychain.cache.clear()
  openAIStorage.clearOpenAIOAuthTokenCache()
  try { output.push(row.apiKeySource ? { source: auth.getAnthropicApiKeyWithSource().source, reads } : row.modelActive ? { active: auth.isOpenAIAuthActive(), reads } : { enabled: auth.isAnthropicAuthEnabled(), subscriber: auth.isClaudeAISubscriber(), reads }) }
  catch (error) { output.push({ error: { name: (error as Error).name, message: (error as Error).message }, reads }) }
}
process.stdout.write(JSON.stringify(output))
