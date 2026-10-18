import fs from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { mock, spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_SDK_SUGGESTION_BILLING_FIXTURE !== '1') throw new Error('Explicit pinned Source metadata fixture required')
const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(process.env.HOME, 'SDK initialize metadata')
const printPath = fs.realpathSync(fileURLToPath(new URL('../../../src/cli/print.ts', import.meta.url)))
const mainPath = fs.realpathSync(fileURLToPath(new URL('../../../src/main.tsx', import.meta.url)))
const absent=[...absentManagedSettingsFixturePaths(), '/Library/Application Support/ClaudeCode/.claude/skills', '/Library/Application Support/ClaudeCode/.claude/agents', '/Library/Application Support/ClaudeCode/.claude/output-styles', join(process.execPath, '..', 'rg'), '/usr/bin/rg', '/bin/rg', '/home/claude/.claude/remote/.oauth_token','/home/claude/.claude/remote/.api_key']
for(let directory=dirname(home);;directory=dirname(directory)){absent.push(join(directory,'.git'));if(directory===dirname(directory))break}
let activeSuggestionPeer: ((request: Request) => Promise<Response>) | undefined
const suggestionPeer = input.suggestions ? Bun.serve({ hostname: '127.0.0.1', port: 0, fetch(request) {
  if (!activeSuggestionPeer) return new Response('No active private suggestion profile', { status: 404 })
  return activeSuggestionPeer(request)
} }) : undefined
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absent, ...(suggestionPeer ? { loopbackFetchOrigin: suggestionPeer.url.origin } : {}) })
process.once('exit', () => suggestionPeer?.stop(true))
// Only the external credential-store primitive is replaced. Its bytes are the
// disposable physical Source credential fixture, parsed by the real auth code.
const secure = join(home, '.claude', '.credentials.json')
if (input.cachedSubscriber) {
  fs.mkdirSync(dirname(secure),{recursive:true})
  fs.writeFileSync(secure,JSON.stringify({claudeAiOauth:{accessToken:'fake-source-suggestion-subscriber',scopes:['user:inference','user:profile'],expiresAt:9999999999999,subscriptionType:'max'}}))
  delete process.env.ANTHROPIC_API_KEY
}
mock.module(new URL('../../../src/utils/secureStorage/index.ts', import.meta.url).pathname, () => ({ getSecureStorage: () => ({ name: 'private-physical-credential-store', read: () => fs.existsSync(secure) ? JSON.parse(fs.readFileSync(secure, 'utf8')) : null, readAsync: async () => fs.existsSync(secure) ? JSON.parse(fs.readFileSync(secure, 'utf8')) : null }) }))
// Execute original prefetch with a narrowly bound external security primitive.
const processes = await import('node:child_process')
const fakeExecFile = (command: string, args: string[], options: unknown, callback: Function) => {
  if (command !== 'security' || args[0] !== 'find-generic-password') throw new Error('Unbound Source subprocess')
  callback(Object.assign(new Error('private Keychain entry absent'), { code:44 }), '')
  return { on() {}, kill() {} } as any
}
spyOn(processes, 'execFile').mockImplementation(fakeExecFile as any)
const prefetch = await import('../../../src/utils/secureStorage/keychainPrefetch')
prefetch.startKeychainPrefetch()
await prefetch.ensureKeychainPrefetchCompleted()
Object.assign(globalThis, { MACRO: { VERSION:'fixture', BUILD_TIME:'' } })
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setCwdState(process.cwd()); state.setOriginalCwd(process.cwd()); state.setProjectRoot(process.cwd()); state.setAllowedSettingSources(['userSettings']); state.setIsInteractive(false)
const config = await import('../../../src/utils/config'); config.enableConfigs()
const model = await import('../../../src/utils/model/model')
const { getModelOptions } = await import('../../../src/utils/model/modelOptions')
const auth = await import('../../../src/utils/auth')
const effort = await import('../../../src/utils/effort')
const thinking = await import('../../../src/utils/thinking')
const fast = await import('../../../src/utils/fastMode')
const { getAPIProvider } = await import('../../../src/utils/model/providers')
state.setInitialMainLoopModel(input.initial ?? null)
state.setMainLoopModelOverride(input.override)
// Execute the original modelInfos declaration body, supplying its actual
// imported producers. No expected catalog or capability outputs are injected.
const source = fs.readFileSync(printPath, 'utf8')
const begin = source.indexOf('  const modelOptions = getModelOptions()')
const end = source.indexOf('  let activeUserSpecifiedModel', begin)
if (begin < 0 || end < 0) throw new Error('Original initialize modelInfos declaration missing')
const body = new Bun.Transpiler({ loader:'ts' }).transformSync(source.slice(begin,end)+'\nreturn modelInfos\n')
const build = new Function('getModelOptions','feature','getDefaultMainLoopModel','parseUserSpecifiedModel','modelSupportsEffort','modelSupportsAdaptiveThinking','isFastModeSupportedByModel','getSupportedEffortLevelsForModel',body)
const models = build(getModelOptions, (name:string) => name === 'TRANSCRIPT_CLASSIFIER', model.getDefaultMainLoopModel, model.parseUserSpecifiedModel, effort.modelSupportsEffort, thinking.modelSupportsAdaptiveThinking, fast.isFastModeSupportedByModel, effort.getSupportedEffortLevelsForModel)
const { initBuiltinPlugins } = await import('../../../src/plugins/bundled/index')
const { initBundledSkills } = await import('../../../src/skills/bundled/index')
initBuiltinPlugins(); initBundledSkills()
const commandsModule = await import('../../../src/commands')
const headless = await import('../../../src/commands/headless')
const commands = headless.filterCommandsForHeadlessMode(await commandsModule.getCommands(process.cwd())).filter(cmd => cmd.userInvocable !== false).map(cmd => ({name:commandsModule.getCommandName(cmd),description:commandsModule.formatDescriptionWithSource(cmd),argumentHint:cmd.argumentHint || ''}))
const agentsModule = await import('../../../src/tools/AgentTool/loadAgentsDir')
const definitions = await agentsModule.getAgentDefinitionsWithOverrides(process.cwd())
const agents = definitions.activeAgents.map(agent => ({name:agent.agentType,description:agent.whenToUse,...(agent.model !== undefined && agent.model !== 'inherit' ? {model:agent.model}: {})}))
const styles = await import('../../../src/constants/outputStyles')
const outputStyles = Object.keys(await styles.getAllOutputStyles(process.cwd()))
const settings = await import('../../../src/utils/settings/settings')
const outputStyle = settings.getSettings_DEPRECATED()?.outputStyle || styles.DEFAULT_OUTPUT_STYLE_NAME
const accountInfo = auth.getAccountInformation()
const account = {email:accountInfo?.email,organization:accountInfo?.organization,subscriptionType:accountInfo?.subscription,tokenSource:accountInfo?.tokenSource,apiKeySource:accountInfo?.apiKeySource,apiProvider:getAPIProvider()}
// Execute the unchanged original initialize handler with its real producers.
const initializeBegin = source.indexOf('async function handleInitializeRequest(')
const initializeEnd = source.indexOf('async function handleRewindFiles(', initializeBegin)
if (initializeBegin < 0 || initializeEnd < 0) throw new Error('Original SDK initialize consumer missing')
const initializeBody = new Bun.Transpiler({loader:'ts'}).transformSync(source.slice(initializeBegin, initializeEnd) + '\nreturn handleInitializeRequest\n')
const cwd = await import('../../../src/utils/cwd')
const dependencies = {
  AwsAuthStatusManager: (await import('../../../src/utils/awsAuthStatusManager')).AwsAuthStatusManager,
  randomUUID: (await import('node:crypto')).randomUUID,
  getSessionId: state.getSessionId,
  parseAgentsFromJson: agentsModule.parseAgentsFromJson,
  getMainThreadAgentType: state.getMainThreadAgentType,
  setMainThreadAgentType: state.setMainThreadAgentType,
  isBuiltInAgent: agentsModule.isBuiltInAgent,
  parseUserSpecifiedModel: model.parseUserSpecifiedModel,
  setMainLoopModelOverride: state.setMainLoopModelOverride,
  getSettings_DEPRECATED: settings.getSettings_DEPRECATED,
  DEFAULT_OUTPUT_STYLE_NAME: styles.DEFAULT_OUTPUT_STYLE_NAME,
  getAllOutputStyles: styles.getAllOutputStyles,
  getCwd: cwd.getCwd,
  getAccountInformation: auth.getAccountInformation,
  registerHookCallbacks: state.registerHookCallbacks,
  setInitJsonSchema: state.setInitJsonSchema,
  getCommandName: commandsModule.getCommandName,
  formatDescriptionWithSource: commandsModule.formatDescriptionWithSource,
  getAPIProvider,
  isFastModeEnabled: fast.isFastModeEnabled,
  isFastModeAvailable: fast.isFastModeAvailable,
  getFastModeState: fast.getFastModeState,
}
const handle = new Function(...Object.keys(dependencies), initializeBody)(...Object.values(dependencies))
const {StructuredIO} = await import('../../../src/cli/structuredIO')
const io = new StructuredIO((async function*(){})())
const output: any[] = []
const options: any = input.options ?? {}
const allCommands = headless.filterCommandsForHeadlessMode(await commandsModule.getCommands(process.cwd()))
const allAgents = definitions.activeAgents.slice()
if (input.authStatus !== undefined) {
  if (input.sessionId) state.switchSession(input.sessionId)
  await handle({subtype:'initialize'}, 'actual-init', false, {enqueue:(value:any)=>output.push(value)}, allCommands, models, io, input.authStatus, options, allAgents, ()=>({fastMode:false}))
  await handle({subtype:'initialize'}, 'repeated-init', true, {enqueue:(value:any)=>output.push(value)}, allCommands, models, io, input.authStatus, options, allAgents, ()=>({fastMode:false}))
  process.stdout.write(JSON.stringify({output,capabilities})+'\n')
  process.exit(0)
}
if (input.controls) {
  const modelStart=source.indexOf('          const requestedModel = message.request.model')
  const modelEnd=source.indexOf("        } else if (message.request.subtype === 'set_max_thinking_tokens')",modelStart)
  const thinkingStart=source.indexOf('          if (message.request.max_thinking_tokens === null)',modelEnd)
  const thinkingEnd=source.indexOf("        } else if (message.request.subtype === 'mcp_status')",thinkingStart)
  if([modelStart,modelEnd,thinkingStart,thinkingEnd].some(index=>index<0))throw new Error('Original SDK inference consumers missing')
  const runModel=new Function('message','getDefaultMainLoopModel','setMainLoopModelOverride','notifySessionMetadataChanged','injectModelSwitchBreadcrumbs','sendControlResponseSuccess',new Bun.Transpiler({loader:'ts'}).transformSync('let activeUserSpecifiedModel\n'+source.slice(modelStart,modelEnd)))
  const runThinking=new Function('message','options','sendControlResponseSuccess',new Bun.Transpiler({loader:'ts'}).transformSync(source.slice(thinkingStart,thinkingEnd)))
  const messages=await import('../../../src/utils/messages')
  const main=fs.readFileSync(mainPath,'utf8')
  const initialStart=main.indexOf('    let thinkingEnabled = shouldEnableThinkingByDefault();')
  const initialEnd=main.indexOf('    logForDiagnosticsNoPII(',initialStart)
  if(initialStart<0||initialEnd<0)throw new Error('Original thinking initialization missing')
  const initialBody=new Bun.Transpiler({loader:'ts'}).transformSync(main.slice(initialStart,initialEnd)+'\nreturn thinkingConfig\n')
  const initialThinking=new Function('options','shouldEnableThinkingByDefault','process',initialBody)({},thinking.shouldEnableThinkingByDefault,process)
  const options:any={thinkingConfig:initialThinking}
  const frames:any[]=[]
  const breadcrumbs:any[]=[]
  const send=(message:any)=>frames.push({type:'control_response',response:{subtype:'success',request_id:message.request_id}})
  const snapshots:any[]=[]
  for(const request of input.controls){
    const message={request_id:'control-'+snapshots.length,request}
    if(request.subtype==='set_model')runModel(message,model.getDefaultMainLoopModel,state.setMainLoopModelOverride,()=>{},(requested:string,resolved:string)=>breadcrumbs.push(...messages.createModelSwitchBreadcrumbs(requested,resolved)),send)
    else runThinking(message,options,send)
    snapshots.push({model:model.getMainLoopModel(),thinkingConfig:options.thinkingConfig})
  }
  process.stdout.write(JSON.stringify({frames,snapshots,breadcrumbs,capabilities})+'\n')
  process.exit(0)
}

// Reuse the already qualified private credential/config harness, then call
// the original signer and system cache splitter directly on real peer bodies.
const { signClaudeCodeCCHInString } = await import('../../../src/utils/claudeCodeCch')
const { splitSysPromptPrefix } = await import('../../../src/utils/api')
const bodies: string[] = input
const signed = bodies.map(signClaudeCodeCCHInString)
const blocks = bodies.map(body => splitSysPromptPrefix(JSON.parse(body).system.map((block: any) => block.text)))
if (capabilities.blocked.length) throw new Error('Source billing capability escape: ' + JSON.stringify(capabilities.blocked))
process.stdout.write(JSON.stringify({ signed, blocks, capabilities }) + '\n')
process.exit(0)
