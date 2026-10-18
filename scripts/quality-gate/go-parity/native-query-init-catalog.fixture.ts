import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual QueryEngine init catalog')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_QUERY_INIT_CATALOG_FIXTURE !== '1') throw new Error('Pinned private QueryEngine catalog profile required')
const cwd = join(home, 'project')
const absent = absentManagedSettingsFixturePaths()
for (const spelling of new Set([home, fs.realpathSync(home)])) for (let path = dirname(spelling); ; path = dirname(path)) {
  for (const leaf of ['.git', '.claude/skills', '.claude/workflows', '.claude/commands', '.claude/rules', '.claude/output-styles', '.agents/skills', 'CLAUDE.md', '.claude/CLAUDE.md', 'CLAUDE.local.md', 'AGENTS.md', '.claude/AGENTS.md']) {
    const candidate = join(path, leaf)
    if (!fs.existsSync(candidate)) absent.push(candidate)
  }
  if (dirname(path) === path) break
}
for (const path of ['/Library/Application Support/ClaudeCode/CLAUDE.md', '/Library/Application Support/ClaudeCode/.claude/rules', '/Library/Application Support/ClaudeCode/.claude/skills', '/home/claude/.claude/remote/.oauth_token', '/home/claude/.claude/remote/.api_key']) if (!fs.existsSync(path)) absent.push(path)
const managedRoot = process.platform === 'darwin' ? '/Library/Application Support/ClaudeCode' : process.platform === 'win32' ? 'C:\\Program Files\\ClaudeCode' : '/etc/claude-code'
for (const path of [join(managedRoot, '.claude', 'commands'), join(managedRoot, '.claude', 'skills')]) {
  try { fs.lstatSync(path) } catch (error: any) { if (error.code !== 'ENOENT') throw error; absent.push(path) }
}
for (const path of [join(dirname(process.execPath), 'rg'), '/usr/bin/rg', '/bin/rg']) if (!fs.existsSync(path)) absent.push(path)
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent, loopbackFetchOrigin: input.peer })
;(globalThis as any).MACRO = { VERSION: '999.0.0-init-catalog', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(cwd)
bootstrap.setOriginalCwd(cwd)
bootstrap.setProjectRoot(cwd)
bootstrap.setIsInteractive(false)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const commands = await import('../../../src/commands')
const plugins = await import('../../../src/utils/plugins/pluginLoader')
if (input.pluginFlagProfile) {
  process.env.CLAUDE_CODE_SYNC_PLUGIN_INSTALL = input.pluginFlagProfile.first ? '1' : '0'
  const first = plugins.loadAllPluginsCacheOnly()
  const full = input.pluginFlagProfile.first ? plugins.loadAllPlugins() : undefined
  process.env.CLAUDE_CODE_SYNC_PLUGIN_INSTALL = input.pluginFlagProfile.first ? '0' : '1'
  const sameBeforeCompletion = plugins.loadAllPluginsCacheOnly() === first
  await first
  const current = plugins.loadAllPluginsCacheOnly()
  await current
  const result = { sameBeforeCompletion, wrapperDistinct: !full || first !== full, replacedByFullWarm: input.pluginFlagProfile.first ? current !== first && current !== full : current === first, blocked: caps.blocked }
  if (caps.blocked.length) throw new Error('Plugin flag profile escaped private capabilities: ' + JSON.stringify(caps.blocked))
  await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify(result), error => error ? reject(error) : resolve()))
  process.exit(0)
}
// Actual startup loads, followed by the original public cache invalidation.
// No command/plugin result or loader Promise is replaced by the fixture.
await commands.getCommands(cwd)
const beforePlugin = plugins.loadAllPluginsCacheOnly()
await beforePlugin
const beforeSkills = input.warmOriginal ? undefined : commands.getSlashCommandToolSkills(cwd)
if (beforeSkills) await beforeSkills
const warmSkillsSame = beforeSkills ? commands.getSlashCommandToolSkills(cwd) === beforeSkills : false
if (input.cacheLifecycle) {
  fs.writeFileSync(input.skill, '---\ndescription: After cache edit\n---\nChanged real skill body.')
  const warmDescription = (await commands.getSlashCommandToolSkills(cwd)).find(command => command.name === 'held')?.description
  commands.clearCommandsCache()
  const clearedSkills = commands.getSlashCommandToolSkills(cwd)
  const clearDescription = (await clearedSkills).find(command => command.name === 'held')?.description
  const secondCwd = input.secondCwd as string
  bootstrap.setCwdState(secondCwd)
  const otherSkills = commands.getSlashCommandToolSkills(secondCwd)
  const otherNames = (await otherSkills).map(command => command.name)
  bootstrap.setCwdState(cwd)
  const firstCwdPromiseSame = commands.getSlashCommandToolSkills(cwd) === clearedSkills
  const full = plugins.loadAllPlugins()
  await full
  const warmedPlugin = plugins.loadAllPluginsCacheOnly()
  await warmedPlugin
  const fullWarmDistinct = warmedPlugin !== beforePlugin && warmedPlugin !== full
  const slashAfterFullSame = commands.getSlashCommandToolSkills(cwd) === clearedSkills
  const fullWarmSame = plugins.loadAllPlugins() === full
  plugins.clearPluginCache('native-query-init-catalog-fixture')
  const clearedPlugin = plugins.loadAllPluginsCacheOnly()
  await clearedPlugin
  const result = { warmSkillsSame, warmDescription, clearDescription, otherNames, otherCwdDistinct: otherSkills !== clearedSkills, firstCwdPromiseSame, clearedSkillsDifferent: clearedSkills !== beforeSkills, fullWarmDistinct, fullWarmSame, slashAfterFullSame, pluginClearDifferent: clearedPlugin !== warmedPlugin, blocked: caps.blocked }
  if (caps.blocked.length) throw new Error('Catalog lifecycle escaped private capabilities: ' + JSON.stringify(caps.blocked))
  await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify(result), error => error ? reject(error) : resolve()))
  process.exit(0)
}
if (input.dualFIFO) {
  commands.clearCommandsCache()
  plugins.clearPluginCache('native-query-init-dual-fifo')
  fs.renameSync(input.pluginFifo, input.pluginPath)
  fs.renameSync(input.fifo, input.skill)
  fs.writeFileSync(input.armed, 'ready')
  // The original loadAllCommands/getSkills fan-out, not a copied caller. Both
  // actual readFile operations remain held by the external private FIFO owner.
  const definitions = await commands.getCommands(cwd)
  const names = definitions.map(command => command.name)
  const result = { names, blocked: caps.blocked }
  if (caps.blocked.length) throw new Error('Dual FIFO command load escaped private capabilities: ' + JSON.stringify(caps.blocked))
  await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify(result), error => error ? reject(error) : resolve()))
  process.exit(0)
}
if (!input.warmOriginal) {
  commands.clearCommandsCache()
  fs.renameSync(input.fifo, input.skill)
  fs.writeFileSync(input.armed, 'ready')
}
const { getDefaultAppState } = await import('../../../src/state/AppStateStore')
const { initializeToolPermissionContext } = await import('../../../src/utils/permissions/permissionSetup')
const permission = await initializeToolPermissionContext({ allowedToolsCli: [], disallowedToolsCli: [], baseToolsCli: [''], permissionMode: 'default', allowDangerouslySkipPermissions: false, addDirs: [] })
let state = { ...getDefaultAppState(), toolPermissionContext: permission.toolPermissionContext }
const { QueryEngine } = await import('../../../src/QueryEngine')
const storage = await import('../../../src/utils/sessionStorage')
const engine = new QueryEngine({ initialMessages: [], cwd, tools: [], commands: [], mcpClients: [], agents: [], readFileCache: new Map() as any,
  customSystemPrompt: 'private actual init catalog', userSpecifiedModel: 'fixture-model', thinkingConfig: { type: 'disabled' }, maxTurns: 2,
  canUseTool: async () => ({ behavior: 'allow', updatedInput: {} }), getAppState: () => state, setAppState: update => { state = update(state) },
} as any)
const yielded: any[] = []
const originalRecord = storage.recordTranscript
let observed = false
const sameRecordPromise: boolean[] = []
const slashCalls: { afterRecord: boolean; samePromise: boolean }[] = []
const originalSlash = (commands.getSlashCommandToolSkills as any).getMockImplementation?.() ?? commands.getSlashCommandToolSkills
const slashSpy = input.warmOriginal ? spyOn(commands, 'getSlashCommandToolSkills').mockImplementation((...args: any[]) => {
  const promise = originalSlash(...args)
  slashCalls.push({ afterRecord: observed, samePromise: Promise.resolve(promise) === promise })
  return promise
}) : undefined
const recordSpy = spyOn(storage, 'recordTranscript').mockImplementation((...args: any[]) => {
  const promise = (originalRecord as any)(...args)
  sameRecordPromise.push(Promise.resolve(promise) === promise)
  if (!observed) {
    observed = true
    // One real queueMicrotask observer from the original Record call. The
    // FIFO is held externally; this neither supplies IO nor delays init.
    if (!input.warmOriginal) void promise.then(() => { queueMicrotask(() => fs.writeFileSync(input.probe, JSON.stringify({ init: yielded.some(value => value.type === 'system' && value.subtype === 'init') }))) })
  }
  return promise
})
try {
  for await (const value of engine.submitMessage('ACTUAL_INIT_CATALOG_USER')) yielded.push(value)
} finally { recordSpy.mockRestore(); slashSpy?.mockRestore() }
await storage.flushSessionStorage()
const afterSkills = commands.getSlashCommandToolSkills(cwd)
await afterSkills
const pluginSame = plugins.loadAllPluginsCacheOnly() === beforePlugin
const result = { init: yielded.find(value => value.type === 'system' && value.subtype === 'init'), warmSkillsSame, clearedSkillsDifferent: afterSkills !== beforeSkills, pluginSame, sameRecordPromise, slashCalls, warmOriginal: !!input.warmOriginal, blocked: caps.blocked }
if (caps.blocked.length) throw new Error('Catalog fixture escaped private capabilities: ' + JSON.stringify(caps.blocked))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify(result), error => error ? reject(error) : resolve()))
process.exit(0)
