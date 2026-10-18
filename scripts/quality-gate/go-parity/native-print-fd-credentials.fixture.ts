import fs from 'node:fs'
import { join } from 'node:path'
import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const home = assertDisposableFixtureHome(process.env.HOME, 'Native print FD credentials')
if (process.env.CC_HAHA_PRINT_FD_FIXTURE !== '1') throw new Error('Unowned print FD fixture')
const rows = JSON.parse(await new Response(Bun.stdin.stream()).text())
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
const fsOperations = await import('../../../src/utils/fsOperations')
const portable = await import('../../../src/utils/execFileNoThrow')
const originalFS = fsOperations.getFsImplementation()
let directory = '', reads: Record<string, number> = {}
mock.module('../../../src/utils/fsOperations', () => ({ ...fsOperations, getFsImplementation: () => ({ ...originalFS, readFileSync: (value: any, options: any) => {
  const path = String(value)
  let mapped = value
  if (path.startsWith('/dev/fd/') || path.startsWith('/proc/self/fd/')) {
    reads[path.split('/').at(-1)!] = (reads[path.split('/').at(-1)!] ?? 0) + 1
    mapped = join(directory, 'descriptor-' + path.split('/').at(-1))
  } else if (path.startsWith('/home/claude/.claude/remote/')) mapped = join(directory, path.split('/').at(-1)!)
  return originalFS.readFileSync(mapped, options)
} }) }))
// Only the external legacy Keychain primitive is absent. Auth selection,
// descriptor parsing/cache and metadata functions remain original Source.
mock.module('../../../src/utils/execFileNoThrow', () => ({ ...portable, execSyncWithDefaults_DEPRECATED: () => null }))
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setOriginalCwd(home)
bootstrap.setCwdState(home)
bootstrap.setProjectRoot(home)
bootstrap.setClientType('cli')
const config = await import('../../../src/utils/config')
config.enableConfigs()
const auth = await import('../../../src/utils/auth')
const names = ['ANTHROPIC_API_KEY', 'ANTHROPIC_AUTH_TOKEN', 'CLAUDE_CODE_OAUTH_TOKEN', 'CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR', 'CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR', 'CLAUDE_CODE_SIMPLE', 'CI']
const result = []
for (const [index, row] of rows.entries()) {
  for (const name of names) delete process.env[name]
  Object.assign(process.env, row.env)
  bootstrap.setIsInteractive(!!row.interactive)
  bootstrap.setApiKeyFromFd(undefined)
  bootstrap.setOauthTokenFromFd(undefined)
  auth.getClaudeAIOAuthTokens.cache.clear()
  auth.getApiKeyFromConfigOrMacOSKeychain.cache.clear()
  directory = join(home, 'fd-' + index)
  fs.mkdirSync(directory, { mode: 0o700 })
  for (const [name, value] of Object.entries(row.files ?? {})) fs.writeFileSync(join(directory, name), String(value), { mode: 0o600 })
  reads = {}
  const snapshot = () => {
    const api = auth.getAnthropicApiKeyWithSource({ skipRetrievingKeyFromApiKeyHelper: true })
    const tokens = auth.getClaudeAIOAuthTokens()
    const subscriber = auth.isClaudeAISubscriber()
    const account = auth.getAccountInformation()
    return { key: api.key ?? '', source: api.source, tokens, subscriber, tokenSource: account?.tokenSource ?? '', accountAPIKeySource: account?.apiKeySource ?? '' }
  }
  const before = snapshot()
  for (const name of ['descriptor-3', 'descriptor-4', '.api_key', '.oauth_token']) fs.writeFileSync(join(directory, name), 'fake-later-must-not-be-adopted', { mode: 0o600 })
  auth.getClaudeAIOAuthTokens.cache.clear()
  const after = snapshot()
  result.push({ before, after, descriptorReads: reads })
}
if (capabilities.blocked.length !== 0) throw new Error('Print FD fixture crossed capability boundary: ' + JSON.stringify(capabilities.blocked))
process.stdout.write(JSON.stringify(result))
