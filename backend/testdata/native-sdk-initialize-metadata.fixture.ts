import fs from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { mock, spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../scripts/quality-gate/go-parity/client-options-capabilities'
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_SDK_METADATA_FIXTURE !== '1') throw new Error('Explicit pinned Source metadata fixture required')
const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(process.env.HOME, 'SDK initialize metadata')
const printPath = fs.realpathSync(fileURLToPath(new URL('../../src/cli/print.ts', import.meta.url)))
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: [...absentManagedSettingsFixturePaths(), '/Library/Application Support/ClaudeCode/.claude/skills', '/Library/Application Support/ClaudeCode/.claude/agents', '/Library/Application Support/ClaudeCode/.claude/output-styles', join(process.execPath, '..', 'rg'), '/usr/bin/rg', '/bin/rg', '/home/claude/.claude/remote/.oauth_token','/home/claude/.claude/remote/.api_key'] })
// Only the external credential-store primitive is replaced. Its bytes are the
// disposable physical Source credential fixture, parsed by the real auth code.
const secure = join(home, '.claude', '.credentials.json')
mock.module(new URL('../../src/utils/secureStorage/index.ts', import.meta.url).pathname, () => ({ getSecureStorage: () => ({ name: 'private-physical-credential-store', read: () => fs.existsSync(secure) ? JSON.parse(fs.readFileSync(secure, 'utf8')) : null, readAsync: async () => fs.existsSync(secure) ? JSON.parse(fs.readFileSync(secure, 'utf8')) : null }) }))
// Execute original prefetch with a narrowly bound external security primitive.
const processes = await import('node:child_process')
const fakeExecFile = (command: string, args: string[], options: unknown, callback: Function) => {
  if (command !== 'security' || args[0] !== 'find-generic-password') throw new Error('Unbound Source subprocess')
  callback(Object.assign(new Error('private Keychain entry absent'), { code:44 }), '')
  return { on() {}, kill() {} } as any
}
spyOn(processes, 'execFile').mockImplementation(fakeExecFile as any)
const prefetch = await import('../../src/utils/secureStorage/keychainPrefetch')
prefetch.startKeychainPrefetch()
await prefetch.ensureKeychainPrefetchCompleted()
Object.assign(globalThis, { MACRO: { VERSION:'fixture', BUILD_TIME:'' } })
await import('../../preload')
const state = await import('../../src/bootstrap/state')
state.setCwdState(process.cwd()); state.setOriginalCwd(process.cwd()); state.setProjectRoot(process.cwd()); state.setAllowedSettingSources(['userSettings']); state.setIsInteractive(false)
const config = await import('../../src/utils/config'); config.enableConfigs()
const model = await import('../../src/utils/model/model')
const { getModelOptions } = await import('../../src/utils/model/modelOptions')
const auth = await import('../../src/utils/auth')
const effort = await import('../../src/utils/effort')
const thinking = await import('../../src/utils/thinking')
const fast = await import('../../src/utils/fastMode')
const { getAPIProvider } = await import('../../src/utils/model/providers')
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
const { initBuiltinPlugins } = await import('../../src/plugins/bundled/index')
const { initBundledSkills } = await import('../../src/skills/bundled/index')
initBuiltinPlugins(); initBundledSkills()
const commandsModule = await import('../../src/commands')
const headless = await import('../../src/commands/headless')
const commands = headless.filterCommandsForHeadlessMode(await commandsModule.getCommands(process.cwd())).filter(cmd => cmd.userInvocable !== false).map(cmd => ({name:commandsModule.getCommandName(cmd),description:commandsModule.formatDescriptionWithSource(cmd),argumentHint:cmd.argumentHint || ''}))
const agentsModule = await import('../../src/tools/AgentTool/loadAgentsDir')
const definitions = await agentsModule.getAgentDefinitionsWithOverrides(process.cwd())
const agents = definitions.activeAgents.map(agent => ({name:agent.agentType,description:agent.whenToUse,...(agent.model !== undefined && agent.model !== 'inherit' ? {model:agent.model}: {})}))
const styles = await import('../../src/constants/outputStyles')
const outputStyles = Object.keys(await styles.getAllOutputStyles(process.cwd()))
const settings = await import('../../src/utils/settings/settings')
const outputStyle = settings.getSettings_DEPRECATED()?.outputStyle || styles.DEFAULT_OUTPUT_STYLE_NAME
const accountInfo = auth.getAccountInformation()
const account = {email:accountInfo?.email,organization:accountInfo?.organization,subscriptionType:accountInfo?.subscription,tokenSource:accountInfo?.tokenSource,apiKeySource:accountInfo?.apiKeySource,apiProvider:getAPIProvider()}
process.stdout.write(JSON.stringify({models,account,commands,agents,available_output_styles:outputStyles,output_style:outputStyle,capabilities})+'\n')
