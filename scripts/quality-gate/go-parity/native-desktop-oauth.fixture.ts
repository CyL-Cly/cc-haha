import { assertDisposableFixtureHome } from './fixture-guard'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'

const home = process.env.HOME!
assertDisposableFixtureHome(home, 'Native desktop OAuth')
if (process.env.CC_HAHA_DESKTOP_OAUTH_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Desktop OAuth fixture requires isolated production configuration')
// Only the secure-storage OS primitive is selected as plaintext. No actual
// Keychain command can run in this Source child; all credentials are fixtures.
Object.defineProperty(process, 'platform', { value: 'linux' })
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
let clock = input.clock
const OriginalDate = Date
globalThis.Date = class extends OriginalDate {
  constructor(value?: string | number) { super(value === undefined ? clock : value) }
  static now() { return clock }
} as DateConstructor
await import('../../../preload')
// Preserve actual file I/O while redirecting the original CCR constant into
// this fixture's disposable root. The original reader and caches still run.
const fsOperations = await import('../../../src/utils/fsOperations')
const sourceFs = fsOperations.getFsImplementation()
fsOperations.setFsImplementation({
  ...sourceFs,
  readFileSync(path, options) {
    if (path.startsWith('/home/claude/.claude/remote/')) path = join(home, 'remote', path.split('/').at(-1)!)
    if (input.cliAuth && path.startsWith('/proc/self/fd/')) path = join(home, 'descriptor-input')
    return sourceFs.readFileSync(path, options)
  },
})
const { enableConfigs } = await import('../../../src/utils/config')
enableConfigs()
const { HahaOAuthService } = await import('../../../src/server/services/hahaOAuthService')
const { normalizeExplicitClaudeOfficialModelId, getClaudeOfficialDefaultModelId } = await import('../../../src/server/services/claudeOfficialRuntime')
const { fetchProfileInfo, parseScopes, buildAuthUrl } = await import('../../../src/services/oauth/client')
const { getOauthConfig } = await import('../../../src/constants/oauth')
const { generateCodeChallenge } = await import('../../../src/services/oauth/crypto')
const service = new HahaOAuthService()
if (input.cliAuth) {
  const request = input.cliAuth
  if (request.fallback !== undefined) {
    await mkdir(join(home, 'remote'), { recursive: true })
    await writeFile(join(home, 'remote', '.oauth_token'), request.fallback, { mode: 0o600 })
  }
  if (request.descriptor !== undefined) await writeFile(join(home, 'descriptor-input'), request.descriptor, { mode: 0o600 })
  if (request.fd !== undefined) process.env.CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR = request.fd
  if (request.envToken !== undefined) process.env.CLAUDE_CODE_OAUTH_TOKEN = request.envToken
  if (request.bare) process.env.CLAUDE_CODE_SIMPLE = '1'
  if (request.credentials) await writeFile(join(process.env.CLAUDE_CONFIG_DIR!, '.credentials.json'), JSON.stringify(request.credentials), { mode: 0o600 })
  const { getClaudeAIOAuthTokens, clearOAuthTokenCache } = await import('../../../src/utils/auth')
  const values = [getClaudeAIOAuthTokens()]
  process.env.CLAUDE_CODE_OAUTH_TOKEN = 'fake-changed'
  values.push(getClaudeAIOAuthTokens())
  clearOAuthTokenCache()
  values.push(getClaudeAIOAuthTokens())
  process.stdout.write(JSON.stringify(values))
  process.exit(0)
}
if (input.providerModels) {
  const { handleProvidersApi } = await import('../../../src/server/api/providers')
  const { setGrokRuntimeModelCatalog, GROK_MODEL_CATALOG } = await import('../../../src/services/grokAuth/models')
  const config = process.env.CLAUDE_CONFIG_DIR!
  const values = []
  async function disk(path: string) {
    try { return JSON.parse(await readFile(path, 'utf8')) } catch { return null }
  }
  for (const item of input.providerModels) {
    setGrokRuntimeModelCatalog(item.catalog ?? GROK_MODEL_CATALOG)
    for (const [field, path] of [['index', join(config, 'cc-haha', 'providers.json')], ['user', join(config, 'settings.json')], ['managed', join(config, 'cc-haha', 'settings.json')]] as const) {
      if (Object.hasOwn(item, field)) {
        await mkdir(join(path, '..'), { recursive: true })
        await writeFile(path, JSON.stringify(item[field], null, 2) + '\n')
      }
    }
    const url = new URL(`http://localhost:3456${item.path}`)
    const req = new Request(url, { method: item.method, ...(item.body === undefined ? {} : { body: item.body }) })
    const response = await handleProvidersApi(req, url, url.pathname.split('/').filter(Boolean))
    values.push({ status: response.status, body: await response.json(), index: await disk(join(config, 'cc-haha', 'providers.json')), user: await disk(join(config, 'settings.json')), managed: await disk(join(config, 'cc-haha', 'settings.json')) })
  }
  process.stdout.write(JSON.stringify(values))
  process.exit(0)
}
if (input.api) {
  const { hahaOAuthService } = await import('../../../src/server/services/hahaOAuthService')
  const { handleHahaOAuthApi, handleHahaOAuthCallback } = await import('../../../src/server/api/haha-oauth')
  const responses = []
  for (const item of input.api) {
    if (item.tokens) await hahaOAuthService.saveTokens(item.tokens)
    const url = new URL(`http://localhost:3456${item.path}`)
    const req = new Request(url.toString(), { method: item.method, ...(item.body === undefined ? {} : { body: item.body }) })
    const response = item.path.startsWith('/callback') ? await handleHahaOAuthCallback(url) : await handleHahaOAuthApi(req, url, url.pathname.split('/').filter(Boolean))
    const text = await response.text()
    responses.push({ status: response.status, html: response.headers.get('content-type')?.startsWith('text/html') ?? false, body: response.headers.get('content-type')?.includes('json') ? JSON.parse(text) : text })
  }
  process.stdout.write(JSON.stringify(responses))
  process.exit(0)
}
let session: ReturnType<typeof service.startSession> | null = null
const values: unknown[] = []
for (const action of input.actions) {
  try {
    switch (action.kind) {
      case 'start': {
        session = service.startSession({ serverPort: action.port })
        const url = new URL(session.authorizeUrl)
        const validChallenge = url.searchParams.get('code_challenge') === generateCodeChallenge(session.codeVerifier)
        url.searchParams.set('code_challenge', 'CHALLENGE')
        url.searchParams.set('state', 'STATE')
        values.push({ start: { authorizeUrl: url.toString(), serverPort: session.serverPort, createdAt: session.createdAt, stateLength: session.state.length, verifierLength: session.codeVerifier.length, validChallenge } })
        break
      }
      case 'advance': clock += action.ms; values.push({ clock }); break
      case 'session': values.push({ exists: !!service.getSession(session?.state ?? 'missing') }); break
      case 'consume': values.push({ exists: !!service.consumeSession(session?.state ?? 'missing') }); break
      case 'complete': values.push({ tokens: await service.completeSession(action.code ?? 'fake-code', action.unknown ? 'unknown-state' : session?.state ?? 'missing') }); break
      case 'save': await service.saveTokens(action.tokens); values.push({ saved: true }); break
      case 'load': values.push({ tokens: await service.loadTokens() }); break
      case 'ensure': values.push({ tokens: await service.ensureFreshTokens() }); break
      case 'parallel': values.push({ tokens: await Promise.all(Array.from({ length: 12 }, () => service.ensureFreshTokens())) }); break
      case 'unauthorized': values.push({ tokens: await service.recoverFromUnauthorized(action.token) }); break
      case 'delete': await service.deleteTokens(); values.push({ deleted: true }); break
      case 'profile': values.push({ profile: await fetchProfileInfo('fake-profile-token') }); break
    }
  } catch (error) { values.push({ error: (error as Error).message }) }
}
let disk: string | null = null
try { disk = await readFile(join(process.env.CLAUDE_CONFIG_DIR!, 'cc-haha', 'oauth.json'), 'utf8') } catch {}
const models = input.models.map((value: string) => normalizeExplicitClaudeOfficialModelId(value))
const defaults = input.subscriptions.map((value: string | null) => getClaudeOfficialDefaultModelId(value as any))
const scopes = input.scopes.map((value: string | undefined) => parseScopes(value))
const authUrls = input.auth.map((value: any) => buildAuthUrl({ codeChallenge: value.challenge, state: value.state, port: value.port, isManual: false, loginWithClaudeAi: true }))
process.stdout.write(JSON.stringify({ values, disk, models, defaults, scopes, authUrls, config: getOauthConfig().CLIENT_ID }))
