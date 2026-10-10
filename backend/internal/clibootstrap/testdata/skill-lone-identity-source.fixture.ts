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
if(input.compactionOnly) {
 const { restoreSkillStateFromMessages }=await import('../../../../src/utils/conversationRecovery')
 const { createSkillAttachmentIfNeeded }=await import('../../../../src/services/compact/compact')
 const snapshot=()=>[...state.getInvokedSkills()].map(([key,value])=>({key,value}))
 Date.now=()=>9000
 restoreSkillStateFromMessages(input.history)
 const snapshots=[snapshot()]
 for(const call of input.calls){Date.now=()=>call.Now;state.addInvokedSkill(call.Name,call.Path,call.Content,call.Agent||null);snapshots.push(snapshot())}
 const attachment=createSkillAttachmentIfNeeded()
 if(!attachment || attachment.attachment.type!=='invoked_skills' || attachment.attachment.skills.length!==4)throw new Error('Original agent scope or four preserved skills missing')
 state.clearInvokedSkills()
 Date.now=()=>20000
 restoreSkillStateFromMessages([attachment])
 const restored=snapshot()
 if(capabilities.blocked.length)throw new Error('Capability denied:'+capabilities.blocked.join(','))
 process.stdout.write(JSON.stringify({snapshots,attachment,restored,capabilities:{blocked:capabilities.blocked}})+'\n')
 process.exit(0)
}
const { initBundledSkills } = await import('../../../../src/skills/bundled/index')
initBundledSkills()
const { getCommands, getCommand } = await import('../../../../src/commands')
const { SkillTool } = await import('../../../../src/tools/SkillTool/SkillTool')
const commands = await getCommands(input.cwd)

