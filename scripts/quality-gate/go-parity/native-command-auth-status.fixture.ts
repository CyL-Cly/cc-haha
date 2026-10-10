import { assertDisposableFixtureHome } from './fixture-guard'
import { mock, spyOn } from 'bun:test'
import * as fs from 'node:fs'
import { join } from 'node:path'

assertDisposableFixtureHome(process.env.HOME, 'native CLI auth status')
if (process.env.CC_HAHA_COMMAND_AUTH_FIXTURE !== '1') throw new Error('Unowned CLI auth fixture')
const rows = JSON.parse(await new Response(Bun.stdin.stream()).text())
let current: any
const config = await import('../../../src/utils/config')
const settings = await import('../../../src/utils/settings/settings')
const secure = await import('../../../src/utils/secureStorage/index')
const portable = await import('../../../src/utils/execFileNoThrow')
const fsOperations = await import('../../../src/utils/fsOperations')
const nativeFS = fsOperations.getFsImplementation()
const mkdir = fs.mkdirSync.bind(fs), writeFile = fs.writeFileSync.bind(fs)
const mapped = (path: unknown): unknown => {
  const text = String(path)
  if (text.startsWith('/home/claude/.claude/remote')) return join(current.fdRoot, text.slice('/home/claude/.claude/remote'.length))
  if (text.startsWith('/dev/fd/') || text.startsWith('/proc/self/fd/')) return join(current.fdRoot, 'descriptor-' + text.split('/').at(-1))
  return path
}
mock.module('../../../src/utils/fsOperations', () => ({ ...fsOperations, getFsImplementation: () => ({ ...nativeFS, readFileSync: (path: any, options: any) => nativeFS.readFileSync(mapped(path) as any, options) }) }))
spyOn(fs, 'mkdirSync').mockImplementation(((path: any, options: any) => mkdir(mapped(path) as any, options)) as any)
spyOn(fs, 'writeFileSync').mockImplementation(((path: any, data: any, options: any) => writeFile(mapped(path) as any, data, options)) as any)
mock.module('../../../src/utils/config', () => ({ ...config, getGlobalConfig: () => ({ customApiKeyResponses: { approved: [], rejected: [] }, ...(current.global ?? {}) }) }))
mock.module('../../../src/utils/settings/settings', () => ({ ...settings, getSettings_DEPRECATED: () => current.settings ?? {}, getSettingsForSource: (name: string) => name === 'flagSettings' ? current.flagSettings ?? null : null }))
mock.module('../../../src/utils/secureStorage/index', () => ({ ...secure, getSecureStorage: () => ({ read: () => current.secure ?? null }) }))
mock.module('../../../src/utils/execFileNoThrow', () => ({ ...portable, execSyncWithDefaults_DEPRECATED: () => current.legacy ?? null }))
const bootstrap = await import('../../../src/bootstrap/state')
const auth = await import('../../../src/utils/auth')
const openAIStorage = await import('../../../src/services/openaiAuth/storage')
const mtls = await import('../../../src/utils/mtls')
const handler = await import('../../../src/cli/handlers/auth')
const names = ['CLAUDE_CODE_SIMPLE', 'ANTHROPIC_UNIX_SOCKET', 'CLAUDE_CODE_OAUTH_TOKEN', 'CLAUDE_CODE_USE_BEDROCK', 'CLAUDE_CODE_USE_VERTEX', 'CLAUDE_CODE_USE_FOUNDRY', 'CLAUDE_CODE_USE_AZURE_OPENAI', 'ANTHROPIC_AUTH_TOKEN', 'ANTHROPIC_API_KEY', 'CLAUDE_CODE_REMOTE', 'CLAUDE_CODE_ENTRYPOINT', 'CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR', 'CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR', 'CI', 'NODE_ENV', 'USER_TYPE', 'COO_RUNNING_ON_HOMESPACE', 'IS_DEMO', 'AWS_REGION', 'AWS_DEFAULT_REGION', 'BEDROCK_BASE_URL', 'VERTEX_BASE_URL', 'CLOUD_ML_REGION', 'ANTHROPIC_VERTEX_PROJECT_ID', 'ANTHROPIC_FOUNDRY_RESOURCE', 'ANTHROPIC_FOUNDRY_BASE_URL', 'CLAUDE_CODE_SKIP_BEDROCK_AUTH', 'CLAUDE_CODE_SKIP_VERTEX_AUTH', 'CLAUDE_CODE_SKIP_FOUNDRY_AUTH', 'https_proxy', 'HTTPS_PROXY', 'http_proxy', 'HTTP_PROXY', 'NODE_EXTRA_CA_CERTS', 'ANTHROPIC_BASE_URL', 'CLAUDE_CODE_CLIENT_CERT', 'CLAUDE_CODE_CLIENT_KEY', 'CLAUDE_CODE_CLIENT_KEY_PASSPHRASE']
let stdout = '', stderr = ''
const write = process.stdout.write.bind(process.stdout)
class HandlerExit { constructor(readonly code: number) {} }
spyOn(process.stdout, 'write').mockImplementation(((chunk: any) => { stdout += Buffer.from(chunk).toString('utf8'); return true }) as any)
spyOn(process.stderr, 'write').mockImplementation(((chunk: any) => { stderr += Buffer.from(chunk).toString('utf8'); return true }) as any)
spyOn(process, 'exit').mockImplementation(((code: number) => { throw new HandlerExit(code) }) as any)
const output = []
for (const row of rows) {
  current = row
  current.fdRoot = join(process.env.HOME!, 'fd-' + output.length)
  mkdir(current.fdRoot, { recursive: true, mode: 0o700 })
  for (const [name, data] of Object.entries(row.credentialFiles ?? {})) writeFile(join(current.fdRoot, name), String(data), { mode: 0o600 })
  for (const name of names) delete process.env[name]
  for (const [name, value] of Object.entries(row.env ?? {})) process.env[name] = String(value)
  bootstrap.setIsInteractive(!row.nonInteractive)
  bootstrap.setClientType('cli')
  bootstrap.setApiKeyFromFd(undefined)
  bootstrap.setOauthTokenFromFd(undefined)
  auth.getClaudeAIOAuthTokens.cache.clear()
  auth.getApiKeyFromConfigOrMacOSKeychain.cache.clear()
  openAIStorage.clearOpenAIOAuthTokenCache()
  mtls.getMTLSConfig.cache.clear()
  stdout = ''; stderr = ''
  let exitCode: number | null = null, failure: unknown
  try { await handler.authStatus({ text: !!row.text, openai: !!row.openai }) }
  catch (error) {
    if (error instanceof HandlerExit) exitCode = error.code
    else failure = { name: (error as Error).name, message: (error as Error).message }
  }
  const persisted = Object.fromEntries(['.oauth_token', '.api_key'].filter(name => fs.existsSync(join(current.fdRoot, name))).map(name => [name, fs.readFileSync(join(current.fdRoot, name), 'utf8')]))
  output.push({ stdout, stderr, exitCode, persisted, ...(failure ? { failure } : {}) })
}
write(JSON.stringify(output))
