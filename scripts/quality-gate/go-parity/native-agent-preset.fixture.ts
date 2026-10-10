import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
import { spyOn, mock } from 'bun:test'
import { dirname, join } from 'node:path'
import { realpathSync } from 'node:fs'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual Agent preset lifecycle')
if (process.env.CC_HAHA_NATIVE_AGENT_PRESET_FIXTURE !== '1') throw new Error('Missing Agent preset fixture')
const absent = [...absentManagedSettingsFixturePaths(), '/Library/Application Support/ClaudeCode/managed-mcp.json']
if (input.cwd) for (const parent of [dirname(input.cwd), dirname(realpathSync(input.cwd))]) for (let root = parent; ; root = dirname(root)) {
  absent.push(join(root, '.mcp.json'), join(root, '.git'))
  if (root === dirname(root)) break
}
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absent })
mock.module('../../../src/utils/secureStorage/keychainPrefetch', () => ({ getLegacyApiKeyPrefetchResult: () => ({ stdout: '' }), getLegacyApiKeyPrefetchPromise: () => undefined, startKeychainPrefetch: () => {}, clearLegacyApiKeyPrefetch: () => {} }))
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(input.cwd ?? input.homeDir)
bootstrap.setOriginalCwd(input.cwd ?? input.homeDir)
bootstrap.setProjectRoot(input.cwd ?? input.homeDir)
bootstrap.setIsInteractive(false)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const commands = await import('../../../src/commands')
const client = await import('../../../src/services/mcp/client')
const settings = await import('../../../src/utils/settings/settings')
const { getDefaultAppState } = await import('../../../src/state/AppStateStore')
const { createFileStateCacheWithSizeLimit } = await import('../../../src/utils/fileStateCache')
const { asSystemPrompt } = await import('../../../src/utils/systemPromptType')
const { runAgent, initializeAgentMcpServers } = await import('../../../src/tools/AgentTool/runAgent')
const skillRows = []
for (const row of input.skills ?? []) {
  const started: string[] = []
  let release!: () => void
  const barrier = new Promise<void>(resolve => { release = resolve })
  const definitions = row.definitions.map((d: any) => ({ type: 'prompt', ...d, ...(d.userFacingName ? { userFacingName: () => d.userFacingName } : {}), getPromptForCommand: async (_args: string, ctx: any) => {
    started.push(d.name)
    if (started.length === row.expectedStarts) release()
    await barrier
    if (d.fail) throw new Error(d.fail)
    return [{ type: 'text', text: `${d.name}|${ctx.getAppState().toolPermissionContext.mode}|${ctx.options.mainLoopModel}` }]
  } }))
  const discovery = spyOn(commands, 'getSkillToolCommands').mockResolvedValue(definitions)
  const definition: any = { agentType: row.agentType, source: 'projectSettings', whenToUse: 'Preset fixture', getSystemPrompt: () => 'Preset fixture', skills: row.names }
  const state = getDefaultAppState()
  state.toolPermissionContext.mode = 'bypassPermissions'
  const context: any = { options: { commands: [], tools: [], mainLoopModel: 'fixture-model', mcpClients: [], mcpResources: {}, isNonInteractiveSession: false, agentDefinitions: { activeAgents: [definition], allAgents: [definition] } }, abortController: new AbortController(), readFileState: createFileStateCacheWithSizeLimit(), getAppState: () => state, setAppState: () => {}, setResponseLength: () => {}, messages: [] }
  let result: unknown[] = [], failure: string | null = null
  const stop = new Error('Actual prequery preset captured')
  const generator = runAgent({ agentDefinition: definition, promptMessages: [], toolUseContext: context, canUseTool: async () => ({ behavior: 'allow' }) as any, isAsync: false, querySource: 'agent:custom', availableTools: [], override: { agentId: 'actual-preset-fixture' as any, userContext: {}, systemContext: {}, systemPrompt: asSystemPrompt([]) }, onCacheSafeParams: params => { result = params.forkContextMessages.map(m => ({ type: m.type, message: (m as any).message, isMeta: (m as any).isMeta })); throw stop } })
  try { await generator.next() } catch (error) { if (error !== stop) failure = (error as Error).message }
  discovery.mockRestore()
  skillRows.push({ messages: result, failure, started })
}
const mcpRows = []
for (const row of input.mcp ?? []) {
  const calls: any[] = [], cleaned: string[] = []
  const policy = spyOn(settings, 'getSettingsForSource').mockImplementation(((source: string) => source === 'policySettings' ? { strictPluginOnlyCustomization: row.policy } : null) as any)
  const connect = spyOn(client, 'connectToServer').mockImplementation((async (name: string, config: any) => { calls.push({ name, config }); return { type: row.statuses?.[name] ?? 'connected', name, config, cleanup: async () => { cleaned.push(name) } } }) as any)
  const fetch = spyOn(client, 'fetchToolsForClient').mockImplementation((async (peer: any) => [{ name: `mcp__${peer.name}__echo` }]) as any)
  const parent: any[] = [{ type: 'connected', name: 'parent' }]
  let result: any, failure = null
  try {
    result = await initializeAgentMcpServers({ agentType: 'preset', source: row.source, mcpServers: row.specs } as any, parent)
    await result.cleanup()
  } catch (error) { failure = (error as Error).message }
  mcpRows.push({ calls, clients: result?.clients.map((c: any) => ({ name: c.name, status: c.type })) ?? [], tools: result?.tools.map((t: any) => t.name) ?? [], cleaned, failure })
  policy.mockRestore(); connect.mockRestore(); fetch.mockRestore()
}
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ skillRows, mcpRows, capabilities }), () => resolve()))
process.exit(0)
