import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Current configuration-directory managed provider')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_CONFIG_DIR_PROVIDER_SOURCE !== '1') throw new Error('Pinned current-directory Source profile required')
if (!['ordinary', 'desktop', 'host-managed', 'missing-index', 'invalid-index'].includes(input.profile)) throw new Error('Declared directory/provider profile required')
const initial = join(home, '.claude'), second = join(home, 'owned-second'), third = join(home, 'owned-third'), project = join(home, 'owned-project')
for (const directory of [initial, second, third, join(project, '.claude')]) fs.mkdirSync(join(directory, 'cc-haha'), { recursive: true })
const provider = (id: string) => ({ id, presetId: 'deepseek', name: 'Private ' + id, apiKey: 'fake-private-' + id, baseUrl: 'http://127.0.0.1:1/anthropic', apiFormat: 'anthropic', models: { main: 'deepseek-v4-flash', haiku: '', sonnet: 'deepseek-v4-flash', opus: 'deepseek-v4-pro' }, futureProvider: { keep: id } })
const files: Array<{ path: string, content: string }> = []
const write = (path: string, value: unknown) => {
  const content = typeof value === 'string' ? value : JSON.stringify(value) + '\n'
  fs.writeFileSync(path, content, { mode: 0o600 })
  files.push({ path, content })
}
write(join(initial, '.claude.json'), { futureGlobal: { keep: 71 } })
write(join(initial, 'settings.json'), { language: 'original-warm-cache', env: { CLAUDE_CONFIG_DIR: second, PROBE_USER: 'user' }, futureUser: { keep: 29 } })
write(join(second, 'settings.json'), { language: 'second-unwarmed', futureUser: { keep: 43 } })
write(join(project, '.claude', 'settings.json'), { env: { CLAUDE_CONFIG_DIR: third, PROBE_PROJECT: 'post-trust' }, futureProject: { keep: 31 } })
for (const [directory, id] of [[initial, 'initial'], [second, 'second'], [third, 'third']] as const) {
  write(join(directory, 'cc-haha', 'settings.json'), { env: { PROBE_HAHA: id, PROBE_ORDER: 'haha-' + id, ANTHROPIC_API_KEY: 'stale-' + id, ANTHROPIC_MODEL: 'stale-' + id, OPENAI_CODEX_OAUTH_FILE: 'stale-' + id }, futureManaged: { keep: id } })
  if (input.providers !== false && input.profile !== 'missing-index') write(join(directory, 'cc-haha', 'providers.json'), input.profile === 'invalid-index' ? '{invalid JSON' : { schemaVersion: 6, activeId: id, providers: [provider(id)] })
}
const absent = absentManagedSettingsFixturePaths()
for (let directory = home; ; directory = dirname(directory)) {
  const git = join(directory, '.git')
  if (!fs.existsSync(git)) absent.push(git)
  if (dirname(directory) === directory) break
}
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent })
// Export only the original private reader for observation. Its complete
// two-phase callers and provider/legacy reader bodies remain unchanged.
Bun.plugin({ name: 'source-current-directory-reader-observation', setup(build) {
  build.onLoad({ filter: /\/src\/utils\/managedEnv\.ts$/ }, args => ({ contents: fs.readFileSync(args.path, 'utf8') + '\nexport { getCcHahaSettingsEnv as sourceCcHahaEnvironment }\n', loader: 'ts' }))
} })
;(globalThis as any).MACRO = { VERSION: '999.0.0-config-provider', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setOriginalCwd(project)
bootstrap.setCwdState(project)
bootstrap.setProjectRoot(project)
bootstrap.setAllowedSettingSources(['userSettings', 'projectSettings'])
const config = await import('../../../src/utils/config')
const settings = await import('../../../src/utils/settings/settings')
const env = await import('../../../src/utils/env')
const managed: any = await import('../../../src/utils/managedEnv')
if (input.profile === 'desktop') process.env.CLAUDE_CODE_ENTRYPOINT = 'claude-desktop'
if (input.profile === 'host-managed') process.env.CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST = '1'
process.env.CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH = '1'
config.enableConfigs()
const before = { global: env.getGlobalClaudeFile(), cachedLanguage: settings.getSettingsForSource('userSettings')?.language }
const keys = ['CLAUDE_CONFIG_DIR', 'PROBE_USER', 'PROBE_PROJECT', 'PROBE_HAHA', 'PROBE_ORDER', 'ANTHROPIC_API_KEY', 'ANTHROPIC_AUTH_TOKEN', 'ANTHROPIC_MODEL', 'OPENAI_CODEX_OAUTH_FILE']
const capture = (phase: string) => ({ phase, current: process.env.CLAUDE_CONFIG_DIR, global: env.getGlobalClaudeFile(), cachedLanguage: settings.getSettingsForSource('userSettings')?.language, merged: managed.sourceCcHahaEnvironment(), environment: Object.fromEntries(keys.filter(key => Object.hasOwn(process.env, key)).map(key => [key, process.env[key]])) })
const stages = [capture('before')]
managed.applySafeConfigEnvironmentVariables()
stages.push(capture('safe'))
managed.applyConfigEnvironmentVariables()
stages.push(capture('trusted'))
// An actual reread must see physical changes without resetting settings or
// the unrelated global/project memo. No full-entry admission is claimed.
fs.writeFileSync(join(process.env.CLAUDE_CONFIG_DIR!, 'cc-haha', 'settings.json'), JSON.stringify({ env: { PROBE_HAHA: 'physical-reread', PROBE_ORDER: 'reread', ANTHROPIC_API_KEY: 'reread-stale' }, futureManaged: { keep: 97 } }) + '\n')
stages.push(capture('reread'))
const indexesUnchanged = files.filter(file => file.path.endsWith('/providers.json')).every(file => fs.readFileSync(file.path, 'utf8') === file.content)
if (!indexesUnchanged) throw new Error('Original managed provider reader mutated its private input index')
if (caps.blocked.length || caps.secureReads.length) throw new Error('Current-directory producer escaped private boundary ' + JSON.stringify(caps))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ initial, second, third, project, before, stages, files, indexesUnchanged, caps, scope: { fullSafeAndTrustedCallers: true, mainConstructor: false, modelHTTP: false, proxyRequiredProvider: false } }), error => error ? reject(error) : resolve()))
process.exit(0)
