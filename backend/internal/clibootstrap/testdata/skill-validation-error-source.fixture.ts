import { join, dirname } from 'node:path'
import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import {
  absentManagedSettingsFixturePaths,
  installClientFixtureCapabilities,
} from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual guide model request UTF16')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_GUIDE_UTF16_FIXTURE !== '1') {
  throw new Error('Owned pinned fixture required')
}
const capabilities = await installClientFixtureCapabilities(home, {
  absentPaths: [
    ...absentManagedSettingsFixturePaths(),
    '/Library/Application Support/ClaudeCode/.claude/skills',
    '/Library/Application Support/ClaudeCode/.claude/agents',
    '/Library/Application Support/ClaudeCode/.claude/output-styles',
    '/usr/bin/rg',
    '/bin/rg',
    join(dirname(process.execPath), 'rg'),
    '/home/claude/.claude/remote/.api_key',
    '/home/claude/.claude/remote/.oauth_token',
  ],
})
mock.module('../../../../src/utils/secureStorage/keychainPrefetch', () => ({
  getLegacyApiKeyPrefetchResult: () => ({ stdout: null }),
  clearLegacyApiKeyPrefetch() {},
  startKeychainPrefetch() { throw new Error('No keychain') },
  ensureKeychainPrefetchCompleted: async () => {},
}))
await import('../../../../preload')
const state = await import('../../../../src/bootstrap/state')
state.setOriginalCwd(home)
state.setCwdState(home)
state.setProjectRoot(home)
state.setAllowedSettingSources(['userSettings'])
const config = await import('../../../../src/utils/config')
config.enableConfigs()
if (typeof input.cwd !== 'string' || !input.cwd.startsWith(home + '/')) throw new Error('Owned cwd required')
if (!Array.isArray(input.directories) || input.directories.some((path: string) => !path.startsWith(home + '/'))) throw new Error('Owned plugin directories required')
state.setOriginalCwd(input.cwd)
state.setCwdState(input.cwd)
state.setProjectRoot(input.cwd)
state.setInlinePlugins(input.directories)
const { initBundledSkills } = await import('../../../../src/skills/bundled/index')
initBundledSkills()
const { getCommands, getCommand } = await import('../../../../src/commands')
const { SkillTool } = await import('../../../../src/tools/SkillTool/SkillTool')
const commands = await getCommands(input.cwd)

process.env.CLAUDE_CODE_DISABLE_ATTACHMENTS = '1'
const { getEmptyToolPermissionContext } = await import('../../../../src/Tool')
const permission = { ...getEmptyToolPermissionContext(), mode: 'bypassPermissions' as const }
const context: any = {
  getAppState: () => ({ mcp: { commands: [], tools: [] }, tasks: {}, toolPermissionContext: permission }),
  setAppState: () => { throw new Error('Unexpected Skill state mutation') },
  options: { commands, tools: [SkillTool], mcpClients: [], agents: [], agentDefinitions: { activeAgents: [] }, mainLoopModel: 'fixture-model' },
  messages: [], readFileState: new Map(), dynamicSkillDirTriggers: new Set(),
  nestedMemoryAttachmentTriggers: new Set(), abortController: new AbortController(),
}
const { runToolUse } = await import('../../../../src/services/tools/toolExecution')
const sourceResults = []
const goResults = []
const validationCodes = []
for (let index = 0; index < input.names.length; index++) {
  const id = 'actual-validation-skill-' + (index + 1)
  const toolUse = { type: 'tool_use', id, name: 'Skill', input: { skill: input.names[index] } }
  const parent: any = { type: 'assistant', uuid: '11111111-1111-4111-8111-111111111111', message: { id: 'actual-parent', role: 'assistant', content: [toolUse] } }
  const validation = await SkillTool.validateInput(toolUse.input, context)
  if (validation.result) throw new Error('Required actual validation rejection absent')
  validationCodes.push(validation.errorCode)
  const updates = []
  for await (const update of runToolUse(toolUse as any, parent, async () => { throw new Error('Unexpected permission/call after validation rejection') }, context)) {
    if (update.message) updates.push(update.message)
  }
  const source = updates.find((message: any) => Array.isArray(message.message?.content) && message.message.content.some((block: any) => block.type === 'tool_result' && block.tool_use_id === id))
  const result = source?.message.content.find((block: any) => block.type === 'tool_result' && block.tool_use_id === id)
  if (!result?.is_error || typeof source.toolUseResult !== 'string') throw new Error('Original validation two error outlets missing')
  sourceResults.push({ content: result.content, is_error: result.is_error, toolUseResult: source.toolUseResult })
  const blocks = input.goRequests[index + 1].messages.flatMap((message: any) => Array.isArray(message.content) ? message.content : [])
  const go = blocks.find((block: any) => block.type === 'tool_result' && block.tool_use_id === id)
  const history = input.goHistory.find((message: any) => Array.isArray(message.message?.content) && message.message.content.some((block: any) => block.type === 'tool_result' && block.tool_use_id === id))
  if (!go?.is_error || typeof history?.toolUseResult !== 'string') throw new Error('Actual Go next request/history validation feedback missing')
  goResults.push({ content: go.content, is_error: go.is_error, toolUseResult: history.toolUseResult })
}
if (capabilities.blocked.length) throw new Error('Capability denied:' + capabilities.blocked.join(','))
process.stdout.write(JSON.stringify({
  exactValidationParity: JSON.stringify(sourceResults) === JSON.stringify(goResults),
  sourceResults, goResults, validationCodes, originalGoRequests: input.goRequests,
  originalGoHistory: input.goHistory,
  actualNonpromptCommands: commands.filter(command => command.name === 'cost').map(command => ({ name: command.name, type: command.type, disableModelInvocation: command.disableModelInvocation })),
  capabilities: { blocked: capabilities.blocked },
}) + '\n')
