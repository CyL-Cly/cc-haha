import { assertDisposableFixtureHome } from './fixture-guard'
import { mock } from 'bun:test'
assertDisposableFixtureHome(process.env.HOME, 'Native credential error logging')
if (process.env.CC_HAHA_ERROR_LOG_FIXTURE !== '1') throw new Error('Unowned error logging fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
if (input.auth) Object.defineProperty(process, 'platform', { value: 'darwin', configurable: true })
await import('../../../preload')
const fixed = 1700000000000
const DateOriginal = Date
globalThis.Date = class extends DateOriginal {
  constructor(...args: any[]) { super(...(args.length ? args : [fixed]) as [any]) }
  static now() { return fixed }
} as DateConstructor
const names = ['CLAUDE_CODE_USE_BEDROCK', 'CLAUDE_CODE_USE_VERTEX', 'CLAUDE_CODE_USE_FOUNDRY', 'CLAUDE_CODE_USE_AZURE_OPENAI', 'DISABLE_ERROR_REPORTING', 'CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC', 'DISABLE_TELEMETRY', 'NODE_ENV', 'USER_TYPE', 'DEBUG', 'DEBUG_SDK', 'ANTHROPIC_API_KEY', 'ANTHROPIC_AUTH_TOKEN', 'CLAUDE_CODE_OAUTH_TOKEN', 'CLAUDE_CODE_SIMPLE', 'CI']
const log = await import('../../../src/utils/log')
const rows: any[] = []
if (input.auth) {
  let current: any, reads = 0
  const config = await import('../../../src/utils/config')
  const portable = await import('../../../src/utils/execFileNoThrow')
  mock.module('../../../src/utils/config', () => ({ ...config, getGlobalConfig: () => current.global ?? {} }))
  mock.module('../../../src/utils/execFileNoThrow', () => ({ ...portable, execSyncWithDefaults_DEPRECATED: () => { reads++; const err = new Error(current.message); err.stack = current.stack ?? ''; throw err } }))
  const prefetch = await import('../../../src/utils/secureStorage/keychainPrefetch')
  mock.module('../../../src/utils/secureStorage/keychainPrefetch', () => ({ ...prefetch, getLegacyApiKeyPrefetchResult: () => null }))
  const auth = await import('../../../src/utils/auth')
  for (const profile of input.auth) {
    current = profile; reads = 0
    names.forEach(name => delete process.env[name])
    Object.entries(profile.environment).forEach(([name, value]) => { process.env[name] = String(value) })
    log._resetErrorLogForTesting(); auth.getApiKeyFromConfigOrMacOSKeychain.cache.clear()
    const sink: any[] = []
    log.attachErrorLogSink({ logError: error => sink.push({ name: error.name, message: error.message, stack: error.stack }), logMCPError() {}, logMCPDebug() {}, getErrorsPath: () => '', getMCPLogsPath: () => '' })
    const first = auth.getApiKeyFromConfigOrMacOSKeychain(), second = auth.getApiKeyFromConfigOrMacOSKeychain()
    rows.push({ first, second, reads, memory: log.getInMemoryErrors(), sink })
  }
} else {
  for (const profile of input.profiles) {
    names.forEach(name => delete process.env[name])
    Object.entries(profile.environment ?? {}).forEach(([name, value]) => { process.env[name] = String(value) })
    log._resetErrorLogForTesting()
    const sink: any[] = []
    for (const operation of profile.operations) {
      if (operation.attach) {
        log.attachErrorLogSink({ logError: error => { sink.push({ name: error.name, message: error.message, stack: error.stack }); if (operation.throwSink) throw new Error('private sink failed') }, logMCPError() {}, logMCPDebug() {}, getErrorsPath: () => '', getMCPLogsPath: () => '' })
      } else {
        const error = new Error(operation.message); error.name = operation.name ?? 'Error'; error.stack = operation.stack ?? ''
        log.logError(error)
      }
    }
    rows.push({ memory: log.getInMemoryErrors(), sink })
  }
}
process.stdout.write(JSON.stringify({ rows }))
