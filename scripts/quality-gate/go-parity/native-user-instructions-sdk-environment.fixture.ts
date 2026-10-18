import fs from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { mock, spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual SDK user-instructions environment')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_USER_INSTRUCTIONS_SDK_ENVIRONMENT !== '1') throw new Error('Explicit pinned SDK user-instructions fixture required')
const origin = new URL(input.origin)
if (origin.origin !== input.origin || origin.hostname !== '127.0.0.1' || origin.protocol !== 'http:' || !origin.port || process.env.ANTHROPIC_BASE_URL !== input.origin || process.env.ANTHROPIC_API_KEY !== 'offline-fixture-key') throw new Error('Exact disposable loopback model origin required')
if (process.env.CLAUDE_CODE_SIMPLE !== undefined || process.env.CLAUDE_CODE_DISABLE_AUTO_MEMORY !== undefined || process.env.CLAUDE_CODE_DISABLE_CLAUDE_MDS !== undefined) throw new Error('Original default instruction and memory gates required')
const project = join(home, 'owned-sdk-instructions-project')
const initial = join(home, '.claude')
const nextRequested = 'owned-sdk-instructions-next'
const nextPhysical = resolve(project, nextRequested)
for (const [directory, marker] of [[initial, 'INITIAL'], [nextPhysical, 'NEXT']] as const) {
  fs.mkdirSync(join(directory, 'rules'), { recursive: true })
  fs.writeFileSync(join(directory, 'settings.json'), JSON.stringify({ future: { owned: true, directory: marker } }) + '\n')
  fs.writeFileSync(join(directory, 'CLAUDE.md'), `ACTUAL_SDK_USER_${marker}\n`)
  fs.writeFileSync(join(directory, 'rules', 'ordinary.md'), `ACTUAL_SDK_RULE_${marker}\n`)
}
const absent = absentManagedSettingsFixturePaths()
const externalManaged = dirname(absent[0]!)
absent.push(join(externalManaged, 'CLAUDE.md'), join(externalManaged, '.claude/rules'), join(externalManaged, '.claude/skills'), join(externalManaged, 'managed-mcp.json'))
for (const spelling of new Set([home, fs.realpathSync(home)])) {
  for (let directory = dirname(spelling);; directory = dirname(directory)) {
    const outputStyles = join(directory, '.claude/output-styles')
    try {
      fs.lstatSync(outputStyles)
      throw new Error('External output-styles fixture path is not physically absent')
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error
    }
    for (const leaf of ['.git', '.mcp.json', '.claude/skills', '.claude/commands', '.claude/workflows', '.claude/rules', '.claude/output-styles', '.agents/skills', '.agents/commands', 'CLAUDE.md', '.claude/CLAUDE.md', 'CLAUDE.local.md', 'AGENTS.md', '.claude/AGENTS.md']) absent.push(join(directory, leaf))
    if (dirname(directory) === directory) break
  }
}
absent.push('/usr/bin/rg', '/bin/rg', join(dirname(process.execPath), 'rg'))
for (const path of ['/home/claude/.claude/remote/.oauth_token', '/home/claude/.claude/remote/.api_key']) {
  if (fs.existsSync(path)) throw new Error('External remote credential path is not physically absent')
  absent.push(path)
}
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent, loopbackFetchOrigin: input.origin })
mock.module('../../../src/utils/settings/mdm/rawRead', () => ({
  startMdmRawRead: () => {},
  getMdmRawReadPromise: () => Promise.resolve({ plistStdouts: [], hklmStdout: null, hkcuStdout: null }),
  fireRawRead: async () => ({ plistStdouts: [], hklmStdout: null, hkcuStdout: null }),
}))
;(globalThis as any).MACRO = { VERSION: '999.0.0-sdk-instruction-path', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
process.chdir(project)
const state = await import('../../../src/bootstrap/state')
state.setCwdState(project)
state.setOriginalCwd(project)
state.setProjectRoot(project)
state.setIsInteractive(false)
state.setAllowedSettingSources(['userSettings'])
;(await import('../../../src/utils/config')).enableConfigs()
const permissions = await import('../../../src/utils/permissions/permissionSetup')
const permission = await permissions.initializeToolPermissionContext({ allowedToolsCli: [], disallowedToolsCli: [], baseToolsCli: [''], permissionMode: 'default', allowDangerouslySkipPermissions: false, addDirs: [] })
const { getDefaultAppState } = await import('../../../src/state/AppStateStore')
const { createStore } = await import('../../../src/state/store')
const store = createStore({ ...getDefaultAppState(), toolPermissionContext: permission.toolPermissionContext })
const operations = (await import('../../../src/utils/fsOperations')).getFsImplementation()
const originalRead = operations.readFile.bind(operations)
const originalMkdir = operations.mkdir.bind(operations)
const events: Array<{ phase: string; path?: string; directory?: string; value?: string }> = []
const reads: string[] = [], mkdirs: string[] = []
let notifyRead!: () => void, notifyMkdir!: () => void, releaseRead!: () => void, releaseMkdir!: () => void
const readEntered = new Promise<void>(resolve => { notifyRead = resolve })
const mkdirEntered = new Promise<void>(resolve => { notifyMkdir = resolve })
const readRelease = new Promise<void>(resolve => { releaseRead = resolve })
const mkdirRelease = new Promise<void>(resolve => { releaseMkdir = resolve })
let heldRead = false, heldMkdir = false, autoDirectory: string | undefined
operations.readFile = ((...args: Parameters<typeof operations.readFile>) => {
  const path = String(args[0])
  reads.push(path)
  const result = originalRead(...args)
  if (path === join(initial, 'CLAUDE.md') && !heldRead) {
    heldRead = true
    return result.then(async body => {
      events.push({ phase: 'User native read completed and held', path })
      notifyRead()
      await readRelease
      events.push({ phase: 'User completion released', path })
      return body
    })
  }
  return result
}) as typeof operations.readFile
operations.mkdir = ((...args: Parameters<typeof operations.mkdir>) => {
  const path = String(args[0])
  mkdirs.push(path)
  const result = originalMkdir(...args)
  if (!heldMkdir && resolve(path).startsWith(join(initial, 'projects') + '/') && /\/memory\/?$/.test(path)) {
    heldMkdir = true
    autoDirectory = path
    return result.then(async value => {
      events.push({ phase: 'Memory native mkdir completed and held', path })
      notifyMkdir()
      await mkdirRelease
      events.push({ phase: 'Memory completion released', path })
      return value
    })
  }
  return result
}) as typeof operations.mkdir
let completeFirst!: () => void, completeSecond!: () => void
const first = new Promise<void>(resolve => { completeFirst = resolve })
const second = new Promise<void>(resolve => { completeSecond = resolve })
let results = 0
let completeSettings!: () => void
const settingsCompleted = new Promise<void>(resolve => { completeSettings = resolve })
let settingsResponse: unknown
const { Stream } = await import('../../../src/utils/stream')
const enqueue = Stream.prototype.enqueue
spyOn(Stream.prototype, 'enqueue').mockImplementation(function (message) {
  const result = enqueue.call(this, message)
  if (message?.type === 'result') {
    results++
    events.push({ phase: 'actual result', value: String(results) })
    if (results === 1) completeFirst()
    if (results === 2) completeSecond()
  }
  if (message?.type === 'control_response' && message.response.request_id === 'sdk-directory-settings') {
    settingsResponse = JSON.parse(JSON.stringify(message.response))
    events.push({ phase: 'actual SDK settings response' })
    completeSettings()
  }
  return result
})
const protectedPaths = [join(initial, 'CLAUDE.md'), join(initial, 'rules', 'ordinary.md'), join(nextPhysical, 'CLAUDE.md'), join(nextPhysical, 'rules', 'ordinary.md')]
const before = protectedPaths.map(path => ({ path, content: fs.readFileSync(path, 'utf8') }))
process.on('exit', code => {
  fs.writeFileSync(join(home, 'sdk-user-instructions-environment-result.json'), JSON.stringify({ code, home, project, initial, nextRequested, nextPhysical, autoDirectory, heldRead, heldMkdir, results, settingsResponse, events, reads, mkdirs, autoDirectoryExists: autoDirectory !== undefined && fs.statSync(autoDirectory).isDirectory(), finalEnvironment: process.env.CLAUDE_CONFIG_DIR, protectedFilesUnchanged: before.every(file => fs.readFileSync(file.path, 'utf8') === file.content), caps }))
})
const { runHeadless } = await import('../../../src/cli/print')
await runHeadless((async function* () {
  yield JSON.stringify({ type: 'user', message: { role: 'user', content: 'SDK_INSTRUCTIONS_FIRST_HUMAN' } }) + '\n'
  await Promise.all([readEntered, mkdirEntered])
  events.push({ phase: 'both original concurrent producer completions held', directory: process.env.CLAUDE_CONFIG_DIR })
  yield JSON.stringify({ type: 'update_environment_variables', variables: { CLAUDE_CONFIG_DIR: nextRequested } }) + '\n'
  if (process.env.CLAUDE_CONFIG_DIR !== nextRequested) throw new Error('Actual StructuredIO did not apply the SDK environment message')
  events.push({ phase: 'actual SDK environment message consumed', directory: process.env.CLAUDE_CONFIG_DIR })
  if (input.verifySettings) {
    yield JSON.stringify({ type: 'control_request', request_id: 'sdk-directory-settings', request: { subtype: 'get_settings' } }) + '\n'
    await settingsCompleted
  }
  releaseMkdir()
  releaseRead()
  await first
  yield JSON.stringify({ type: 'user', message: { role: 'user', content: 'SDK_INSTRUCTIONS_SECOND_HUMAN' } }) + '\n'
  await second
})(), store.getState, store.setState, [], [], {}, [], {
  verbose: true, outputFormat: 'stream-json', includePartialMessages: false, replayUserMessages: false,
  userSpecifiedModel: 'claude-sonnet-4-5-20250929', thinkingConfig: { type: 'disabled' }, maxTurns: 1,
  allowedTools: [], enableAuthStatus: false,
} as Parameters<typeof runHeadless>[7])
