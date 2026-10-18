import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { execFileSync } from 'node:child_process'
import { mock, spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Whole original command migrations')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_COMMAND_MIGRATIONS_SOURCE !== '1') throw new Error('Pinned private sinks auth fixture required')
if (typeof input.origin !== 'string' || !/^http:\/\/127\.0\.0\.1:[1-9][0-9]*$/.test(input.origin)) throw new Error('Explicit owned HTTP origin required')
const kinds = ['sonnet-pro', 'sonnet-max-from-1m', 'opus-max', 'opus-pro', 'opus-unknown', 'bypass-managed', 'bypass-project-only', 'mcp-git', 'mcp-git-privacy', 'changelog-new', 'changelog-existing', 'changelog-write-failure', 'already-v11']
if (!kinds.includes(input.kind)) throw new Error('Unknown concrete migration profile')
const cwd = join(home, 'workspace'), config = join(home, '.claude')
for (const path of [cwd, config, join(cwd, '.claude')]) fs.mkdirSync(path, { recursive: true })
const key = 'fake-owned-command-1p-env-key'
const model = input.kind === 'sonnet-pro' ? 'claude-sonnet-4-5-20250929'
  : input.kind === 'sonnet-max-from-1m' ? 'sonnet[1m]' : input.kind.startsWith('opus-') ? 'opus' : undefined
const settings = { env: { ANTHROPIC_BASE_URL: input.origin + '/safe' }, futurePrivate: { keep: true }, ...(model === undefined ? {} : { model }) }
fs.writeFileSync(join(config, 'settings.json'), JSON.stringify(settings))
const global: Record<string, unknown> = { userID: 'owned-command-1p-device', numStartups: 3, futureGlobal: { keep: true } }
const subscriber = input.kind.startsWith('sonnet-') || input.kind.startsWith('opus-')
if (subscriber) {
  global.oauthAccount = { accountUuid: 'owned-account', organizationUuid: 'owned-organization', emailAddress: 'owned@fixture.invalid', billingType: 'stripe_subscription', accountCreatedAt: '2020-01-01T00:00:00.000Z', subscriptionCreatedAt: '2020-01-01T00:00:00.000Z', hasExtraUsageEnabled: false }
  const subscriptionType = input.kind.endsWith('-pro') ? 'pro' : input.kind === 'opus-unknown' ? null : 'max'
  fs.writeFileSync(join(config, '.credentials.json'), JSON.stringify({ claudeAiOauth: { accessToken: 'fake-owned-command-cached-token', scopes: ['user:inference', 'user:profile'], expiresAt: Date.now() + 365 * 86400000, subscriptionType, rateLimitTier: subscriptionType === 'pro' ? 'default_claude_pro' : 'default_claude_max_5x' } }))
  delete process.env.ANTHROPIC_API_KEY
} else {
  global.customApiKeyResponses = { approved: [key.slice(-20)], rejected: [] }
  process.env.ANTHROPIC_API_KEY = key
}
if (input.kind.startsWith('bypass-')) global.bypassPermissionsModeAccepted = true
if (input.kind === 'bypass-project-only') fs.writeFileSync(join(cwd, '.claude', 'settings.json'), JSON.stringify({ skipDangerousModePermissionPrompt: true, futureProject: { keep: true } }))
if (input.kind.startsWith('mcp-git')) {
  const privateBin = join(home, 'bin'); fs.mkdirSync(privateBin)
  const actualGit = process.platform === 'darwin' ? '/Library/Developer/CommandLineTools/usr/bin/git' : '/usr/bin/git'
  fs.copyFileSync(actualGit, join(privateBin, 'git')); fs.chmodSync(join(privateBin, 'git'), 0o700)
  process.env.PATH = fs.realpathSync(privateBin) + ':/usr/bin:/bin'
  process.env.GIT_CONFIG_NOSYSTEM = '1'; process.env.GIT_CONFIG_GLOBAL = join(home, '.gitconfig')
  execFileSync(actualGit, ['init', '--quiet', cwd], { env: { ...process.env }, stdio: ['ignore', 'pipe', 'pipe'] })
  fs.mkdirSync(join(cwd, 'nested', '.claude'), { recursive: true }); global.projects = { [cwd]: { enableAllProjectMcpServers: true, enabledMcpjsonServers: ['old', 'shared'], disabledMcpjsonServers: ['disabled'], futureProject: { keep: true } } }
  fs.writeFileSync(join(cwd, 'nested', '.claude', 'settings.local.json'), JSON.stringify({ enableAllProjectMcpServers: false, enabledMcpjsonServers: ['local', 'shared'], disabledMcpjsonServers: ['local-disabled'], futureLocal: { keep: true } }))
}
if (input.kind.startsWith('changelog-')) {
  global.cachedChangelog = '# owned migration changelog\n'; fs.mkdirSync(join(config, 'cache'))
  if (input.kind === 'changelog-existing') fs.writeFileSync(join(config, 'cache', 'changelog.md'), '# already-owned cache\n')
  if (input.kind === 'changelog-write-failure') fs.mkdirSync(join(config, 'cache', 'changelog.md'))
}
if (input.kind === 'already-v11') global.migrationVersion = 11
fs.writeFileSync(join(config, '.claude.json'), JSON.stringify(global))
const userBefore = fs.readFileSync(join(config, 'settings.json'), 'utf8')
const globalBefore = fs.readFileSync(join(config, '.claude.json'), 'utf8')
if (input.kind === 'mcp-git-privacy') process.env.DISABLE_TELEMETRY = '1'
const absent = [...absentManagedSettingsFixturePaths(), '/Library/Application Support/ClaudeCode/managed-mcp.json', '/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token']
for (const path of ['/sys/hypervisor/uuid', '/.dockerenv']) if (!fs.existsSync(path)) absent.push(path)
for (const spelling of new Set([home, fs.realpathSync(home)])) for (let path = spelling; ; path = dirname(path)) {
  for (const suffix of ['.git', '.claude/skills', '.claude/commands', '.agents/skills', '.agents/commands']) if (!fs.existsSync(join(path, suffix))) absent.push(join(path, suffix))
  if (dirname(path) === path) break
}
const spawns: unknown[] = []
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent, loopbackFetchOrigin: input.origin, ...(input.kind.startsWith('mcp-git') ? { privateExecutableFiles: [join(home, 'bin', 'git')], approvedSpawn: (request: any) => { spawns.push({ command: request.command, args: request.args, cwd: request.options.cwd }); return request.command === 'git' && JSON.stringify(request.args) === JSON.stringify(['check-ignore', '.claude/settings.local.json']) && request.options.cwd === join(cwd, 'nested') } } : {}) })
const deniedRequests: unknown[] = []
const https = require('node:https')
const deniedHTTPSRequest = https.request
https.request = (...args: any[]) => {
  const request = args[0]
  deniedRequests.push({ target: request instanceof URL ? request.href : typeof request === 'string' ? request : { protocol: request?.protocol, hostname: request?.hostname, path: request?.path }, stack: new Error('actual source HTTPS admission').stack })
  return deniedHTTPSRequest(...args)
}
const mdmRaw = { plistStdouts: input.kind === 'bypass-managed' ? [{ stdout: JSON.stringify({ skipDangerousModePermissionPrompt: true }), label: 'device-level managed preferences' }] : [], hklmStdout: null, hkcuStdout: null }
mock.module('../../../src/utils/settings/mdm/rawRead', () => ({ startMdmRawRead: () => {}, getMdmRawReadPromise: () => Promise.resolve(mdmRaw), fireRawRead: async () => mdmRaw }))
mock.module('../../../src/utils/secureStorage/keychainPrefetch', () => ({ getLegacyApiKeyPrefetchResult: () => ({ stdout: '' }), getLegacyApiKeyPrefetchPromise: () => undefined, startKeychainPrefetch: () => {}, clearLegacyApiKeyPrefetch: () => {}, ensureKeychainPrefetchCompleted: async () => {} }))
// Only the external secure-store primitive reads this private fake credential
// file. Token/auth/scope/expiry selection remains the original implementation.
mock.module('../../../src/utils/secureStorage/index', () => ({ getSecureStorage: () => ({
  read: () => { caps.secureReads.push('private-fake-credential-store'); try { return JSON.parse(fs.readFileSync(join(config, '.credentials.json'), 'utf8')) } catch { return null } },
  readAsync: async () => { caps.secureReads.push('private-fake-credential-store-async'); try { return JSON.parse(fs.readFileSync(join(config, '.credentials.json'), 'utf8')) } catch { return null } },
}) }))
;(globalThis as any).MACRO = { VERSION: '999.0.0-command-sinks-auth', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
process.argv = [process.execPath, fileURLToPath(new URL('../../../src/entrypoints/cli.tsx', import.meta.url)), 'plugin', 'list', '--json']
const state = await import('../../../src/bootstrap/state')
const startCwd = input.kind.startsWith('mcp-git') ? join(cwd, 'nested') : cwd
state.setCwdState(startCwd); state.setOriginalCwd(startCwd); state.setProjectRoot(startCwd)
const configModule = await import('../../../src/utils/config')
const auth = await import('../../../src/utils/auth')
const metadata = await import('../../../src/services/analytics/metadata')
const logger = await import('../../../src/services/analytics/firstPartyEventLogger')
const exporter = await import('../../../src/services/analytics/firstPartyEventLoggingExporter')
const settingsModule = await import('../../../src/utils/settings/settings')
const releaseNotes = await import('../../../src/utils/releaseNotes')
const gitIgnore = await import('../../../src/utils/git/gitignore')
const mutations: unknown[] = [], backgroundJobs: Promise<void>[] = []
for (const name of ['saveGlobalConfig', 'saveCurrentProjectConfig'] as const) {
  const actual = configModule[name]
  spyOn(configModule, name).mockImplementation(((...args: any[]) => { const returned = (actual as any)(...args); mutations.push({ kind: name, global: JSON.parse(fs.readFileSync(join(config, '.claude.json'), 'utf8')) }); return returned }) as any)
}
const writeSettings = settingsModule.updateSettingsForSource
spyOn(settingsModule, 'updateSettingsForSource').mockImplementation((...args) => {
  const returned = writeSettings(...args)
  mutations.push({ kind: 'updateSettingsForSource', source: args[0], patch: args[1], undefinedKeys: Object.keys(args[1]).filter(key => (args[1] as any)[key] === undefined), error: returned.error?.message ?? null })
  return returned
})
for (const [module, name] of [[releaseNotes, 'migrateChangelogFromConfig'], [gitIgnore, 'addFileGlobRuleToGitignore']] as const) {
  const actual = module[name as keyof typeof module] as (...args: any[]) => Promise<void>
  spyOn(module as any, name).mockImplementation((...args: any[]) => { const returned = actual(...args); backgroundJobs.push(returned); return returned })
}
const publicEvents = await import('../../../src/services/analytics/index')
const eventCalls: Array<{ name: string; metadata: unknown }> = []
const actualLogEvent = publicEvents.logEvent
spyOn(publicEvents, 'logEvent').mockImplementation((...args) => { const returned = actualLogEvent(...args); eventCalls.push({ name: args[0], metadata: args[1] }); return returned })
const { main } = await import('../../../src/main')
const axios = (await import('axios')).default
const originalPost = axios.post.bind(axios)
const prepares: unknown[] = [], emitted: unknown[] = [], initializations: unknown[] = []
// The external HTTP primitive maps only the exact real 1P endpoint to a real
// loopback socket. No headers, payload, status or exporter logic is fabricated.
spyOn(axios, 'post').mockImplementation((target: any, body: any, options: any) => {
  if (target !== 'https://api.anthropic.com/api/event_logging/batch') throw new Error('Unowned Source telemetry endpoint: ' + target)
  prepares.push({ target, headers: options.headers, timeout: options.timeout, body })
  return originalPost(input.origin + '/events', body, { ...options, adapter: 'fetch' })
})
const metadataJobs: Promise<unknown>[] = []
const getMetadata = metadata.getEventMetadata
spyOn(metadata, 'getEventMetadata').mockImplementation((...args) => {
  const pending = getMetadata(...args)
  metadataJobs.push(pending)
  return pending // same callee Promise; no observer async-return adoption
})
const initialize = logger.initialize1PEventLogging
spyOn(logger, 'initialize1PEventLogging').mockImplementation(() => {
  const result = initialize()
  initializations.push({ phase: 'actual-init-1p', nonInteractive: state.getIsNonInteractiveSession(), trust: configModule.checkHasTrustDialogAccepted() })
  return result
})
const originalExport = exporter.FirstPartyEventLoggingExporter.prototype.export
spyOn(exporter.FirstPartyEventLoggingExporter.prototype, 'export').mockImplementation(function (records, callback) {
  emitted.push(records.map(record => ({ body: record.body, attributes: record.attributes })))
  return originalExport.call(this, records, callback)
})
const exits: number[] = []
// This fixture replaces only the terminal OS exit primitive, then exercises
// the original graceful 1P shutdown consumer. It is not normal-exit timing or
// cold CLI/HEAD authority; the original main/handler/sinks all execute.
const terminalExit = new Error('owned external terminal exit')
const actualTerminalExit = process.exit.bind(process)
process.exit = ((code?: number) => { exits.push(code ?? 0); throw terminalExit }) as never
try { await main() } catch (failure) { if (failure !== terminalExit) throw failure }
await Promise.all(backgroundJobs)
await Promise.all(metadataJobs)
await logger.shutdown1PEventLogging()
// Check alias identity only after the original work and exporter have settled.
// A copied fixture without the original tsconfig can otherwise load a second
// analytics/config instance through the migrations' `src/*` imports.
const aliasEvents = await import('src/services/analytics/index')
const aliasConfig = await import('src/utils/config')
const moduleIdentity = { analytics: aliasEvents === publicEvents, config: aliasConfig === configModule, analyticsLogEvent: aliasEvents.logEvent === publicEvents.logEvent, configGetter: aliasConfig.getGlobalConfig === configModule.getGlobalConfig }
const fileState = (path: string) => { try { const stat = fs.statSync(path); return stat.isDirectory() ? { directory: true } : { text: fs.readFileSync(path, 'utf8'), base64: fs.readFileSync(path).toString('base64') } } catch { return null } }
const receipt = { kind: input.kind, moduleIdentity, initializations, prepares, emitted, exits, deniedRequests, nonInteractive: state.getIsNonInteractiveSession(), trust: configModule.checkHasTrustDialogAccepted(), subscriber: auth.isClaudeAISubscriber(), userBefore, globalBefore, userAfter: JSON.parse(fs.readFileSync(join(config, 'settings.json'), 'utf8')), globalAfter: JSON.parse(fs.readFileSync(join(config, '.claude.json'), 'utf8')), policyAfter: settingsModule.getSettingsForSource('policySettings'), localAfter: fileState(join(startCwd, '.claude', 'settings.local.json')), changelogAfter: fileState(join(config, 'cache', 'changelog.md')), gitignoreAfter: fileState(join(home, '.config', 'git', 'ignore')), backgroundJobCount: backgroundJobs.length, mutations, spawns, eventCalls, telemetryDisabled: process.env.DISABLE_TELEMETRY === '1', caps, scope: 'whole original main preAction init -> sinks -> synchronous v11 sequence + original detached changelog/local-GitIgnore -> real plugin/metadata/logger/exporter. Same-callee return identities; observation joins original detached jobs only after terminal external exit; no cold timing authority' }
fs.writeFileSync(join(home, 'command-migrations-source.json'), JSON.stringify(receipt))
// Original CLI exits at the intercepted boundary above. After joining only the
// observed original jobs, use that same OS exit primitive to retire any native
// subprocess signal handles; natural module completion is not a CLI exit.
actualTerminalExit(exits[0] ?? 0)