process.env.CLAUDE_CODE_DISABLE_ATTACHMENTS = '1'
const { getEmptyToolPermissionContext } = await import('../../../../src/Tool')
let permission = { ...getEmptyToolPermissionContext(), mode: 'bypassPermissions' as const }
if (input.denied) {
 const { initializeToolPermissionContext } = await import('../../../../src/utils/permissions/permissionSetup')
 permission = (await initializeToolPermissionContext({ allowedToolsCli: [], disallowedToolsCli: [], permissionMode: 'default', allowDangerouslySkipPermissions: false, addDirs: [] })).toolPermissionContext as any
}
const context: any = { getAppState: () => ({ mcp: { commands: [], tools: [] }, tasks: {}, toolPermissionContext: permission }), setAppState: () => { throw new Error('Unexpected Skill state mutation') }, options: { commands, tools: [SkillTool], agents: [], agentDefinitions: { activeAgents: [] }, mainLoopModel: 'fixture-model' }, messages: [], readFileState: new Map(), dynamicSkillDirTriggers: new Set(), nestedMemoryAttachmentTriggers: new Set(), abortController: new AbortController() }
if (input.permissionOnly) {
 const { hasPermissionsToUseTool } = await import('../../../../src/utils/permissions/permissions')
 const decisions=[]
 for(const name of input.names) decisions.push(await hasPermissionsToUseTool(SkillTool,{skill:name},context,{message:{id:'actual-parent'}} as any,'actual-id'))
 if(capabilities.blocked.length) throw new Error('Capability denied:'+capabilities.blocked.join(','))
 process.stdout.write(JSON.stringify({decisions,permission,capabilities:{blocked:capabilities.blocked}})+'\n')
 process.exit(0)
}
const sourceResults = []
const goResults = []
Date.now = () => 100000
for (let index = 0; index < input.names.length; index++) {
  const name = input.names[index]
  const valid = await SkillTool.validateInput({ skill: name }, context)
  if (!valid.result) throw new Error('Actual Source rejected raw Skill:' + valid.message)
  const normalized = name.trim().replace(/^\//, '')
  const command = getCommand(normalized, commands)
  if (command.type !== 'prompt') throw new Error('Actual Source did not select prompt command')
  const id = 'actual-lone-skill-' + (index + 1 + (input.reload ? 3 : 0))
  const parent: any = { message: { id: 'fixture-parent', content: [{ type: 'tool_use', name: 'Skill', id }] } }
  const { hasPermissionsToUseTool } = await import('../../../../src/utils/permissions/permissions')
  const decision = await hasPermissionsToUseTool(SkillTool, { skill: name }, context, parent, id)
  const blocks = input.goRequests[index + 1].messages.flatMap((message: any) => Array.isArray(message.content) ? message.content : [])
  const beforeBlocks = input.goRequests[index].messages.flatMap((message: any) => Array.isArray(message.content) ? message.content : [])
  const feedback = blocks.find((block: any) => block.type === 'tool_result' && block.tool_use_id === id)
  if (!feedback) throw new Error('Actual Go Skill result missing ' + index)
  if (decision.behavior === 'deny') {
   sourceResults.push({ behavior: 'deny', body: null })
   const countBodies = (list:any[])=>list.filter(block=>block.type==='text' && block.text.includes('PRIVATE_LOOKUP_BODY_')).length
   const body = countBodies(blocks) === countBodies(beforeBlocks) ? null : 'unexpected executed Skill body'
   goResults.push({ behavior: feedback.is_error && feedback.content.includes(decision.message) ? 'deny' : 'allow', body })
   continue
  }
  const result = await SkillTool.call({ skill: name }, context, async () => { throw new Error('Unexpected shell/tool permission execution') }, parent)
  const mapped = SkillTool.mapToolResultToToolResultBlockParam(result.data, id)
  const bodies = result.newMessages!.flatMap((message: any) => message.type === 'user' && Array.isArray(message.message.content) ? message.message.content.filter((block: any) => block.type === 'text').map((block: any) => block.text) : []).filter((text: string) => text.includes('PRIVATE_LOOKUP_BODY_'))
  if (bodies.length !== 1) throw new Error('Source actual Skill body missing')
  sourceResults.push({ name: result.data.commandName, feedback: mapped.content, body: bodies[0], error: null })
  // Request normalization may append a separator to an older adjacent text
  // block. Count the exact complete original body, so a third invocation must
  // add one new instance rather than reusing the earlier request's body.
  const countBody=(list:any[])=>list.filter(block=>block.type==='text').reduce((count,block)=>count+block.text.split(bodies[0]).length-1,0)
  const body = countBody(blocks) === countBody(beforeBlocks) + 1 ? bodies[0] : null
  goResults.push({ name: feedback.is_error ? null : normalized, feedback: feedback.content, body, error: feedback.is_error ? feedback.content : null })
}
const sourceConfig = config.getGlobalConfig()
const invoked = [...state.getInvokedSkills()].map(([key, value]) => ({ key, value }))
if (invoked.length !== (input.denied ? 1 : 2)) throw new Error('Source actual distinct invocation map missing')
const { createSkillAttachmentIfNeeded } = await import('../../../../src/services/compact/compact')
const attachment = createSkillAttachmentIfNeeded()
if (!attachment || attachment.attachment.type !== 'invoked_skills' || attachment.attachment.skills.length !== (input.denied ? 1 : 2)) throw new Error('Original compaction consumer did not preserve both names')
const exactInvocationParity = JSON.stringify(sourceResults) === JSON.stringify(goResults)
const exactUsageParity = JSON.stringify(sourceConfig.skillUsage) === JSON.stringify(input.goConfig.skillUsage)
if (capabilities.blocked.length) throw new Error('Capability denied:' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({ exactInvocationParity, exactUsageParity, sourceResults, goResults, sourceConfig, goConfig: input.goConfig, invoked, attachment, sourcePermission: permission, originalGoRequests: input.goRequests, goNativeCommandMetadata: input.goNativeCommandMetadata, sourceActualCommandNames: commands.filter(command=>command.source==='plugin').map(command=>command.name), capabilities: { blocked: capabilities.blocked } }) + '\n')
