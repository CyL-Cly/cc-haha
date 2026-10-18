import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual native configuration directory consumers')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_CONFIG_DIRECTORY_SOURCE !== '1') throw new Error('Pinned config-directory Source required')
const initial = join(home, '.claude'), redirected = join(home, 'owned-config-second'), project = join(home, 'owned-project')
fs.mkdirSync(initial, { recursive: true })
fs.mkdirSync(redirected, { recursive: true })
fs.mkdirSync(project, { recursive: true })
const oldRaw = JSON.stringify({ language: 'old-location', env: { CLAUDE_CONFIG_DIR: redirected }, future: { keep: 41 } }) + '\n'
const nextRaw = JSON.stringify({ language: 'new-location', future: { keep: 73 } }) + '\n'
fs.writeFileSync(join(initial, 'settings.json'), oldRaw)
fs.writeFileSync(join(redirected, 'settings.json'), nextRaw)
fs.writeFileSync(join(initial, '.claude.json'), JSON.stringify({ futureGlobal: { keep: true } }) + '\n')
fs.writeFileSync(join(initial, '.credentials.json'), JSON.stringify({ fixturePrivate: { oldLocation: true, keep: 29 } }))
fs.writeFileSync(join(redirected, '.credentials.json'), JSON.stringify({ fixturePrivate: { oldUnknown: 17 } }))
const absent = absentManagedSettingsFixturePaths()
for (let parent = home; ; parent = dirname(parent)) {
  const path = join(parent, '.git')
  if (!fs.existsSync(path)) absent.push(path)
  if (dirname(parent) === parent) break
}
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent })
;(globalThis as any).MACRO = { VERSION: '999.0.0-config-dir-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setOriginalCwd(project)
bootstrap.setCwdState(project)
bootstrap.setProjectRoot(project)
bootstrap.setAllowedSettingSources(['userSettings'])
const config = await import('../../../src/utils/config')
const settings = await import('../../../src/utils/settings/settings')
const cache = await import('../../../src/utils/settings/settingsCache')
const managed = await import('../../../src/utils/managedEnv')
const storage = await import('../../../src/utils/sessionStorage')
const env = await import('../../../src/utils/env')
const plaintext = (await import('../../../src/utils/secureStorage/plainTextStorage')).plainTextStorage
const { getMacOsKeychainStorageServiceName, getUsername, CREDENTIALS_SERVICE_SUFFIX } = await import('../../../src/utils/secureStorage/macOsKeychainHelpers')
process.env.CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH = '1'
config.enableConfigs()
const before = { global: env.getGlobalClaudeFile(), settings: settings.getSettingsFilePathForSource('userSettings'), cachedLanguage: settings.getSettingsForSource('userSettings')?.language, credential: plaintext.read() }
const warmedProject = input.warmProject ? storage.getProjectDir(project) : null
managed.applySafeConfigEnvironmentVariables()
managed.applyConfigEnvironmentVariables()
const after = { global: env.getGlobalClaudeFile(), settings: settings.getSettingsFilePathForSource('userSettings'), cachedLanguage: settings.getSettingsForSource('userSettings')?.language, project: storage.getProjectDir(project), freshProject: storage.getProjectDir(join(project, 'never-used-before')), credential: plaintext.read() }
cache.resetSettingsCache()
const uncachedLanguage = settings.getSettingsForSource('userSettings')?.language
const writeResult = settings.updateSettingsForSource('userSettings', { effortLevel: 'high' })
if (writeResult.error) throw writeResult.error
const credentialUpdate = plaintext.update({ ...plaintext.read(), fixtureRevision: 2 } as any)
const credentialAsync = await plaintext.readAsync()
const initialCredential = fs.readFileSync(join(initial, '.credentials.json'), 'utf8')
const redirectedCredential = fs.readFileSync(join(redirected, '.credentials.json'), 'utf8')
const initialCredentialMode = fs.statSync(join(initial, '.credentials.json')).mode & 0o777
const redirectedCredentialMode = fs.statSync(join(redirected, '.credentials.json')).mode & 0o777
const credentialDelete = plaintext.delete()
const credentialAfterDelete = await plaintext.readAsync()
const initialCredentialAfterDelete = fs.readFileSync(join(initial, '.credentials.json'), 'utf8')
const paths = []
for (const [name, directory, cowork, user] of [
  ['absolute', redirected, false, 'owned-first-user'],
  ['normalized', join(home, 'owned-config-e\u0301'), false, 'owned-second-user'],
  ['relative', 'owned-relative-config', false, 'owned-用户-é'],
  ['empty', '', false, ''],
  ['absent', undefined, false, undefined],
  ['cowork', redirected, true, 'owned-last-user'],
] as const) {
  if (directory === undefined) delete process.env.CLAUDE_CONFIG_DIR
  else process.env.CLAUDE_CONFIG_DIR = directory
  if (cowork) process.env.CLAUDE_CODE_USE_COWORK_PLUGINS = 'true'
  else delete process.env.CLAUDE_CODE_USE_COWORK_PLUGINS
  if (user === undefined) delete process.env.USER
  else process.env.USER = user
  paths.push({ name, directory: directory ?? null, cowork, user: user ?? null, keychainAccount: getUsername(), cwd: process.cwd(), path: settings.getSettingsFilePathForSource('userSettings'), keychainService: getMacOsKeychainStorageServiceName(CREDENTIALS_SERVICE_SUFFIX), legacyService: getMacOsKeychainStorageServiceName() })
}
if (caps.blocked.length || caps.secureReads.length) throw new Error('Configuration directory Source escaped private boundary ' + JSON.stringify(caps))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ initial, redirected, project, before, after, warmedProject, uncachedLanguage, initialSettings: fs.readFileSync(join(initial, 'settings.json'), 'utf8'), redirectedSettings: fs.readFileSync(join(redirected, 'settings.json'), 'utf8'), initialCredential, redirectedCredential, initialCredentialMode, redirectedCredentialMode, credentialUpdate, credentialAsync, credentialDelete, credentialAfterDelete, initialCredentialAfterDelete, paths, caps }), error => error ? reject(error) : resolve()))
process.exit(0)
