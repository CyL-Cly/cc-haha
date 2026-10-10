import fs, * as fsExports from 'node:fs'
import { syncBuiltinESMExports } from 'node:module'
import { dirname, join } from 'node:path'
import { spyOn } from 'bun:test'
import { feature } from 'bun:bundle'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
if (Object.keys(input).some(key => !['home', 'name', 'global', 'user', 'project', 'local', 'flag', 'flagInline', 'flagSameAsUser', 'ccHaha', 'ccHahaBase64', 'environment', 'allowed', 'operations'].includes(key))) throw new Error('Startup env requires declared private inputs before production imports')
const home = assertDisposableFixtureHome(input.home, 'CLI startup environment')
if (!feature('TRANSCRIPT_CLASSIFIER') || process.env.NODE_ENV !== 'production' || process.env.CC_HAHA_CLI_STARTUP_ENV_FIXTURE !== '1' || process.env.USER_TYPE !== 'external' || !Array.isArray(input.operations)) throw new Error('Startup env requires explicit original release/private profile before production imports')
if (input.allowed?.some((source: string) => !['userSettings', 'projectSettings', 'localSettings', 'flagSettings', 'policySettings'].includes(source)) || input.operations.some((op: any) => !['safe', 'trusted', 'extraCA', 'resetSettings', 'setEnv', 'write', 'cacheFlagNull'].includes(op.kind))) throw new Error('Startup env requires declared original operations before production imports')
const project = join(home, 'project'), config = join(home, '.claude')
const paths: any = { global: join(config, '.claude.json'), user: join(config, 'settings.json'), project: join(project, '.claude/settings.json'), local: join(project, '.claude/settings.local.json'), flag: input.flagSameAsUser ? join(config, 'settings.json') : join(home, 'flag.json'), ccHaha: join(config, 'cc-haha/settings.json') }
const protectedKeys = new Set(['HOME', 'CLAUDE_CONFIG_DIR', 'PATH', 'USER_TYPE', 'CLAUDE_CODE_SIMPLE', 'NODE_ENV', 'BUN_RUNTIME_TRANSPILER_CACHE_PATH', 'CC_HAHA_FIXTURE_TEMP_ROOT', 'CC_HAHA_CLI_STARTUP_ENV_FIXTURE', 'CLAUDE_CODE_ENABLE_XAA', 'DEBUG', 'DEBUG_SDK'])
const probeKeys = new Set<string>(['NODE_EXTRA_CA_CERTS'])
const original = (raw: string) => JSON.parse(raw.replaceAll('<HOME>', home))
const checkEnv = (env: any) => {
  if (!env || typeof env !== 'object' || Array.isArray(env)) return
  for (const key of Object.keys(env)) { if (protectedKeys.has(key)) throw new Error('Startup env refuses fixture capability mutation'); probeKeys.add(key) }
}
for (const field of ['global', 'user', 'project', 'local', 'flag', 'flagInline', 'ccHaha']) if (input[field] !== undefined) { try { checkEnv(original(input[field])?.env) } catch (error) { if (String(error).includes('refuses fixture')) throw error } }
if (input.environment) checkEnv(original(input.environment))
if (input.ccHahaBase64) checkEnv(JSON.parse(Buffer.from(input.ccHahaBase64, 'base64').toString('utf8')).env)
for (const operation of input.operations) if (operation.raw) { try { checkEnv(operation.kind === 'setEnv' ? original(operation.raw) : original(operation.raw)?.env) } catch (error) { if (String(error).includes('refuses fixture')) throw error } }
for (const key of ['ANTHROPIC_DEFAULT_FABLE_MODEL_SUPPORTED_CAPABILITIES', 'ANTHROPIC_DEFAULT_HAIKU_MODEL_SUPPORTED_CAPABILITIES', 'ANTHROPIC_DEFAULT_SONNET_MODEL_SUPPORTED_CAPABILITIES', 'ANTHROPIC_DEFAULT_OPUS_MODEL_SUPPORTED_CAPABILITIES']) probeKeys.add(key)
fs.mkdirSync(join(project, '.claude'), { recursive: true }); fs.mkdirSync(join(config, 'cc-haha'), { recursive: true })
fs.writeFileSync(paths.global, (input.global ?? '{}').replaceAll('<HOME>', home), { mode: 0o640 })
for (const field of ['user', 'project', 'local', 'flag', 'ccHaha']) if (input[field] !== undefined && !(field === 'flag' && input.flagSameAsUser)) fs.writeFileSync(paths[field], input[field].replaceAll('<HOME>', home), { mode: 0o640 })
if (input.ccHahaBase64) fs.writeFileSync(paths.ccHaha, Buffer.from(input.ccHahaBase64, 'base64'), { mode: 0o640 })
const absentGit: string[] = []
for (const base of [home, fs.realpathSync(home)]) for (let current = dirname(base); ; current = dirname(current)) { absentGit.push(join(current, '.git')); if (dirname(current) === current) break }
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: [...absentManagedSettingsFixturePaths(), ...absentGit] })
const { enableConfigs } = await import('../../../src/utils/config')
const bootstrap = await import('../../../src/bootstrap/state')
const settings = await import('../../../src/utils/settings/settings')
const cache = await import('../../../src/utils/settings/settingsCache')
const debug = await import('../../../src/utils/debug')
const managed = await import('../../../src/utils/managedEnv')
const extra = await import('../../../src/utils/caCertsConfig')
const eligible = await import('../../../src/services/remoteManagedSettings/syncCache')
const fileRead = await import('../../../src/utils/fileRead')
bootstrap.setOriginalCwd(project)
if (input.allowed) bootstrap.setAllowedSettingSources(input.allowed)
if (input.flag || input.flagSameAsUser) bootstrap.setFlagSettingsPath(paths.flag)
if (input.flagInline) bootstrap.setFlagSettingsInline(original(input.flagInline))
enableConfigs()
if (input.environment) Object.assign(process.env, original(input.environment))
const describe = (value: any) => {
  if (value === undefined) return { kind: 'undefined' }
  if (typeof value === 'number') return { kind: 'number', json: JSON.stringify(value), nativeString: String(value), negativeZero: Object.is(value, -0), finite: Number.isFinite(value) }
  return { kind: value === null ? 'null' : typeof value, json: JSON.stringify(value) }
}
const fields = (object: any) => Object.keys(object ?? {}).map(key => ({ key: JSON.stringify(key), value: describe(object[key]) }))
const tree = (value: any): any => {
  if (Array.isArray(value)) return { kind: 'array', items: value.map(tree) }
  if (value !== null && typeof value === 'object') return { kind: 'object', fields: Object.keys(value).map(key => ({ key: JSON.stringify(key), value: tree(value[key]) })) }
  return describe(value)
}
const snapshot = () => [...probeKeys].map(key => ({ key: JSON.stringify(key), own: Object.hasOwn(process.env, key), value: describe(process.env[key]) }))
const realRead = fs.readFileSync, assign = Object.assign, realDebug = debug.logForDebugging
let effects: any[] = []
const realSettingsRead = fileRead.readFileSync
spyOn(fileRead, 'readFileSync').mockImplementation((path: string) => {
  if ([paths.user, paths.project, paths.local, paths.flag].includes(path)) effects.push({ kind: 'read', path })
  return realSettingsRead(path)
})
const reads = (...args: any[]) => {
  if (typeof args[0] === 'string' && ([paths.global, paths.ccHaha, join(config, 'cc-haha/providers.json')].includes(args[0]))) effects.push({ kind: 'read', path: args[0] })
  return realRead(...args as [any, any])
}
fs.readFileSync = reads as any; spyOn(fsExports, 'readFileSync').mockImplementation(reads as any); syncBuiltinESMExports()
Object.assign = ((target: any, ...sources: any[]) => {
  const result = assign(target, ...sources)
  if (target === process.env) for (const source of sources) effects.push({ kind: 'assign', fields: fields(source) })
  return result
}) as typeof Object.assign
spyOn(debug, 'logForDebugging').mockImplementation((message: string, options?: any) => { if (message.startsWith('CA certs:')) effects.push({ kind: 'debug', message: JSON.stringify(message), level: options?.level ?? 'debug' }); return realDebug(message, options) })
const results: any[] = []
for (const operation of input.operations) {
  effects = []
  let error: any = null
  try {
    if (operation.kind === 'safe') managed.applySafeConfigEnvironmentVariables()
    else if (operation.kind === 'trusted') managed.applyConfigEnvironmentVariables()
    else if (operation.kind === 'extraCA') extra.applyExtraCACertsFromConfig()
    else if (operation.kind === 'resetSettings') cache.resetSettingsCache()
    else if (operation.kind === 'cacheFlagNull') cache.setCachedSettingsForSource('flagSettings', null)
    else if (operation.kind === 'setEnv') assign(process.env, original(operation.raw))
    else if (operation.kind === 'write') fs.writeFileSync(paths[operation.file], operation.raw.replaceAll('<HOME>', home))
  } catch (caught: any) { error = { name: caught.name, message: JSON.stringify(caught.message) } }
  const session = cache.getSessionSettingsCache()
  results.push({ operation: operation.kind, error, effects, environment: snapshot(), sessionSettings: session ? JSON.stringify(session.settings) : null, sessionView: session ? tree(session.settings) : null, sessionErrors: session?.errors ?? null })
}
if (process.env.HOME !== home || process.env.CLAUDE_CONFIG_DIR !== config) throw new Error('Startup env escaped fixture HOME/config')
process.stdout.write(JSON.stringify({ name: input.name, results, capabilities, sourceOnly: { eligibility: eligible.isRemoteManagedSettingsEligible(), enabledSources: (await import('../../../src/utils/settings/constants')).getEnabledSettingSources(), fullInitExecuted: false } }))
