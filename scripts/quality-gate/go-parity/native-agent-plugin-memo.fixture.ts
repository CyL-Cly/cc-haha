import fs from 'node:fs'
import { dirname, join, relative } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual plugin Agent skill memo consumer')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_PLUGIN_AGENT_MEMO !== '1') throw new Error('Pinned plugin Agent memo guard required')
if (!input.plugin || relative(home, input.plugin).startsWith('..')) throw new Error('Plugin must remain in private home')
fs.mkdirSync(join(home, 'bin'), { recursive: true })
process.env.PATH = join(home, 'bin')
const absent = absentManagedSettingsFixturePaths()
const managedSkills = join(dirname(absent[0]!), '.claude', 'skills')
if (!fs.existsSync(managedSkills)) absent.push(managedSkills)
const packagedRipgrep = join(dirname(process.execPath), process.platform === 'win32' ? 'rg.exe' : 'rg')
if (!fs.existsSync(packagedRipgrep)) absent.push(packagedRipgrep)
for (const spelling of new Set([home, fs.realpathSync(home)])) {
  for (let parent = spelling; ; parent = dirname(parent)) {
    for (const suffix of ['.git', '.claude/settings.json', '.claude/settings.local.json', '.claude/agents', '.agents/agents', '.claude/skills', '.claude/commands', '.claude/workflows', '.agents/skills', '.mcp.json']) {
      const path = join(parent, suffix)
      if (!fs.existsSync(path)) absent.push(path)
    }
    if (dirname(parent) === parent) break
  }
}
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absent })
;(globalThis as any).MACRO = { VERSION: '999.0.0-agent-memo', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(input.cwd)
bootstrap.setOriginalCwd(input.cwd)
bootstrap.setProjectRoot(input.cwd)
bootstrap.setIsInteractive(false)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const commands = await import('../../../src/commands')
const { loadPluginAgents } = await import('../../../src/utils/plugins/loadPluginAgents')
const warm = await commands.getCommands(input.cwd)
let definition = (await loadPluginAgents()).find(agent => agent.agentType === 'sample:worker')
if (!definition || !warm.some(command => command.name === 'sample:sample')) throw new Error('Actual parent plugin warmup premise missing: '+JSON.stringify({definition:definition?.agentType,commands:warm.map(command=>command.name),plugins:await(await import('../../../src/utils/plugins/pluginLoader')).loadAllPluginsCacheOnly()}))
for (const row of input.files) {
  const path = join(input.plugin, row.path)
  if (relative(input.plugin, path).startsWith('..')) throw new Error('Late file escaped plugin')
  fs.mkdirSync(dirname(path), { recursive: true })
  fs.writeFileSync(path, row.content)
}
const { getDefaultAppState } = await import('../../../src/state/AppStateStore')
const { createFileStateCacheWithSizeLimit } = await import('../../../src/utils/fileStateCache')
const { asSystemPrompt } = await import('../../../src/utils/systemPromptType')
const { runAgent } = await import('../../../src/tools/AgentTool/runAgent')
let state = getDefaultAppState()
state.toolPermissionContext.mode = 'bypassPermissions'
const rows = []
for (const reloaded of [false, true]) {
  if (reloaded) {
    const { refreshActivePlugins } = await import('../../../src/utils/plugins/refresh')
    await refreshActivePlugins(update => { state = update(state) })
    definition = (await loadPluginAgents()).find(agent => agent.agentType === 'sample:worker')
    if (!definition) throw new Error('Original reload lost the installed Agent')
  }
  let captured: unknown[] | undefined
  const context: any = { options: { commands: warm, tools: [], mainLoopModel: 'fixture-model', mcpClients: [], mcpResources: {}, isNonInteractiveSession: false, agentDefinitions: { activeAgents: [definition], allAgents: [definition] } }, abortController: new AbortController(), readFileState: createFileStateCacheWithSizeLimit(), getAppState: () => state, setAppState: (update: any) => { state = update(state) }, setResponseLength: () => {}, messages: [] }
  const stop = new Error('Original Agent prequery boundary observed')
  const generator = runAgent({ agentDefinition: definition, promptMessages: [], toolUseContext: context, canUseTool: async () => ({ behavior: 'allow' }) as any, isAsync: false, querySource: 'agent:custom', availableTools: [], override: { agentId: 'actual-plugin-memo-agent' as any, userContext: {}, systemContext: {}, systemPrompt: asSystemPrompt([]) }, onCacheSafeParams: params => { captured = params.forkContextMessages.map(message => ({ type: message.type, message: (message as any).message, isMeta: (message as any).isMeta })); throw stop } })
  try { await generator.next() } catch (error) { if (error !== stop) throw error }
  if (!captured) throw new Error('Actual Agent did not reach prequery skill consumer')
  rows.push({ reloaded, messages: captured })
}
if (capabilities.blocked.length) throw new Error('Actual Agent memo escaped fixture: ' + JSON.stringify(capabilities.blocked))
process.stdout.write(JSON.stringify({ rows, capabilities }))
process.exit(0)
