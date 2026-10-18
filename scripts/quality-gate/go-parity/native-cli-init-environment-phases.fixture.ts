import fs from 'node:fs'
import { join, resolve } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual native CLI init environment phases')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_NATIVE_INIT_PHASES_SOURCE !== '1') throw new Error('Pinned actual init phases Source required')
const origin = input.origin
if (typeof origin !== 'string' || !/^http:\/\/127\.0\.0\.1:[1-9][0-9]*$/.test(origin)) throw new Error('Init phases require explicit loopback transport')
function extraEnv(value: unknown): Record<string, string> {
  if (value == null) return {}
  if (!value || typeof value !== 'object' || Array.isArray(value) || Object.values(value).some(item => typeof item !== 'string')) throw new Error('Init phases require scalar environment input')
  return value as Record<string, string>
}
const project = join(home, 'project'), config = join(home, '.claude')
const redirectProfiles: Record<string, string> = { absolute: join(home, 'next-config'), relative: '../next-config', empty: '', unicode: '../config-e\u0301' }
const redirect = input.configRedirect === undefined ? undefined : redirectProfiles[input.configRedirect]
if (input.configRedirect !== undefined && redirect === undefined) throw new Error('Unknown private config redirect profile')
const redirectDirectory = redirect === undefined ? undefined : resolve(project, redirect.normalize('NFC'))
const files = [
  { path: join(config, 'settings.json'), content: JSON.stringify({ env: { ANTHROPIC_BASE_URL: origin + '/safe', PROBE_INIT_TRUSTED: 'user', ...extraEnv(input.userEnv), ...(redirect === undefined ? {} : { CLAUDE_CONFIG_DIR: redirect }) }, futurePrivate: { keep: true } }) },
  { path: join(project, '.claude', 'settings.json'), content: JSON.stringify({ env: { ANTHROPIC_BASE_URL: origin + '/full', PROBE_INIT_PROJECT: 'project', ...extraEnv(input.projectEnv) }, futurePrivateProject: { keep: true } }) },
]
if (redirectDirectory !== undefined) files.push(
  { path: join(redirectDirectory, 'settings.json'), content: JSON.stringify({ env: { ANTHROPIC_BASE_URL: origin + '/redirect-user', PROBE_INIT_TRUSTED: 'next-user' }, futureRedirect: { keep: true } }) },
  { path: join(redirectDirectory, 'cc-haha', 'settings.json'), content: JSON.stringify({ env: { PROBE_INIT_CC_HAHA: 'redirect-provider' }, futureProvider: { keep: true } }) },
)
for (const file of files) { fs.mkdirSync(join(file.path, '..'), { recursive: true }); fs.writeFileSync(file.path, file.content) }
fs.mkdirSync(join(project, '.git'), { recursive: true })
const caps = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths(), loopbackFetchOrigin: origin })
;(globalThis as any).MACRO = { VERSION: '999.0.0-init-phases-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = join(home, 'managed')
const { getManagedFilePath } = await import('../../../src/utils/settings/managedPath')
if (getManagedFilePath() !== join(home, 'managed')) throw new Error('Unowned managed path')
process.env.USER_TYPE = 'external'
process.chdir(project)
const state = await import('../../../src/bootstrap/state')
state.setOriginalCwd(process.cwd())
state.setProjectRoot(process.cwd())
state.setCwdState(process.cwd())
state.setAllowedSettingSources(input.settingSources === 'user' ? ['userSettings', 'policySettings'] : ['userSettings', 'projectSettings', 'localSettings', 'policySettings'])
const { enableConfigs } = await import('../../../src/utils/config')
const managed = await import('../../../src/utils/managedEnv')
const ca = await import('../../../src/utils/caCertsConfig')
const { preconnectAnthropicApi } = await import('../../../src/utils/apiPreconnect')
const observed: Array<{ url: string; method: string }> = []
const pending: Promise<Response>[] = []
const originalFetch = globalThis.fetch
globalThis.fetch = ((...args: Parameters<typeof fetch>) => {
  observed.push({ url: String(args[0]), method: args[1]?.method ?? 'GET' })
  const result = originalFetch(...args)
  pending.push(result)
  return result
}) as typeof fetch
const snapshot = () => ({ base: process.env.ANTHROPIC_BASE_URL, trusted: process.env.PROBE_INIT_TRUSTED, project: process.env.PROBE_INIT_PROJECT, config: process.env.CLAUDE_CONFIG_DIR, ccHaha: process.env.PROBE_INIT_CC_HAHA })
enableConfigs()
managed.applySafeConfigEnvironmentVariables()
ca.applyExtraCACertsFromConfig()
const safe = snapshot()
preconnectAnthropicApi()
managed.applyConfigEnvironmentVariables()
const full = snapshot()
await Promise.all(pending)
for (const file of files) if (fs.readFileSync(file.path, 'utf8') !== file.content) throw new Error('Init Source mutated protected settings')
if (caps.blocked.length || caps.secureReads.length || observed.length > 1) throw new Error('Init phases Source escaped declared private boundary ' + JSON.stringify(caps))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ project: process.cwd(), files, safe, full, observed, caps }), error => error ? reject(error) : resolve()))
process.exit(0)
