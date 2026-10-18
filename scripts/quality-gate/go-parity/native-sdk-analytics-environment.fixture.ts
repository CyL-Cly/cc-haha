import fs from 'node:fs'
import * as fsExports from 'node:fs'
import { dirname, join } from 'node:path'
import { mock, spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Original SDK environment and file analytics')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_SDK_ANALYTICS_ENVIRONMENT !== '1') throw new Error('Explicit pinned SDK analytics fixture required')
if (typeof input.origin !== 'string' || !/^http:\/\/127\.0\.0\.1:[1-9][0-9]*$/.test(input.origin) || process.env.ANTHROPIC_BASE_URL !== input.origin) throw new Error('Owned exact loopback origin required')
if (process.env.NODE_ENV !== 'production' || process.env.USER_TYPE !== 'external' || process.env.DISABLE_TELEMETRY !== undefined || process.env.CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC !== undefined) throw new Error('Actual external first-party logger required')
const project = join(home, 'workspace'), initial = join(home, '.claude'), nextRequested = 'sdk-next', nextPhysical = join(project, nextRequested)
const filePath = join(project, 'observed.txt'), content = 'ACTUAL_SDK_ANALYTICS_READ\n'
for (const directory of [project, initial, nextPhysical]) fs.mkdirSync(directory, { recursive: true })
for (const [directory, value] of [[initial, 'INITIAL'], [nextPhysical, 'NEXT']] as const) {
  fs.writeFileSync(join(directory, 'settings.json'), JSON.stringify({ future: { directory: value } }))
  fs.writeFileSync(join(directory, '.claude.json'), JSON.stringify({ userID: 'owned-sdk-analytics-device', migrationVersion: 11, customApiKeyResponses: { approved: ['fake-sdk-analytics-key'.slice(-20)], rejected: [] } }))
}
fs.writeFileSync(filePath, content)
const absent = [...absentManagedSettingsFixturePaths(), '/Library/Application Support/ClaudeCode/managed-mcp.json', '/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token', '/usr/bin/rg', '/bin/rg']
const managed = '/Library/Application Support/ClaudeCode'
for (const leaf of ['CLAUDE.md', '.claude/skills', '.claude/rules']) {
  const path = join(managed, leaf)
  if (fs.existsSync(path)) throw new Error('Managed fixture absence no longer physical: ' + path)
  absent.push(path)
}
for (const spelling of new Set([home, fs.realpathSync(home)])) for (let directory = dirname(spelling); ; directory = dirname(directory)) {
  for (const leaf of ['.git', '.mcp.json', '.claude/skills', '.claude/commands', '.claude/workflows', '.claude/rules', '.agents/skills', '.agents/commands', 'CLAUDE.md', '.claude/CLAUDE.md', 'CLAUDE.local.md', 'AGENTS.md', '.claude/AGENTS.md']) if (!fs.existsSync(join(directory, leaf))) absent.push(join(directory, leaf))
  if (dirname(directory) === directory) break
}
for (const path of ['/sys/hypervisor/uuid', '/.dockerenv']) if (!fs.existsSync(path)) absent.push(path)
const actualRealpath = fs.realpathSync
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent, loopbackFetchOrigin: input.origin })
const guardedRealpath = fs.realpathSync, systemMetadataReads: string[] = []
// Read's original permission boundary canonicalizes the system temp symlink.
// This admits only that exact synchronous OS metadata primitive, not any
// directory contents, descendant path, write, URL or file descriptor.
const metadataRealpath = ((path: any, options?: any) => {
  if (path === '/tmp') { systemMetadataReads.push(path); return actualRealpath(path, options) }
  return guardedRealpath(path, options)
}) as typeof fs.realpathSync
spyOn(fs, 'realpathSync').mockImplementation(metadataRealpath)
spyOn(fsExports, 'realpathSync').mockImplementation(metadataRealpath)
mock.module('../../../src/utils/settings/mdm/rawRead', () => ({ startMdmRawRead: () => {}, getMdmRawReadPromise: () => Promise.resolve({ plistStdouts: [], hklmStdout: null, hkcuStdout: null }), fireRawRead: async () => ({ plistStdouts: [], hklmStdout: null, hkcuStdout: null }) }))
mock.module('../../../src/utils/secureStorage/keychainPrefetch', () => ({ getLegacyApiKeyPrefetchResult: () => ({ stdout: '' }), getLegacyApiKeyPrefetchPromise: () => undefined, startKeychainPrefetch: () => {}, clearLegacyApiKeyPrefetch: () => {}, ensureKeychainPrefetchCompleted: async () => {} }))
;(globalThis as any).MACRO = { VERSION: '999.0.0-sdk-analytics', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
process.chdir(project)
const state = await import('../../../src/bootstrap/state')
state.setCwdState(project); state.setOriginalCwd(project); state.setProjectRoot(project); state.setIsInteractive(false); state.setAllowedSettingSources(['userSettings'])
;(await import('../../../src/utils/config')).enableConfigs()
const permissions = await import('../../../src/utils/permissions/permissionSetup')
const permission = await permissions.initializeToolPermissionContext({ allowedToolsCli: ['Read'], disallowedToolsCli: [], baseToolsCli: ['Read'], permissionMode: 'default', allowDangerouslySkipPermissions: false, addDirs: [] })
const { getDefaultAppState } = await import('../../../src/state/AppStateStore')
const { createStore } = await import('../../../src/state/store')
const store = createStore({ ...getDefaultAppState(), toolPermissionContext: permission.toolPermissionContext })
const analytics = await import('../../../src/services/analytics/index')
const metadata = await import('../../../src/services/analytics/metadata')
const logger = await import('../../../src/services/analytics/firstPartyEventLogger')
const axios = (await import('axios')).default
const originalPost = axios.post.bind(axios)
spyOn(axios, 'post').mockImplementation((target: any, body: any, options: any) => {
  if (target !== 'https://api.anthropic.com/api/event_logging/batch') throw new Error('Unowned actual first-party exporter target')
  return originalPost(input.origin + '/events', body, { ...options, adapter: 'fetch' })
})
const metadataJobs: Promise<unknown>[] = [], calls: string[] = []
const originalMetadata = metadata.getEventMetadata
spyOn(metadata, 'getEventMetadata').mockImplementation((...args) => { const result = originalMetadata(...args); metadataJobs.push(result); return result })
const originalLog = analytics.logEvent
spyOn(analytics, 'logEvent').mockImplementation((...args) => { const result = originalLog(...args); calls.push(args[0]); return result })
await (await import('../../../src/entrypoints/init')).init()
;(await import('../../../src/utils/sinks')).initSinks()
let settingsResponse: unknown, completeSettings!: () => void, completeResult!: () => void
const settingsDone = new Promise<void>(resolve => { completeSettings = resolve })
const resultDone = new Promise<void>(resolve => { completeResult = resolve })
const { Stream } = await import('../../../src/utils/stream')
const enqueue = Stream.prototype.enqueue
spyOn(Stream.prototype, 'enqueue').mockImplementation(function (message) {
  const result = enqueue.call(this, message)
  if (message?.type === 'control_response' && message.response.request_id === 'sdk-analytics-settings') { settingsResponse = message.response; completeSettings() }
  if (message?.type === 'result') completeResult()
  return result
})
const alias = await import('src/services/analytics/index')
process.on('exit', code => {
  fs.writeFileSync(join(home, 'sdk-analytics-source.json'), JSON.stringify({ code, project, initial, nextRequested, nextPhysical, filePath, contentUnchanged: fs.readFileSync(filePath, 'utf8') === content, settingsResponse, finalEnvironment: process.env.CLAUDE_CONFIG_DIR, calls, sameAnalyticsModule: alias === analytics, caps, systemMetadataReads, scope: 'Whole original init/sinks -> StructuredIO SDK environment -> get_settings -> runHeadless/query -> real FileReadTool -> actual same-process logger/exporter and original graceful exit. Only a synchronous exit receipt observes completed work; fake OS credentials and exact loopback HTTP primitives only. Not unwrapped CLI/cold timing evidence.' }))
})
const { runHeadless } = await import('../../../src/cli/print')
await runHeadless((async function* () {
    yield JSON.stringify({ type: 'update_environment_variables', variables: { CLAUDE_CONFIG_DIR: nextRequested } }) + '\n'
    yield JSON.stringify({ type: 'control_request', request_id: 'sdk-analytics-settings', request: { subtype: 'get_settings' } }) + '\n'
    await settingsDone
    if (process.env.CLAUDE_CONFIG_DIR !== nextRequested) throw new Error('Original StructuredIO did not apply environment')
    yield JSON.stringify({ type: 'user', message: { role: 'user', content: 'Read the owned file: ' + filePath } }) + '\n'
    await resultDone
})(), store.getState, store.setState, [], [], {}, [], {
    verbose: true, outputFormat: 'stream-json', includePartialMessages: false, replayUserMessages: false,
    userSpecifiedModel: 'claude-sonnet-4-5-20250929', thinkingConfig: { type: 'disabled' }, maxTurns: 3,
    allowedTools: ['Read'], enableAuthStatus: false,
  } as Parameters<typeof runHeadless>[7])
