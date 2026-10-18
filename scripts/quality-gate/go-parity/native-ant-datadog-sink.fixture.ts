import fs from 'node:fs'
import { createHash } from 'node:crypto'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { mock, spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Whole original ant Datadog sink')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_ANT_DATADOG_SOURCE !== '1') throw new Error('Pinned private ant Datadog fixture required')
if (typeof input.origin !== 'string' || !/^http:\/\/127\.0\.0\.1:[1-9][0-9]*$/.test(input.origin)) throw new Error('Explicit owned HTTP origin required')
const profiles = ['ant-enabled', 'ant-disabled', 'ant-killed', 'external-override']
if (!profiles.includes(input.kind)) throw new Error('Unknown concrete Datadog routing profile')
const ant = input.kind !== 'external-override'
if (process.env.USER_TYPE !== (ant ? 'ant' : 'external') || process.env.NODE_ENV !== 'production') throw new Error('Explicit production user type required')
if (process.env.DISABLE_TELEMETRY || process.env.CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC) throw new Error('Actual analytics must remain enabled')
const overrides = { tengu_log_datadog_events: input.kind !== 'ant-disabled', tengu_frond_boric: { datadog: input.kind === 'ant-killed' } }
if (process.env.CLAUDE_INTERNAL_FC_OVERRIDES !== JSON.stringify(overrides)) throw new Error('Exact original ant gate inputs required')
const root = fileURLToPath(new URL('../../../', import.meta.url))
const sourceFiles = ['src/main.tsx', 'src/entrypoints/init.ts', 'src/utils/sinks.ts', 'src/cli/handlers/plugins.ts', 'src/services/analytics/index.ts', 'src/services/analytics/sink.ts', 'src/services/analytics/datadog.ts', 'src/services/analytics/sinkKillswitch.ts', 'src/services/analytics/growthbook.ts', 'src/services/analytics/metadata.ts', 'src/services/analytics/firstPartyEventLogger.ts', 'src/services/analytics/firstPartyEventLoggingExporter.ts', 'src/utils/model/model.ts', 'src/utils/model/providers.ts', 'tsconfig.json', 'preload.ts']
const sourceIdentity = () => Object.fromEntries(sourceFiles.map(path => [path, createHash('sha256').update(fs.readFileSync(join(root, path))).digest('hex')]))
const sourceBefore = sourceIdentity()
const cwd = join(home, 'workspace'), config = join(home, '.claude')
for (const path of [cwd, config, join(cwd, '.claude')]) fs.mkdirSync(path, { recursive: true })
const key = 'fake-owned-ant-datadog-api-key'
// The original public alias returns before this frozen fork's unavailable
// resolveAntModel branch. Its ordinary env override supplies the real model;
// no metadata, model resolver, analytics sink or exporter is replaced.
process.env.ANTHROPIC_MODEL = 'sonnet'
process.env.ANTHROPIC_DEFAULT_SONNET_MODEL = 'claude-sonnet-4-5-20250929'
process.env.ANTHROPIC_API_KEY = key
const settingsPath = join(config, 'settings.json'), globalPath = join(config, '.claude.json')
fs.writeFileSync(settingsPath, JSON.stringify({ env: { ANTHROPIC_BASE_URL: input.origin + '/safe' }, model: 'sonnet', futurePrivate: { keep: true } }))
fs.writeFileSync(globalPath, JSON.stringify({ userID: 'owned-ant-datadog-device', migrationVersion: 11, numStartups: 3, futureGlobal: { keep: true }, customApiKeyResponses: { approved: [key.slice(-20)], rejected: [] } }))
const settingsBefore = fs.readFileSync(settingsPath, 'utf8')
const absent = [...absentManagedSettingsFixturePaths(), '/Library/Application Support/ClaudeCode/managed-mcp.json', '/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token']
for (const path of ['/sys/hypervisor/uuid', '/.dockerenv']) if (!fs.existsSync(path)) absent.push(path)
for (const spelling of new Set([home, fs.realpathSync(home)])) for (let path = spelling; ; path = dirname(path)) {
  for (const suffix of ['.git', '.claude/skills', '.claude/commands', '.claude/output-styles', '.agents/skills', '.agents/commands']) if (!fs.existsSync(join(path, suffix))) absent.push(join(path, suffix))
  if (dirname(path) === path) break
}
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent, loopbackFetchOrigin: input.origin })
const deniedNativeHTTPS: unknown[] = []
const https = require('node:https')
const guardedHTTPSRequest = https.request
https.request = (...args: any[]) => {
  const target = args[0]
  deniedNativeHTTPS.push(target instanceof URL ? target.href : typeof target === 'string' ? target : { hostname: target?.hostname, path: target?.path })
  return guardedHTTPSRequest(...args)
}
mock.module('../../../src/utils/settings/mdm/rawRead', () => ({ startMdmRawRead: () => {}, getMdmRawReadPromise: () => Promise.resolve({ plistStdouts: [], hklmStdout: null, hkcuStdout: null }), fireRawRead: async () => ({ plistStdouts: [], hklmStdout: null, hkcuStdout: null }) }))
mock.module('../../../src/utils/secureStorage/keychainPrefetch', () => ({ getLegacyApiKeyPrefetchResult: () => ({ stdout: '' }), getLegacyApiKeyPrefetchPromise: () => undefined, startKeychainPrefetch: () => {}, clearLegacyApiKeyPrefetch: () => {}, ensureKeychainPrefetchCompleted: async () => {} }))
// The common capability guard supplies only an empty external secure store.
// Original auth, trust, provider, gate, metadata and exporter code all execute.
await import('../../../preload')
process.argv = [process.execPath, fileURLToPath(new URL('../../../src/entrypoints/cli.tsx', import.meta.url)), 'plugin', 'list', '--json']
const state = await import('../../../src/bootstrap/state')
state.setCwdState(cwd); state.setOriginalCwd(cwd); state.setProjectRoot(cwd)
const configModule = await import('../../../src/utils/config')
const publicEvents = await import('../../../src/services/analytics/index')
const datadog = await import('../../../src/services/analytics/datadog')
const metadata = await import('../../../src/services/analytics/metadata')
const logger = await import('../../../src/services/analytics/firstPartyEventLogger')
const exporter = await import('../../../src/services/analytics/firstPartyEventLoggingExporter')
const analyticsConfig = await import('../../../src/services/analytics/config')
const growthbook = await import('../../../src/services/analytics/growthbook')
const killswitch = await import('../../../src/services/analytics/sinkKillswitch')
const providers = await import('../../../src/utils/model/providers')
const initializerCache = datadog.initializeDatadog.cache
if (initializerCache.has(undefined)) throw new Error('Fresh original Datadog initializer cache required')
const routes: Array<Record<string, unknown>> = []
const routeJobs: Promise<void>[] = [], metadataJobs: Promise<unknown>[] = []
const originalTrack = datadog.trackDatadogEvent
spyOn(datadog, 'trackDatadogEvent').mockImplementation((...args) => {
  const cachedBefore = initializerCache.has(undefined)
  const pending = originalTrack(...args)
  const initialization = initializerCache.get(undefined)
  routes.push({ name: args[0], properties: args[1], initializerCachedBefore: cachedBefore, initializerCachedAfter: initializerCache.has(undefined), nativePromise: Promise.resolve(pending) === pending, initializerNativePromise: initialization instanceof Promise && Promise.resolve(initialization) === initialization })
  routeJobs.push(pending)
  return pending // The original sink receives the very same callee Promise.
})
const originalMetadata = metadata.getEventMetadata
spyOn(metadata, 'getEventMetadata').mockImplementation((...args) => {
  const pending = originalMetadata(...args)
  metadataJobs.push(pending)
  return pending
})
const initializations: unknown[] = [], exports: unknown[] = []
const originalInitialize = logger.initialize1PEventLogging
spyOn(logger, 'initialize1PEventLogging').mockImplementation(() => {
  const result = originalInitialize()
  initializations.push({ nonInteractive: state.getIsNonInteractiveSession(), trust: configModule.checkHasTrustDialogAccepted() })
  return result
})
const originalExport = exporter.FirstPartyEventLoggingExporter.prototype.export
spyOn(exporter.FirstPartyEventLoggingExporter.prototype, 'export').mockImplementation(function (records, callback) {
  exports.push(records.map(record => ({ body: record.body, attributes: record.attributes })))
  return originalExport.call(this, records, callback)
})
const axios = (await import('axios')).default
const originalPost = axios.post.bind(axios)
const prepares: unknown[] = [], deniedTelemetry: Array<{ phase: string; target: unknown }> = []
let phase = 'actual-main'
const datadogEndpoint = 'https://http-intake.logs.us5.datadoghq.com/api/v2/logs'
spyOn(axios, 'post').mockImplementation((target: any, body: any, options: any) => {
  if (target !== 'https://api.anthropic.com/api/event_logging/batch') {
    deniedTelemetry.push({ phase, target })
    throw new Error('Unowned Source telemetry endpoint: ' + target)
  }
  prepares.push({ target, headers: options.headers, timeout: options.timeout, body })
  // Only the exact real exporter endpoint is mapped to an owned physical socket.
  return originalPost(input.origin + '/events', body, { ...options, adapter: 'fetch' })
})
const { main } = await import('../../../src/main')
const exits: number[] = []
const terminalExit = new Error('owned external terminal exit')
const actualExit = process.exit.bind(process)
process.exit = ((code?: number) => { exits.push(code ?? 0); throw terminalExit }) as never
try { await main() } catch (error) { if (error !== terminalExit) throw error }
// This is an explicit post-command observer join. It does not change the real
// emit/dispatch caller, cold startup, or normal process-exit timing contract.
await Promise.all(routeJobs)
await Promise.all(metadataJobs)
await logger.shutdown1PEventLogging()
await datadog.shutdownDatadog()
const initializedByActualRoute = initializerCache.has(undefined)
const routedInitialization = initializerCache.get(undefined)
phase = 'post-command-initializer-observation'
const firstInitialization = datadog.initializeDatadog(), secondInitialization = datadog.initializeDatadog()
const initialized = await firstInitialization
const initialization = { initializedByActualRoute, result: initialized, sameMemoizedPromise: firstInitialization === secondInitialization, routeUsesMemoizedPromise: initializedByActualRoute ? routedInitialization === firstInitialization : null, nativePromise: Promise.resolve(firstInitialization) === firstInitialization }
phase = 'negative-datadog-http-probe'
let datadogHTTPRejected = false
try { await axios.post(datadogEndpoint, [{ owned_guard_probe: true }]) } catch (error) { datadogHTTPRejected = error instanceof Error && error.message === 'Unowned Source telemetry endpoint: ' + datadogEndpoint }
const aliasEvents = await import('src/services/analytics/index')
const aliasDatadog = await import('src/services/analytics/datadog')
const aliasConfig = await import('src/utils/config')
const receipt = {
  kind: input.kind, sourceBefore, sourceAfter: sourceIdentity(),
  moduleIdentity: { analytics: aliasEvents === publicEvents, datadog: aliasDatadog === datadog, config: aliasConfig === configModule, analyticsLogEvent: aliasEvents.logEvent === publicEvents.logEvent, datadogTracker: aliasDatadog.trackDatadogEvent === datadog.trackDatadogEvent },
  gate: growthbook.checkStatsigFeatureGate_CACHED_MAY_BE_STALE('tengu_log_datadog_events'), datadogKilled: killswitch.isSinkKilled('datadog'), firstPartyKilled: killswitch.isSinkKilled('firstParty'), analyticsDisabled: analyticsConfig.isAnalyticsDisabled(), provider: providers.getAPIProvider(),
  routes, initialization, initializations, prepares, exports, deniedTelemetry, deniedNativeHTTPS, datadogHTTPRejected, exits,
  userSettingsUnchanged: settingsBefore === fs.readFileSync(settingsPath, 'utf8'), globalAfter: JSON.parse(fs.readFileSync(globalPath, 'utf8')), caps,
  scope: 'Whole original main preAction -> initSinks -> pluginListHandler -> actual sink gate/killswitch -> original empty-token memoized Datadog initializer and real 1P metadata/logger/exporter/socket. Observers return original Promises. Post-command initializer and HTTP denial probes are separate, not positive Datadog export or cold timing authority.',
}
fs.writeFileSync(join(home, 'ant-datadog-source.json'), JSON.stringify(receipt))
actualExit(exits[0] ?? 0)
