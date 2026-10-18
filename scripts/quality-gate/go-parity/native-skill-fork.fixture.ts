import { mock } from 'bun:test'
import { dirname, join } from 'node:path'
import { realpathSync } from 'node:fs'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.homeDir, 'Forked Skill')
if (process.env.CC_HAHA_NATIVE_FORK_SKILL_FIXTURE !== '1') throw new Error('Missing owned fork fixture')
const absent = absentManagedSettingsFixturePaths()
for (const start of new Set([home, realpathSync(home)])) {
  for (let path = dirname(start); ; path = dirname(path)) {
    for (const relative of ['.git', '.claude/skills', '.claude/commands', '.claude/agents', '.agents/skills', '.agents/commands', '.agents/agents', '.claude/workflows']) absent.push(join(path, relative))
    if (dirname(path) === path) break
  }
}
absent.push('/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absent })
await import('../../../preload')
mock.module('../../../src/utils/secureStorage/keychainPrefetch', () => ({ getLegacyApiKeyPrefetchResult: () => ({ stdout: '' }), getLegacyApiKeyPrefetchPromise: () => undefined, startKeychainPrefetch: () => {}, clearLegacyApiKeyPrefetch: () => {} }))
// Reuse the existing native-skill-tool fixture's private MDM path priming.
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = join(home, 'managed')
const { getManagedFilePath } = await import('../../../src/utils/settings/managedPath')
if (getManagedFilePath() !== join(home, 'managed')) throw new Error('Unowned managed path')
process.env.USER_TYPE = 'external'
process.env.CLAUDE_CODE_USE_NATIVE_FILE_SEARCH = '1'
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(input.cwd); bootstrap.setOriginalCwd(input.cwd); bootstrap.setProjectRoot(input.cwd); bootstrap.setIsInteractive(false)
bootstrap.setAllowedSettingSources(['userSettings', 'projectSettings', 'localSettings', 'policySettings'])
process.chdir(input.cwd)
const config = await import('../../../src/utils/config'); config.enableConfigs()
const originalQuery = { ...await import('../../../src/query') }
const { createAssistantMessage, createUserMessage } = await import('../../../src/utils/messages')
const observations: any[] = []
let completed = 0
// The single model query boundary is scripted. Both actual entrypoint bodies,
// prepareForkedCommandContext, runAgent, permission/agent selection, transcript
// writes and generator finally remain Source. This is not live/HTTP evidence.
mock.module('../../../src/query', () => ({ ...originalQuery, query: async function* (params: any) {
  const state = params.toolUseContext.getAppState()
  observations.push({ prompt: params.messages.at(-1)?.message?.content, querySource: params.querySource,
    commandRules: state.toolPermissionContext.alwaysAllowRules.command ?? [],
    denyRules: state.toolPermissionContext.alwaysDenyRules, effort: state.effortValue,
    toolNames: params.toolUseContext.options.tools.map((tool: any) => tool.name) })
  try {
    yield createAssistantMessage({ content: [{ type: 'tool_use', id: 'source-fork-write', name: 'Write', input: { file_path: 'source-contract-only.txt', content: 'scripted query boundary' } }] })
    yield createUserMessage({ content: [{ type: 'tool_result', tool_use_id: 'source-fork-write', content: 'scripted query result' }] })
    yield createAssistantMessage({ content: 'FORK_RESULT_TEXT' })
  } finally { completed++ }
} }))
const { getDefaultAppState } = await import('../../../src/state/AppStateStore')
const { createFileStateCacheWithSizeLimit } = await import('../../../src/utils/fileStateCache')
const { SkillTool } = await import('../../../src/tools/SkillTool/SkillTool')
const { getCommands } = await import('../../../src/commands')
const { processSlashCommand } = await import('../../../src/utils/processUserInput/processSlashCommand')
const { FileWriteTool } = await import('../../../src/tools/FileWriteTool/FileWriteTool')
const { FileReadTool } = await import('../../../src/tools/FileReadTool/FileReadTool')
const { getAgentDefinitionsWithOverrides } = await import('../../../src/tools/AgentTool/loadAgentsDir')
const definitions = await getAgentDefinitionsWithOverrides(input.cwd)
if (!definitions.activeAgents.some(agent => agent.agentType === 'fork-worker')) throw new Error('Actual source agent discovery missed physical fork-worker')
let app = { ...getDefaultAppState(), effortValue: 'high' }
// Only public/tool qualifies parent approval; other body-only profiles keep
// their original settings and do not claim that external approval boundary.
if (input.requireParentSkillPermission === true) {
  const { loadAllPermissionRulesFromDisk } = await import('../../../src/utils/permissions/permissionsLoader')
  const { applyPermissionRulesToPermissionContext } = await import('../../../src/utils/permissions/permissions')
  app = { ...app, toolPermissionContext: applyPermissionRulesToPermissionContext(app.toolPermissionContext, loadAllPermissionRulesFromDisk()) }
  if (app.toolPermissionContext.mode !== 'default' || JSON.stringify(app.toolPermissionContext.alwaysAllowRules.userSettings) !== JSON.stringify(['Skill(forkprobe)'])) throw new Error('Source parent Skill rule was not loaded from precise private settings')
}
const initialParentRules = JSON.stringify(app.toolPermissionContext)
const context: any = { options: { commands: await getCommands(input.cwd), debug: false, mainLoopModel: 'fixture-model', tools: [SkillTool,FileWriteTool,FileReadTool], verbose: false, thinkingConfig: { type: 'disabled' }, mcpClients: [], mcpResources: {}, isNonInteractiveSession: false, agentDefinitions: definitions }, abortController: new AbortController(), readFileState: createFileStateCacheWithSizeLimit(), getAppState: () => app, setAppState: (update: any) => { app = update(app) }, setResponseLength: () => {}, messages: [], dynamicSkillDirTriggers: new Set(), nestedMemoryAttachmentTriggers: new Set() }
const slash = await processSlashCommand('/forkprobe ARG_TOKEN', [], [], [], context, () => {})
const parent: any = { message: { id: 'source-parent', content: [{ type: 'tool_use', name: 'Skill', id: 'source-skill' }] } }
const progress: any[] = []
let parentSkillPermission: any = null
if (input.requireParentSkillPermission === true) {
  parentSkillPermission = await SkillTool.checkPermissions({ skill: 'forkprobe', args: 'ARG_TOKEN' }, context)
  const rule = parentSkillPermission.decisionReason?.rule
  if (parentSkillPermission.behavior !== 'allow' || parentSkillPermission.updatedInput?.skill !== 'forkprobe' || parentSkillPermission.updatedInput?.args !== 'ARG_TOKEN' || parentSkillPermission.decisionReason?.type !== 'rule' || rule?.source !== 'userSettings' || rule?.ruleBehavior !== 'allow' || rule?.ruleValue?.toolName !== 'Skill' || rule?.ruleValue?.ruleContent !== 'forkprobe') throw new Error('Actual Source Skill parent approval did not match the exact private allow rule')
}
const tool = await SkillTool.call(parentSkillPermission?.updatedInput ?? { skill: 'forkprobe', args: 'ARG_TOKEN' }, context, async () => ({ behavior: 'allow' } as any), parent, event => { progress.push(event) })
const storage = await import('../../../src/utils/sessionStorage'); await storage.flushSessionStorage()
if (capabilities.blocked.length) throw new Error('Source fork fixture left owned boundaries: ' + JSON.stringify(capabilities.blocked))
const strip = (message: any) => { const { uuid, timestamp, ...rest } = message; return rest }
const { agentId, ...data } = tool.data as any
if (!agentId || completed !== 2 || slash.shouldQuery !== false || data.status !== 'forked') throw new Error('Original fork bodies did not run both child queries')
process.stdout.write(JSON.stringify({ slash: { messages: slash.messages.map(strip), shouldQuery: slash.shouldQuery, resultText: slash.resultText }, tool: { data, agentIdPresent: !!agentId, hasNewMessages: !!tool.newMessages?.length, hasContextModifier: !!tool.contextModifier }, progress: progress.map(event => ({ toolUseID: event.toolUseID, type: event.data.type, messageType: event.data.message.type, agentIdPresent: !!event.data.agentId })), observations, completed, parentSkillPermission, parentPermissionUnchanged: initialParentRules === JSON.stringify(app.toolPermissionContext), capabilities }))
