import { assertDisposableFixtureHome } from './fixture-guard'
import { mock } from 'bun:test'
import { mkdirSync, writeFileSync, readFileSync } from 'node:fs'
import { join } from 'node:path'

const home = assertDisposableFixtureHome(process.env.HOME, 'Native MCP OAuth')
if (process.env.CC_HAHA_NATIVE_MCP_AUTH_FIXTURE !== '1') throw new Error('Missing isolated MCP OAuth marker')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
if (input.settings) process.env.CLAUDE_CODE_ENABLE_XAA = '1'
let now = input.now
const RealDate = Date
globalThis.Date = class extends RealDate {
  constructor(value?: string | number) { super(value === undefined ? now : value) }
  static now() { return now }
} as DateConstructor
// Only security(1) is isolated. The actual Source fallback storage, auth
// provider, SDK and filesystem business run over the owned credential file.
const nativePortable = await import('../../../src/utils/execFileNoThrowPortable')
mock.module('../../../src/utils/execFileNoThrowPortable', () => ({
  ...nativePortable,
  execSyncWithDefaults_DEPRECATED(command: string) {
    if (command.startsWith('security ')) throw new Error('isolated keychain unavailable')
    throw new Error('Undeclared native subprocess')
  },
}))
const nativeExec = await import('../../../src/utils/execFileNoThrow')
mock.module('../../../src/utils/execFileNoThrow', () => ({
  ...nativeExec,
  execFileNoThrow: async (command: string) => {
    if (command === 'security') return { stdout: '', stderr: 'isolated keychain unavailable', code: 1 }
    throw new Error('Undeclared native subprocess')
  },
}))
const nativeExeca = await import('execa')
mock.module('execa', () => ({ ...nativeExeca, execaSync(command: string) { if (command === 'security') return { exitCode: 1 }; throw new Error('Undeclared native subprocess') } }))
mkdirSync(join(home, '.claude'), { recursive: true })
if (input.settings) writeFileSync(join(home, '.claude', 'settings.json'), JSON.stringify(input.settings))
writeFileSync(join(home, '.claude', '.credentials.json'), JSON.stringify(input.initial), { mode: 0o600 })
if (input.mode === 'schemas') {
  const schemas = await import('@modelcontextprotocol/sdk/shared/auth.js')
  const xss = (await import('xss')).default
  process.stdout.write(JSON.stringify(input.cases.map((c: any) => {
    if (c.kind === 'html') return { value: xss(c.value) }
    const schema = c.kind === 'client' ? schemas.OAuthClientInformationFullSchema : c.kind === 'resource' ? schemas.OAuthProtectedResourceMetadataSchema : c.kind === 'oidc' ? schemas.OpenIdProviderDiscoveryMetadataSchema : c.kind === 'tokens' ? schemas.OAuthTokensSchema : schemas.OAuthMetadataSchema
    const result = schema.safeParse(c.value)
    return result.success ? { value: result.data } : { invalid: true, error: result.error.message }
  })))
  process.exit(0)
}
if ((input.mode === 'flow' || input.mode === 'idplogin')) {
  const originalCrypto = await import('node:crypto')
  mock.module('crypto', () => ({ ...originalCrypto, randomBytes: (n: number) => Buffer.alloc(n), randomUUID: () => '00000000-0000-4000-8000-000000000000' }))
}
if ((input.mode === 'flow' || input.mode === 'idplogin')) {
  const nativeCrypto = globalThis.crypto
  Object.defineProperty(globalThis, 'crypto', { value: { subtle: nativeCrypto.subtle, getRandomValues: (a: Uint8Array) => a.fill(0) }, configurable: true })
}
if (input.mode === 'xaa' || input.mode === 'idp' || input.mode === 'idplogin' || input.mode === 'fullxaa') {
  const originalFetch = globalThis.fetch
  // TLS trust is the only transport primitive override, constrained to the
  // loopback endpoint named in this disposable fixture. Business fetches and
  // their full requests execute against the actual native HTTP test server.
  globalThis.fetch = ((address: any, init: any) => {
    const u = new URL(String(address))
    if (u.hostname !== '127.0.0.1' && u.hostname !== 'localhost') throw new Error('Undeclared external OAuth request')
    return originalFetch(address, { ...init, tls: { rejectUnauthorized: false } })
  }) as typeof fetch
  if (input.mode !== 'idplogin' && input.mode !== 'fullxaa') {
  let value: any = null
  let error: string | null = null
  let clear: boolean | null = null
  try {
    if (input.mode === 'xaa') value = await (await import('../../../src/services/mcp/xaa')).performCrossAppAccess(input.address, input.config)
    else value = await (await import('../../../src/services/mcp/xaaIdpLogin')).discoverOidc(input.address)
  } catch (e) { error = (e as Error).message; clear = (e as any).shouldClearIdToken ?? null }
  process.stdout.write(JSON.stringify({ value, error, clear }))
  process.exit(0)
  }
}
const events: any[] = []
if (input.captureEvents) {
  const analytics = await import('../../../src/services/analytics')
  // Analytics emission is a logging primitive; capture the original auth
  // code's fields rather than sending fixture events to a remote sink.
  mock.module('../../../src/services/analytics/index.js', () => ({ ...analytics, logEvent: (name: string, fields: any) => { if (name.startsWith('tengu_mcp_oauth_')) events.push({ name, fields }) } }))
}
const { ClaudeAuthProvider, getServerKey, hasMcpDiscoveryButNoToken, clearMcpDiscoveryWithoutTokens, saveMcpClientSecret, getMcpClientConfig, clearMcpClientConfig, clearServerTokensFromLocalStorage, normalizeOAuthErrorBody, revokeServerTokens } = await import('../../../src/services/mcp/auth')
if (input.mode === 'fullxaa') {
  let error: string | null = null
  let value: any = null
  try {
    if (input.action === 'authenticate') await (await import('../../../src/services/mcp/auth')).performMCPOAuthFlow(input.name, input.config, () => { throw new Error('Cached IdP must not open browser') }, undefined, { skipBrowserOpen: true })
    else value = await new ClaudeAuthProvider(input.name, input.config, 'http://localhost:3118/callback').tokens() ?? null
  } catch (e) { error = (e as Error).message }
  process.stdout.write(JSON.stringify({ error, value, ...(input.captureEvents ? { events } : {}), storage: JSON.parse(readFileSync(join(home, '.claude', '.credentials.json'), 'utf8')) }))
  process.exit(0)
}
if (input.mode === 'flow' || input.mode === 'idplogin') {
  const { performMCPOAuthFlow } = await import('../../../src/services/mcp/auth')
  let url: string | null = null
  let callback: { status: number, body: string } | null = null
  let pending: Promise<void> = Promise.resolve()
  const controller = new AbortController()
  let error: string | null = null
  let value: string | null = null
  let manual: ((url: string) => void) | undefined
  const handleURL = (address: string) => {
      url = address
      if (input.kind === 'cancel') { controller.abort(); return }
      const params = new URL(address).searchParams
      const suffix = input.kind === 'denied' ? '?error=access_denied&error_description=declined' : '?code=issued-code&state=' + encodeURIComponent(input.kind === 'mismatch' ? 'wrong' : params.get('state')!)
      if (input.kind === 'manual') { manual!('not a URL'); manual!(params.get('redirect_uri')! + suffix); return }
      pending = fetch(params.get('redirect_uri')! + suffix).then(async response => { callback = { status: response.status, body: await response.text() } })
  }
  try {
    if (input.mode === 'idplogin') value = await (await import('../../../src/services/mcp/xaaIdpLogin')).acquireIdpIdToken({ idpIssuer: input.address, idpClientId: 'idp-client', idpClientSecret: 'idp-secret', callbackPort: input.config.oauth.callbackPort, onAuthorizationUrl: handleURL, skipBrowserOpen: true, abortSignal: controller.signal })
    else await performMCPOAuthFlow(input.name, input.config, handleURL, controller.signal, { skipBrowserOpen: true, onWaitingForCallback: submit => { manual = submit } })
  } catch (e) { error = (e as Error).message }
  await pending
  process.stdout.write(JSON.stringify({ url, callback, error, ...(input.captureEvents ? { events } : {}), ...(input.mode === 'idplogin' ? { value } : {}), storage: JSON.parse(readFileSync(join(home, '.claude', '.credentials.json'), 'utf8')) }))
  process.exit(0)
}
const provider = new ClaudeAuthProvider(input.name, input.config, 'http://localhost:3118/callback')
const idp = await import('../../../src/services/mcp/xaaIdpLogin')
const observations = []
for (const op of input.operations) {
  let value
  if (op.action === 'key') value = getServerKey(input.name, input.config)
  else if (op.action === 'client-info') value = await provider.clientInformation()
  else if (op.action === 'save-client') await provider.saveClientInformation(op.value)
  else if (op.action === 'save-tokens') await provider.saveTokens(op.value)
  else if (op.action === 'tokens') value = await provider.tokens()
  else if (op.action === 'save-discovery') await provider.saveDiscoveryState(op.value)
  else if (op.action === 'discovery') value = await provider.discoveryState()
  else if (op.action === 'has-discovery') value = hasMcpDiscoveryButNoToken(input.name, input.config)
  else if (op.action === 'clear-discovery') clearMcpDiscoveryWithoutTokens(input.name, input.config)
  else if (op.action === 'invalidate') await provider.invalidateCredentials(op.scope)
  else if (op.action === 'save-secret') saveMcpClientSecret(input.name, input.config, op.value)
  else if (op.action === 'client-config') value = getMcpClientConfig(input.name, input.config)
  else if (op.action === 'clear-client-config') clearMcpClientConfig(input.name, input.config)
  else if (op.action === 'revoke') await revokeServerTokens(input.name, input.config, { preserveStepUpState: op.value })
  else if (op.action === 'clear-tokens') clearServerTokensFromLocalStorage(input.name, input.config)
  else if (op.action === 'metadata') { provider.setMetadata(op.value); value = provider.clientMetadata }
  else if (op.action === 'step-up') provider.markStepUpPending(op.value)
  else if (op.action === 'redirect') await provider.redirectToAuthorization(new URL(op.value))
  else if (op.action === 'normalize') { const response = await normalizeOAuthErrorBody(new Response(op.body, { status: op.status })); value = { status: response.status, body: await response.text() } }
  else if (op.action === 'issuer-key') value = idp.issuerKey(op.issuer)
  else if (op.action === 'save-idp-token') value = idp.saveIdpIdTokenFromJwt(op.issuer, op.value)
  else if (op.action === 'idp-token') value = idp.getCachedIdpIdToken(op.issuer)
  else if (op.action === 'clear-idp-token') idp.clearIdpIdToken(op.issuer)
  else if (op.action === 'save-idp-secret') idp.saveIdpClientSecret(op.issuer, op.value)
  else if (op.action === 'idp-secret') value = idp.getIdpClientSecret(op.issuer)
  else if (op.action === 'clear-idp-secret') idp.clearIdpClientSecret(op.issuer)
  else if (op.action === 'advance') now += op.delta
  else throw new Error('Undeclared MCP OAuth operation')
  observations.push({ value: value ?? null, storage: JSON.parse(readFileSync(join(home, '.claude', '.credentials.json'), 'utf8')) })
}
process.stdout.write(JSON.stringify(observations))
