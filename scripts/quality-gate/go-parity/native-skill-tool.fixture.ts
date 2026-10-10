import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
import { mock } from 'bun:test'
import { dirname, join } from 'node:path'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const writeJSON = (value: unknown) => new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify(value), error => error ? reject(error) : resolve()))
const home = assertDisposableFixtureHome(input.homeDir, 'Native Skill tool')
if (process.env.CC_HAHA_NATIVE_SKILL_TOOL_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Unowned Skill tool fixture')
const hasResourcePeer = !!(input.resourceMentions || input.resourceToolCalls || input.resourceParallel)
const capabilities = await installClientFixtureCapabilities(home, { ...(hasResourcePeer ? {loopbackFetchOrigin: new URL(input.mcpURL).origin} : {}), absentPaths: [...absentManagedSettingsFixturePaths(), ...(hasResourcePeer ? ['/home/claude/.claude/remote/.session_ingress_token'] : []), ...(input.models || input.calls || input.slashes || input.bundles || input.flow || input.fileMentions || hasResourcePeer ? ['/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token'] : []), ...(input.calls || input.slashes || input.flow || input.fileMentions || hasResourcePeer ? [join(dirname(process.execPath), 'rg'), '/usr/bin/rg', '/bin/rg'] : [])] })
if (input.models || input.calls || input.slashes || input.bundles || input.flow || input.fileMentions || hasResourcePeer) {
  // The sole replaced boundary is the absent native keychain-prefetch result.
  // Direct credentials still go through the original model/auth/settings code.
  mock.module('../../../src/utils/secureStorage/keychainPrefetch', () => ({ getLegacyApiKeyPrefetchResult: () => ({ stdout: '' }), getLegacyApiKeyPrefetchPromise: () => undefined, startKeychainPrefetch: () => {}, clearLegacyApiKeyPrefetch: () => {} }))
}
;(globalThis as any).MACRO = { BUILD_TIME: '' }
if (input.resourceParsers) {
  const { ListMcpResourcesTool } = await import('../../../src/tools/ListMcpResourcesTool/ListMcpResourcesTool')
  const { ReadMcpResourceTool } = await import('../../../src/tools/ReadMcpResourceTool/ReadMcpResourceTool')
  const { toolToAPISchema } = await import('../../../src/utils/api')
  const tools = [ListMcpResourcesTool, ReadMcpResourceTool]
  const schemas = await Promise.all(tools.map(tool => toolToAPISchema(tool, {tools,agents:[],model:'deepseek-flash',getToolPermissionContext: async() => (await import('../../../src/Tool')).getEmptyToolPermissionContext()})))
  const resourceParsers = input.resourceParsers.map((row: any) => {
    const tool = tools.find(tool=>tool.name===row.name)!
    const result = tool.inputSchema.safeParse(JSON.parse(row.input))
    return result.success ? {success:true,data:result.data} : {success:false,issues:result.error.issues}
  })
  await writeJSON({schemas,resourceParsers,capabilities})
  process.exit(0)
}
if (input.resourceEmpty) {
  const { ListMcpResourcesTool } = await import('../../../src/tools/ListMcpResourcesTool/ListMcpResourcesTool')
  const { ReadMcpResourceTool } = await import('../../../src/tools/ReadMcpResourceTool/ReadMcpResourceTool')
  const context: any = {options:{mcpClients:[]}}
  const resourceEmpty = []
  for (const tool of [ListMcpResourcesTool,ReadMcpResourceTool]) {
    try {
      const result = await tool.call(tool.name===ListMcpResourcesTool.name?{}:{server:'missing',uri:'scheme:actual'} as any,context)
      resourceEmpty.push({name:tool.name,data:result.data,mapped:tool.mapToolResultToToolResultBlockParam(result.data as any,'tool-id')})
    } catch (error:any) {resourceEmpty.push({name:tool.name,error:error.message})}
  }
  await writeJSON({resourceEmpty,capabilities})
  process.exit(0)
}
if (input.mentions) {
  const { extractAtMentionedFiles, extractMcpResourceMentions, extractAgentMentions, parseAtMentionedFileLines } = await import('../../../src/utils/attachments')
  const mentions = input.mentions.map((text: string) => {
    const files = extractAtMentionedFiles(text)
    return { files, resources: extractMcpResourceMentions(text), agents: extractAgentMentions(text), parsed: files.map(parseAtMentionedFileLines) }
  })
  await writeJSON({ mentions, capabilities })
  process.exit(0)
}
if (input.toolLists) {
  const { parseToolListFromCLI } = await import('../../../src/utils/permissions/permissionSetup')
  await writeJSON({ toolLists: input.toolLists.map((row: string) => parseToolListFromCLI(JSON.parse(row))), capabilities })
  process.exit(0)
}
if (input.shellMatches) {
  const { executeShellCommandsInPrompt } = await import('../../../src/utils/promptShellExecution')
  const shellMatches = []
  for (const text of input.shellMatches) {
    const commands: string[] = []
    let failure: string | undefined
    try {
      await executeShellCommandsInPrompt(text, {} as any, '/fixture', 'bash', { availability: { bash: true, powershell: false }, checkPermission: async (_tool, input) => { commands.push((input as any).command); return { behavior: 'deny', message: 'parser observer denies before execution' } } })
    } catch (error: any) { failure = error.message }
    shellMatches.push({ commands, failure })
  }
  await writeJSON({ shellMatches, capabilities })
  process.exit(0)
}
if (input.listings) {
  const { formatCommandsWithinBudget } = await import('../../../src/tools/SkillTool/prompt')
  const listings = input.listings.map((row: any) => {
    process.env.SLASH_COMMAND_TOOL_CHAR_BUDGET = row.budget ?? ''
    return formatCommandsWithinBudget(JSON.parse(row.commands), row.contextWindow)
  })
  await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ listings, capabilities }), error => error ? reject(error) : resolve()))
  process.exit(0)
}
const { SkillTool } = await import('../../../src/tools/SkillTool/SkillTool')
if (input.permissions || input.resourcePermissions || input.usages || input.models || input.calls || input.slashes || input.bundles || input.flow || input.fileMentions || hasResourcePeer) {
  process.env.USER_TYPE = 'ant'
  process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = `${home}/managed`
  const { getManagedFilePath } = await import('../../../src/utils/settings/managedPath')
  if (getManagedFilePath() !== `${home}/managed`) throw new Error('Unowned managed path')
  process.env.USER_TYPE = 'external'
  const state = await import('../../../src/bootstrap/state')
  if (input.calls || input.slashes || input.flow || input.fileMentions || hasResourcePeer) process.chdir(input.cwd)
  const cwd = input.calls || input.slashes || input.flow || input.fileMentions || hasResourcePeer ? process.cwd() : home
  state.setCwdState(cwd); state.setOriginalCwd(cwd); state.setProjectRoot(cwd)
  state.setAllowedSettingSources(['userSettings', 'projectSettings', 'localSettings', 'policySettings'])
  state.setIsInteractive(true)
  const config = await import('../../../src/utils/config')
  config.enableConfigs()
  if (hasResourcePeer) {
    const { connectToServer, fetchResourcesForClient, clearServerCache } = await import('../../../src/services/mcp/client')
    const { getEmptyToolPermissionContext } = await import('../../../src/Tool')
    const { getAttachments } = await import('../../../src/utils/attachments')
    if (input.resourceParallel) {
      const { ListMcpResourcesTool } = await import('../../../src/tools/ListMcpResourcesTool/ListMcpResourcesTool')
      const configurations = input.resourceParallel.map((row:any)=>({type:'http',url:row.url,scope:'user'} as const))
      const clients = await Promise.all(input.resourceParallel.map((row:any,i:number)=>connectToServer(row.name,configurations[i])))
      const context: any = { options:{mcpClients:clients} }
      const result = await ListMcpResourcesTool.call({},context)
      await Promise.all(input.resourceParallel.map((row:any,i:number)=>clearServerCache(row.name,configurations[i])))
      await writeJSON({resourceParallel:result.data,capabilities})
      process.exit(0)
    }
    const configuration = {type:'http', url:input.mcpURL, scope:'user'} as const
    const client = await connectToServer('fixture', configuration)
    if (client.type !== 'connected') throw new Error('Original resource peer did not connect: ' + client.type)
    const resources = await fetchResourcesForClient(client)
    const context: any = { getAppState: () => ({toolPermissionContext:getEmptyToolPermissionContext(),mcp:{commands:[]},tasks:{}}), setAppState:()=>{throw new Error('Unexpected state mutation')}, agentId:'observer', readFileState:new Map(), dynamicSkillDirTriggers:new Set(), nestedMemoryAttachmentTriggers:new Set(), options:{ tools:[], mcpClients:[client], mcpResources:{fixture:resources}, agents:[], agentDefinitions:{activeAgents:[]},mainLoopModel:'fixture-model'},messages:[],abortController:new AbortController() }
    if (input.resourceToolCalls) {
      const { ListMcpResourcesTool } = await import('../../../src/tools/ListMcpResourcesTool/ListMcpResourcesTool')
      const { ReadMcpResourceTool } = await import('../../../src/tools/ReadMcpResourceTool/ReadMcpResourceTool')
      const resourceToolCalls = []
      for (const row of input.resourceToolCalls) {
        const tool = row.name === ListMcpResourcesTool.name ? ListMcpResourcesTool : ReadMcpResourceTool
        const parsed = tool.inputSchema.parse(JSON.parse(row.input))
        try {
          const result = await tool.call(parsed as any,context)
          resourceToolCalls.push({data:result.data,mapped:tool.mapToolResultToToolResultBlockParam(result.data as any,'tool-id')})
        } catch (error: any) { resourceToolCalls.push({error:error.message}) }
      }
      await clearServerCache('fixture',configuration)
      await writeJSON({resourceToolCalls,capabilities})
      process.exit(0)
    }
    const resourceMentions = []
    for (const text of input.resourceMentions) resourceMentions.push((await getAttachments(text,context,null,[],[],'repl_main_thread')).filter(attachment=>attachment.type==='mcp_resource'))
    await clearServerCache('fixture',configuration)
    await writeJSON({ resourceMentions, capabilities })
    process.exit(0)
  }
  if (input.fileMentions) {
    const { getEmptyToolPermissionContext } = await import('../../../src/Tool')
    const { getAttachmentsForTesting } = await import('../../../src/utils/attachments')
    const { FileReadTool } = await import('../../../src/tools/FileReadTool/FileReadTool')
    const permission = { ...getEmptyToolPermissionContext(), alwaysDenyRules: {session: ['Read(secret.txt)']} }
    const context: any = { getAppState: () => ({ toolPermissionContext: permission, mcp: {commands: []} }), readFileState: new Map(), dynamicSkillDirTriggers: new Set(), nestedMemoryAttachmentTriggers: new Set(), options: { tools: [SkillTool,FileReadTool], agents: [], agentDefinitions: {activeAgents: []}, mainLoopModel:'fixture-model'}, messages:[], abortController: new AbortController() }
    const fileMentions = []
    for (const text of input.fileMentions) fileMentions.push(await getAttachmentsForTesting.atMentionedFiles(text, context))
    await writeJSON({ fileMentions, capabilities })
    process.exit(0)
  }
  if (input.flow) {
    const { initBundledSkills } = await import('../../../src/skills/bundled/index')
    initBundledSkills()
    const { getEmptyToolPermissionContext } = await import('../../../src/Tool')
    const { getAttachments, resetSentSkillNames } = await import('../../../src/utils/attachments')
    const { addSkillDirectories } = await import('../../../src/skills/loadSkillsDir')
    if (input.resumePath) {
      const { loadMessagesFromJsonlPath, restoreSkillStateFromMessages } = await import('../../../src/utils/conversationRecovery')
      const restored = await loadMessagesFromJsonlPath(input.resumePath)
      restoreSkillStateFromMessages(restored.messages as any)
    }
    const context: any = { getAppState: () => ({ toolPermissionContext: getEmptyToolPermissionContext(), mcp: { commands: [] }, tasks: {} }), setAppState: () => { throw new Error('Unexpected state mutation') }, abortController: new AbortController(), options: { tools: [SkillTool], mcpClients: [], agents: [], agentDefinitions: {activeAgents: []}, mainLoopModel: 'fixture-model' }, messages: [], readFileState: new Map(), nestedMemoryAttachmentTriggers: new Set(), dynamicSkillDirTriggers: new Set() }
    const flow = []
    for (const row of input.flow) {
      if (row.reset) resetSentSkillNames()
      if (row.loadDirs) await addSkillDirectories(row.loadDirs)
      process.env.NODE_ENV = row.nodeEnv ?? 'production'
      process.env.CLAUDE_CODE_SIMPLE = row.simple ?? ''
      process.env.CLAUDE_CODE_DISABLE_ATTACHMENTS = row.disabled ?? ''
      context.agentId = row.agent || undefined
      context.options.tools = row.skill === false ? [] : [SkillTool]
      context.dynamicSkillDirTriggers = new Set(row.dirs)
      const attachments = await getAttachments(null, context, null, [], [], 'repl_main_thread')
      flow.push({ attachments: attachments.filter(value => value.type === 'dynamic_skill' || value.type === 'skill_listing'), triggers: [...context.dynamicSkillDirTriggers] })
    }
    await writeJSON({ flow, capabilities })
    process.exit(0)
  }
  if (input.bundles) {
    const { initBundledSkills } = await import('../../../src/skills/bundled/index')
    const { getBundledSkills, clearBundledSkills } = await import('../../../src/skills/bundledSkills')
    const bundles = []
    for (const row of input.bundles) {
      for (const key of ['CC_HAHA_IMAGE_PROVIDER_KIND','CC_HAHA_IMAGE_PROVIDER_ID','CC_HAHA_IMAGE_MODEL','CC_HAHA_IMAGE_BASE_URL','CC_HAHA_IMAGE_API_KEY']) delete process.env[key]
      Object.assign(process.env, row.env)
      clearBundledSkills(); initBundledSkills()
      const commands = getBundledSkills().filter(command => !command.isEnabled || command.isEnabled()).filter(command => command.name !== 'computer-use')
      const prompts = []
      for (const call of row.calls) {
        const command = commands.find(command => command.name === call.name)
        prompts.push((await command!.getPromptForCommand(call.args, {} as any))[0])
      }
      bundles.push({ metadata: commands, prompts })
    }
    await writeJSON({ bundles, capabilities })
    process.exit(0)
  }
  if (input.slashes) {
    process.env.CLAUDE_CODE_DISABLE_ATTACHMENTS = '1'
    const { getEmptyToolPermissionContext } = await import('../../../src/Tool')
    const { getCommands } = await import('../../../src/commands')
    const { processSlashCommand } = await import('../../../src/utils/processUserInput/processSlashCommand')
    const context: any = { getAppState: () => ({ toolPermissionContext: getEmptyToolPermissionContext(), mcp: { commands: [] }, effortValue: 'high' }), setAppState: () => { throw new Error('Unexpected Skill state setter') }, abortController: new AbortController(), options: { commands: await getCommands(cwd), tools: [SkillTool], agents: [], agentDefinitions: {activeAgents: []}, mainLoopModel: 'opus[1m]' }, messages: [] }
    const slashes = []
    for (const text of input.slashes) {
      const result = await processSlashCommand(text, [], [], [], context, () => { throw new Error('Unexpected JSX local command') })
      slashes.push({ messages: result.messages.map((m: any) => { const {uuid,timestamp,...rest}=m; return rest }), shouldQuery: result.shouldQuery, model: result.model, effort: result.effort, allowedTools: result.allowedTools })
    }
    await writeJSON({ slashes, capabilities })
    process.exit(0)
  }
  if (input.calls) {
    process.env.CLAUDE_CODE_DISABLE_ATTACHMENTS = '1'
    const { getEmptyToolPermissionContext } = await import('../../../src/Tool')
    const context: any = { getAppState: () => ({ toolPermissionContext: { ...getEmptyToolPermissionContext(), mode: 'default' }, mcp: { commands: [] }, effortValue: 'high' }), setAppState: () => { throw new Error('Unexpected Skill state setter') }, abortController: new AbortController(), options: { tools: [SkillTool], agents: [], agentDefinitions: {activeAgents: []}, mainLoopModel: 'opus[1m]' }, messages: [] }
    const parent: any = { message: { id: 'fixture-parent', content: [{type:'tool_use',name:'Skill',id:'first-skill-id'},{type:'tool_use',name:'Skill',id:'second-skill-id'}] } }
    const calls = []
    for (const row of input.calls) {
      const parsed = SkillTool.inputSchema.parse(JSON.parse(row.input))
      const validation = await SkillTool.validateInput(parsed, context)
      if (!validation.result) { calls.push({ validation }); continue }
      const result = await SkillTool.call(parsed, context, async () => { throw new Error('Unexpected outer permission execution') }, parent)
      const modified = result.contextModifier?.(context) ?? context
      calls.push({ validation, data: result.data, messages: result.newMessages?.map((m: any) => { const {uuid,timestamp,...rest}=m; return rest }), model: modified.options.mainLoopModel, effort: modified.getAppState().effortValue, commandRules: modified.getAppState().toolPermissionContext.alwaysAllowRules.command ?? [] })
    }
    await writeJSON({ calls, capabilities })
    process.exit(0)
  }
  if (input.models) {
    const { resolveSkillModelOverride } = await import('../../../src/utils/model/model')
    const { modelSupports1M } = await import('../../../src/utils/context')
    const models = input.models.map((row: any) => {
      process.env.CLAUDE_CODE_DISABLE_1M_CONTEXT = row.disabled ?? ''
      return { result: resolveSkillModelOverride(row.model, row.current), supports: modelSupports1M(row.model) }
    })
    await writeJSON({ models, capabilities })
    process.exit(0)
  }
  if (input.usages) {
    const { recordSkillUsage } = await import('../../../src/utils/suggestions/skillUsageTracking')
    const usages = []
    for (const row of input.usages) {
      Date.now = () => row.now
      recordSkillUsage(row.name)
      usages.push(config.getGlobalConfig().skillUsage)
    }
    await writeJSON({ usages, config: config.getGlobalConfig(), capabilities })
    process.exit(0)
  }
  process.env.CLAUDE_CODE_SIMPLE = '1'
  const { getEmptyToolPermissionContext } = await import('../../../src/Tool')
  const { hasPermissionsToUseTool } = await import('../../../src/utils/permissions/permissions')
  if (input.resourcePermissions) {
    const { ListMcpResourcesTool } = await import('../../../src/tools/ListMcpResourcesTool/ListMcpResourcesTool')
    const { ReadMcpResourceTool } = await import('../../../src/tools/ReadMcpResourceTool/ReadMcpResourceTool')
    const resourcePermissions = []
    for (const row of input.resourcePermissions) {
      const tool = row.name === ListMcpResourcesTool.name ? ListMcpResourcesTool : ReadMcpResourceTool
      const app = { toolPermissionContext: {...getEmptyToolPermissionContext(), ...row.permission}, mcp: {commands:[]} }
      const context: any = { getAppState:()=>app, setAppState:(update:any)=>Object.assign(app,update(app)), abortController:new AbortController(), options:{tools:[ListMcpResourcesTool,ReadMcpResourceTool],agents:[],mainLoopModel:'deepseek-flash'},messages:[] }
      const parsed = tool.inputSchema.parse(JSON.parse(row.input))
      resourcePermissions.push(await hasPermissionsToUseTool(tool,parsed,context,{message:{id:'source-parent'}} as any,'source-tool'))
    }
    await writeJSON({resourcePermissions,capabilities})
    process.exit(0)
  }
  const permissions = []
  for (const row of input.permissions) {
    const command = JSON.parse(row.command)
    const app = { toolPermissionContext: { ...getEmptyToolPermissionContext(), ...row.permission }, mcp: { commands: [command] } }
    const context: any = { getAppState: () => app, setAppState: (update: any) => Object.assign(app, update(app)), abortController: new AbortController(), options: { tools: [SkillTool], agents: [], mainLoopModel: 'deepseek-flash' }, messages: [] }
    const parsed = SkillTool.inputSchema.parse(JSON.parse(row.input))
    permissions.push(await hasPermissionsToUseTool(SkillTool, parsed, context, { message: {id:'source-parent'} } as any, 'source-tool'))
  }
  await writeJSON({ permissions, capabilities })
  process.exit(0)
}
const { toolToAPISchema } = await import('../../../src/utils/api')
const schema = await toolToAPISchema(SkillTool, { tools: [SkillTool], agents: [], model: 'deepseek-flash', getToolPermissionContext: async () => (await import('../../../src/Tool')).getEmptyToolPermissionContext() })
const parsers = input.parsers.map((row: string) => {
  const parsed = SkillTool.inputSchema.safeParse(JSON.parse(row))
  return parsed.success ? { success: true, data: parsed.data } : { success: false, issues: parsed.error.issues }
})
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ schema, parsers, capabilities }), error => error ? reject(error) : resolve()))
