import fs from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { mock, spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../scripts/quality-gate/go-parity/client-options-capabilities'
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_SDK_SETTINGS_FIXTURE !== '1') throw new Error('Pinned private SDK settings fixture required')
const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(process.env.HOME, 'SDK get_settings')
const printPath = fs.realpathSync(fileURLToPath(new URL('../../src/cli/print.ts', import.meta.url)))
const project = fs.realpathSync(input.project)
if (!project.startsWith(home + '/')) throw new Error('Project escaped private fixture HOME')
process.chdir(project)
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: [...absentManagedSettingsFixturePaths(), '/home/claude/.claude/remote/.oauth_token', '/home/claude/.claude/remote/.api_key'] })
const secure = join(home, '.claude', '.credentials.json')
mock.module(new URL('../../src/utils/secureStorage/index.ts', import.meta.url).pathname, () => ({ getSecureStorage: () => ({ name: 'private-physical-store', read: () => fs.existsSync(secure) ? JSON.parse(fs.readFileSync(secure, 'utf8')) : null, readAsync: async () => fs.existsSync(secure) ? JSON.parse(fs.readFileSync(secure, 'utf8')) : null }) }))
const processes = await import('node:child_process')
spyOn(processes, 'execFile').mockImplementation(((command: string, args: string[], options: unknown, callback: Function) => {
  if (command !== 'security' || args[0] !== 'find-generic-password') throw new Error('Unbound subprocess')
  callback(Object.assign(new Error('private Keychain absent'), { code: 44 }), '')
  return { on() {}, kill() {} }
}) as any)
const prefetch = await import('../../src/utils/secureStorage/keychainPrefetch')
prefetch.startKeychainPrefetch(); await prefetch.ensureKeychainPrefetchCompleted()
Object.assign(globalThis, { MACRO: { VERSION: 'fixture', BUILD_TIME: '' } })
await import('../../preload')
const state = await import('../../src/bootstrap/state')
state.setCwdState(process.cwd()); state.setOriginalCwd(process.cwd()); state.setProjectRoot(process.cwd()); state.setAllowedSettingSources(['userSettings', 'projectSettings', 'localSettings']); state.setIsInteractive(false)
state.setFlagSettingsPath(input.flagPath)
const config = await import('../../src/utils/config'); config.enableConfigs()
const settings = await import('../../src/utils/settings/settings')
const model = await import('../../src/utils/model/model')
const effort = await import('../../src/utils/effort')
const source = fs.readFileSync(printPath, 'utf8')
const start = source.indexOf("        } else if (message.request.subtype === 'get_settings') {")
const end = source.indexOf("        } else if (message.request.subtype === 'stop_task')", start)
if (start < 0 || end < 0) throw new Error('Original SDK settings control missing')
const body = source.slice(source.indexOf('\n', start) + 1, end)
const run = new Function('message', 'getAppState', 'getMainLoopModel', 'modelSupportsEffort', 'resolveAppliedEffort', 'getSettingsWithSources', 'sendControlResponseSuccess', new Bun.Transpiler({ loader: 'ts' }).transformSync(body))
const frames: any[] = []
for (const row of input.rows) {
  fs.writeFileSync(join(home, '.claude/settings.json'), JSON.stringify(row.settings))
  state.setMainLoopModelOverride(row.model)
  run({ request: { subtype: 'get_settings' }, request_id: row.id }, () => ({ effortValue: input.effort }), model.getMainLoopModel, effort.modelSupportsEffort, effort.resolveAppliedEffort, settings.getSettingsWithSources, (message: any, response: any) => frames.push({ type: 'control_response', response: { subtype: 'success', request_id: message.request_id, response } }))
}
if (capabilities.blocked.length > 0) throw new Error(`Blocked capabilities: ${JSON.stringify(capabilities.blocked)}`)
process.stdout.write(JSON.stringify({ frames, capabilities }))
