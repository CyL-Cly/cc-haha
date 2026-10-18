import { mock } from 'bun:test'
import fs from 'node:fs'
import { captureFixedHookSpawn } from './fixed-native-hook-spawn'
const installFixedSpawn = captureFixedHookSpawn()
import { dirname, join } from 'node:path'
import { realpathSync } from 'node:fs'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = await Bun.stdin.json()
if (Bun.version !== '1.3.14') throw new Error('Expected pinned Bun 1.3.14')
const home = assertDisposableFixtureHome(input.homeDir, 'Physical frontmatter hooks')
if (process.env.CC_HAHA_NATIVE_FRONTMATTER_HOOKS_FIXTURE !== '1') throw new Error('Missing owned frontmatter hook fixture')
// The paired Go boundary sets the same existing product switch. Git-context
// enrichment is outside this physical hook/query-boundary fixture.
if (process.env.CLAUDE_CODE_DISABLE_GIT_INSTRUCTIONS !== '1') throw new Error('Expected paired private git-context setting')
const absent = absentManagedSettingsFixturePaths()
for (const start of new Set([home, realpathSync(home)])) {
  for (let path = dirname(start); ; path = dirname(path)) {
    for (const relative of ['.git', '.claude/skills', '.claude/commands', '.claude/agents', '.agents/skills', '.agents/commands', '.agents/agents', '.claude/workflows']) absent.push(join(path, relative))
    if (dirname(path) === path) break
  }
}
absent.push('/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absent })
const abortController = new AbortController()
process.env.HOOK_OUTPUT_DIR = input.effects
const native = installFixedSpawn({ home, cwd: input.cwd, effects: input.effects, corpusPath: input.corpusPath, corpusSHA256: input.corpusSHA256, roots: [join(input.cwd, '.claude/skills/hookprobe')], onPhysicalStdout: text => { if (input.profile === 'cancel' && text.includes('HOOK_PHYSICAL_STARTED')) abortController.abort() } })
await import('../../../preload')
mock.module('../../../src/utils/secureStorage/keychainPrefetch', () => ({ getLegacyApiKeyPrefetchResult: () => ({ stdout: '' }), getLegacyApiKeyPrefetchPromise: () => undefined, startKeychainPrefetch: () => {}, clearLegacyApiKeyPrefetch: () => {} }))
// Reuse the existing native-skill-tool fixture's private MDM path priming.
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = join(home, 'managed')
const { getManagedFilePath } = await import('../../../src/utils/settings/managedPath')
if (getManagedFilePath() !== join(home, 'managed')) throw new Error('Unowned managed path')
process.env.USER_TYPE = 'external'
process.env.CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING = '1'
process.env.CLAUDE_CODE_USE_NATIVE_FILE_SEARCH = '1'
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(input.cwd); bootstrap.setOriginalCwd(input.cwd); bootstrap.setProjectRoot(input.cwd); bootstrap.setIsInteractive(false)
bootstrap.setAllowedSettingSources(['userSettings', 'projectSettings', 'localSettings', 'policySettings'])
process.chdir(input.cwd)
const config = await import('../../../src/utils/config'); config.enableConfigs()
const originalQuery = { ...await import('../../../src/query') }
const { createAssistantMessage, createUserMessage } = await import('../../../src/utils/messages')
let runTools: any, executeStopHooks: any
let childQueries = 0, childFinally = 0
const permissionCalls: string[] = [], messages: any[] = [], stopFrames: any[] = []
const canUseTool = async (tool: any, parsed: any) => { permissionCalls.push(tool.name); return { behavior: 'allow', updatedInput: parsed } }
async function physicalWrite(context: any, index: number) {
  const block = { type: 'tool_use', id: `physical-write-${index}`, name: 'Write', input: { file_path: join(input.effects, `artifact-${index}.txt`), content: `PHYSICAL_${index}` } }
  const assistant = createAssistantMessage({ content: [block] })
  context.messages.push(assistant)
  messages.push(assistant)
  for await (const update of runTools([block], [assistant], canUseTool, context)) {
    context = update.newContext
    if (update.message) { context.messages.push(update.message); messages.push(update.message) }
  }
  return context
}
async function physicalStop(context: any, agentId?: any, agentType?: string) {
  for await (const frame of executeStopHooks(context.getAppState().toolPermissionContext.mode, context.abortController.signal, undefined, false, agentId, context, context.messages, agentType)) stopFrames.push(frame)
}
// Only query's model boundary is scripted. The real runAgent registers and
// clears hooks. Every Write goes through real runTools/permission/hooks/IO;
// the model boundary explicitly drives the same real Stop consumer as query.
mock.module('../../../src/query', () => ({ ...originalQuery, query: async function* (params: any) {
  childQueries++
  try {
    let context = params.toolUseContext
    if (input.profile === 'parent-stop-failure') {
      const last: any = createAssistantMessage({ content: 'FAKE_RATE_LIMIT' }); last.error = 'rate_limit'
      context.messages.push(last); yield last
      await hooks.executeStopFailureHooks(last, context)
      return
    }
    for (const index of [1, 2]) {
      const block = { type: 'tool_use', id: `physical-write-${index}`, name: 'Write', input: { file_path: join(input.effects, `artifact-${index}.txt`), content: `PHYSICAL_${index}` } }
      const assistant = createAssistantMessage({ content: [block] }); context.messages.push(assistant); yield assistant
      for await (const update of runTools([block], [assistant], canUseTool, context)) { context = update.newContext; if (update.message) { context.messages.push(update.message); yield update.message } }
    }
    const final = createAssistantMessage({ content: 'HOOK_CHILD_DONE' }); context.messages.push(final); yield final
    await physicalStop(context, context.agentId, 'hook-worker')
  } finally { childFinally++ }
} }))
const { getDefaultAppState } = await import('../../../src/state/AppStateStore')
const { createFileStateCacheWithSizeLimit } = await import('../../../src/utils/fileStateCache')
const { SkillTool } = await import('../../../src/tools/SkillTool/SkillTool')
const { getCommands } = await import('../../../src/commands')
const { processSlashCommand } = await import('../../../src/utils/processUserInput/processSlashCommand')
const { FileWriteTool } = await import('../../../src/tools/FileWriteTool/FileWriteTool')
const { FileReadTool } = await import('../../../src/tools/FileReadTool/FileReadTool')
const { getAgentDefinitionsWithOverrides } = await import('../../../src/tools/AgentTool/loadAgentsDir')
;({ runTools } = await import('../../../src/services/tools/toolOrchestration'))
const hooks = await import('../../../src/utils/hooks')
executeStopHooks = hooks.executeStopHooks
const definitions = await getAgentDefinitionsWithOverrides(input.cwd)
let app = getDefaultAppState()
const { initializeToolPermissionContext } = await import('../../../src/utils/permissions/permissionSetup')
app.toolPermissionContext = (await initializeToolPermissionContext({ allowedToolsCli: [], disallowedToolsCli: [], baseToolsCli: ['Skill,Write,Read,Agent,TaskOutput'], permissionMode: input.permissionMode, allowDangerouslySkipPermissions: input.permissionMode === 'bypassPermissions', addDirs: [] })).toolPermissionContext
const initialParentPermission = JSON.stringify(app.toolPermissionContext)
const context: any = { options: { commands: await getCommands(input.cwd), debug: false, mainLoopModel: 'fixture-model', tools: [SkillTool, FileWriteTool, FileReadTool], verbose: false, thinkingConfig: { type: 'disabled' }, mcpClients: [], mcpResources: {}, isNonInteractiveSession: true, agentDefinitions: definitions }, abortController, readFileState: createFileStateCacheWithSizeLimit(), getAppState: () => app, setAppState: (update: any) => { app = update(app) }, setResponseLength: () => {}, setInProgressToolUseIDs: () => {}, updateFileHistoryState: () => {}, messages: [], dynamicSkillDirTriggers: new Set(), nestedMemoryAttachmentTriggers: new Set() }
let slashShouldQuery: boolean | null = null, toolResultSeen = false, agentSessionCountAfter: number | null = null, parentStopFailureCountAfterChild: number | null = null
let physical: any
const sourceLifecycle: any = { attempted: false, completed: false, before: null, after: null }
function activeSourceResources() {
  const read = (process as any).getActiveResourcesInfo
  if (typeof read !== 'function') return { supported: false, types: [] }
  try {
    const types = read.call(process)
    return { supported: true, types: Array.isArray(types) ? types : [], unexpectedShape: !Array.isArray(types) }
  } catch (error) { return { supported: true, error: String(error), types: [] } }
}
async function finishOwnedSourceSession() {
  if (sourceLifecycle.attempted) return
  sourceLifecycle.attempted = true
  sourceLifecycle.before = activeSourceResources()
  // Same registered-resource consumer as real CLI shutdown, without process exit.
  const { runCleanupFunctions } = await import('../../../src/utils/cleanupRegistry')
  await runCleanupFunctions()
  sourceLifecycle.completed = true
  sourceLifecycle.after = activeSourceResources()
}
try {
  if (input.profile === 'parent-stop-failure') {
    const slash = await processSlashCommand('/hookprobe', [], [], [], context, () => {})
    slashShouldQuery = slash.shouldQuery
    context.messages.push(...slash.messages)
    const definition = definitions.activeAgents.find(agent => agent.agentType === 'plain-worker')
    if (!definition || definition.hooks || definition.skills?.length || definition.mcpServers?.length) throw new Error('Expected physical ordinary Agent without a preset')
    const { runAgent } = await import('../../../src/tools/AgentTool/runAgent')
    for await (const message of runAgent({ agentDefinition: definition, promptMessages: [createUserMessage({ content: 'HOOK_PLAIN_CHILD_INPUT' })], toolUseContext: context, canUseTool, isAsync: false, querySource: 'agent:custom', availableTools: [FileWriteTool, FileReadTool] })) messages.push(message)
    agentSessionCountAfter = app.sessionHooks.size
    parentStopFailureCountAfterChild = app.sessionHooks.get(bootstrap.getSessionId())?.hooks.StopFailure?.flatMap(matcher => matcher.hooks).length ?? 0
    await physicalWrite(context, 3)
  } else if (input.profile === 'agent') {
    const definition = definitions.activeAgents.find(agent => agent.agentType === 'hook-worker')
    if (!definition?.hooks) throw new Error('Actual custom Agent discovery missed frontmatter hooks')
    const { runAgent } = await import('../../../src/tools/AgentTool/runAgent')
    for await (const message of runAgent({ agentDefinition: definition, promptMessages: [createUserMessage({ content: 'HOOK_CHILD_INPUT' })], toolUseContext: context, canUseTool, isAsync: input.background === true, querySource: 'agent:custom', availableTools: [FileWriteTool, FileReadTool] })) messages.push(message)
    agentSessionCountAfter = app.sessionHooks.size
    // Actual parent Write cannot see completed child's declarations.
    await physicalWrite(context, 3)
  } else {
    let current = context
    if (input.entry === 'tool') {
      const block = { type: 'tool_use', id: 'physical-skill', name: 'Skill', input: { skill: 'hookprobe' } }
      const assistant = createAssistantMessage({ content: [block] }); current.messages.push(assistant)
      for await (const update of runTools([block], [assistant], canUseTool, current)) { current = update.newContext; if (update.message) { current.messages.push(update.message); messages.push(update.message); if (JSON.stringify(update.message).includes('hookprobe')) toolResultSeen = true } }
    } else {
      const slash = await processSlashCommand('/hookprobe', [], [], [], current, () => {})
      slashShouldQuery = slash.shouldQuery
      current.messages.push(...slash.messages)
    }
    if (input.profile === 'stop-failure') { const last: any = createAssistantMessage({ content: 'FAKE_RATE_LIMIT' }); last.error = 'rate_limit'; await hooks.executeStopFailureHooks(last, current); await hooks.executeStopFailureHooks(last, current) }
    const count = input.profile === 'stop-failure' ? 0 : input.profile === 'flaky' ? 3 : input.profile === 'deny' || input.profile === 'cancel' ? 1 : 2
    for (let index = 1; index <= count; index++) current = await physicalWrite(current, index)
    if (input.profile !== 'cancel' && input.profile !== 'stop-failure') { current.messages.push(createAssistantMessage({ content: 'HOOK_PARENT_DONE' })); await physicalStop(current) }
  }
  await hooks.executeSessionEndHooks('other', { getAppState: context.getAppState, setAppState: context.setAppState })
  const storage = await import('../../../src/utils/sessionStorage')
  await storage.flushSessionStorage()
  await finishOwnedSourceSession()
  physical = await native.join()
  if (capabilities.blocked.length) throw new Error('Original hook fixture escaped guard: ' + JSON.stringify({ blocked: capabilities.blocked, rejectedNativeRequests: native.rejectedNativeRequests }))
} catch (error) {
  await native.cleanupAfterFailure()
  try { await finishOwnedSourceSession() }
  catch (cleanupError) { throw new AggregateError([error, cleanupError], 'Source consumer and registered cleanup failed') }
  throw error
}
const read = (name: string) => { const path = join(input.effects, name); return fs.existsSync(path) ? fs.readFileSync(path, 'utf8') : null }
const events = (read('events') ?? '').trim().split('\n').filter(Boolean)
const inputs = (read('inputs.jsonl') ?? '').trim().split('\n').filter(Boolean).map(line => JSON.parse(line))
process.stdout.write(JSON.stringify({ profile: input.profile, entry: input.entry, background: input.background ?? false, slashShouldQuery, toolResultSeen, events, inputs, artifacts: [1, 2, 3].map(index => read(`artifact-${index}.txt`)), outsideRoots: (read('outside-roots') ?? '').trim().split('\n').filter(Boolean), permissionCalls, childQueries, childFinally, agentSessionCountAfter, parentStopFailureCountAfterChild, sessionCountAfter: app.sessionHooks.size, parentPermissionUnchanged: initialParentPermission === JSON.stringify(app.toolPermissionContext), aborted: abortController.signal.aborted, physical, stopFrames, messages, capabilities, sourceLifecycle }))
