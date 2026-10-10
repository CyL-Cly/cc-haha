import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import fs from 'node:fs'
import { resolve } from 'node:path'
const home = assertDisposableFixtureHome(process.env.HOME, 'ordinary plugin CLI')
if (process.env.CC_HAHA_PLUGIN_CLI_FIXTURE !== '1') throw new Error('Unowned plugin CLI fixture')
const { mock } = await import('bun:test')
await import('../../preload')
// Only external managed-policy and credential primitives are replaced.
const emptyPolicy = { settings: JSON.parse(process.env.CC_HAHA_PLUGIN_CLI_POLICY ?? '{}'), errors: [] }
mock.module(new URL('../../src/utils/settings/mdm/settings.ts', import.meta.url).pathname, () => ({ startMdmSettingsLoad() {}, ensureMdmSettingsLoaded: async () => {}, getMdmSettings: () => emptyPolicy, getHkcuSettings: () => ({ settings: {}, errors: [] }), clearMdmSettingsCache() {}, setMdmSettingsCache() {}, refreshMdmSettings: async () => ({ mdm: emptyPolicy, hkcu: { settings: {}, errors: [] } }) }))
mock.module(new URL('../../src/utils/secureStorage/index.ts', import.meta.url).pathname, () => ({ getSecureStorage: () => ({ name: 'owned-private-opaque-plugin-store', read: () => null, readAsync: async () => null, update: (value: unknown) => { fs.writeFileSync(resolve(home,'secure-plugin-fixture.json'),JSON.stringify(value));return {success:true} }, delete: () => true }) }))
const mutableFS = require('node:fs')
for (const name of ['readFileSync', 'readdirSync', 'statSync', 'lstatSync', 'existsSync']) {
 const original = mutableFS[name]
 mutableFS[name] = (...args: any[]) => {
  if (typeof args[0] === 'string' && args[0].startsWith('/Library/Application Support/ClaudeCode')) {
   if (name === 'existsSync') return false
   throw Object.assign(new Error('owned absent managed policy'), { code:'ENOENT' })
  }
  return original(...args)
 }
}
if (process.env.CC_HAHA_PLUGIN_CLI_TRANSPORT_MAP) {
 const mapping=JSON.parse(process.env.CC_HAHA_PLUGIN_CLI_TRANSPORT_MAP)
 const axios=(await import('axios')).default
 const adapter=axios.getAdapter(axios.defaults.adapter)
 axios.defaults.adapter=async config=>{
  const requested=config.url??''
  const base=mapping.base
  if (!/^https?:\/\/(127\.0\.0\.1|localhost):[0-9]+$/.test(base)) throw new Error('Unowned external plugin transport')
  if (!requested.startsWith('https://downloads.claude.ai/claude-code-releases/plugins/claude-plugins-official/') && !requested.startsWith('https://raw.githubusercontent.com/anthropics/claude-plugins-official/')) throw new Error('Unowned original external request '+requested)
  fs.appendFileSync(resolve(home,'owned-original-plugin-transport.ndjson'),JSON.stringify({requested})+'\n')
  const path=new URL(requested).pathname
  return adapter({...config,url:base+path})
 }
}
const pendingOwnedIgnore: Promise<void>[] = []
const ignoreModule = await import('../../src/utils/git/gitignore')
const originalIgnore = ignoreModule.addFileGlobRuleToGitignore
mock.module(new URL('../../src/utils/git/gitignore.ts',import.meta.url).pathname, () => ({ ...ignoreModule, addFileGlobRuleToGitignore: (filename: string,cwd?:string) => {
 const task=originalIgnore(filename,cwd)
 pendingOwnedIgnore.push(task)
 return task
} }))
const originalExit=process.exit.bind(process)
// Only the fixture's actual owned Git producer is joined before process exit;
// the original settings writer still launches it after the physical write.
process.exit=((code?:number) => {
 if (pendingOwnedIgnore.length) {void Promise.allSettled(pendingOwnedIgnore).then(()=>originalExit(code));return undefined}
 return originalExit(code)
}) as typeof process.exit
process.env.CLAUDE_CODE_ENTRYPOINT = 'cli'
const state = await import('../../src/bootstrap/state')
state.setOriginalCwd(process.cwd())
state.setInlinePlugins(JSON.parse(process.env.CC_HAHA_PLUGIN_CLI_INLINE ?? '[]'))
state.setAdditionalDirectoriesForClaudeMd(JSON.parse(process.env.CC_HAHA_PLUGIN_CLI_ADDITIONAL ?? '[]'))
const { enableConfigs } = await import('../../src/utils/config')
enableConfigs()
const handlers = await import('../../src/cli/handlers/plugins')
const input = JSON.parse(process.env.CC_HAHA_PLUGIN_CLI_INPUT ?? '{}')
const functions = {
 list: handlers.pluginListHandler,
 validate: handlers.pluginValidateHandler,
 install: handlers.pluginInstallHandler,
 uninstall: handlers.pluginUninstallHandler,
 enable: handlers.pluginEnableHandler,
 disable: handlers.pluginDisableHandler,
 update: handlers.pluginUpdateHandler,
 marketplaceAdd: handlers.marketplaceAddHandler,
 marketplaceList: handlers.marketplaceListHandler,
 marketplaceRemove: handlers.marketplaceRemoveHandler,
 marketplaceUpdate: handlers.marketplaceUpdateHandler,
}
const fn = functions[input.action as keyof typeof functions]
if (!fn) throw new Error('Unowned original plugin CLI handler')
if (['list','marketplaceList'].includes(input.action)) await (fn as any)(input.options ?? {})
else await (fn as any)(input.argument, input.options ?? {})
