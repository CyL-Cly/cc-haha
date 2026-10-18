import fs from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Native raw launch environment')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_CONFIG_LAUNCH_SOURCE !== '1') throw new Error('Pinned raw launch Source required')
const work = join(home, 'owned-launch-work')
fs.mkdirSync(work, { recursive: true })
const absent = absentManagedSettingsFixturePaths()
for (let parent = home; ; parent = dirname(parent)) {
  const path = join(parent, '.git')
  if (!fs.existsSync(path)) absent.push(path)
  if (dirname(parent) === parent) break
}
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent })
;(globalThis as any).MACRO = { VERSION: '999.0.0-config-launch-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
process.chdir(work)
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setOriginalCwd(process.cwd())
bootstrap.setCwdState(process.cwd())
bootstrap.setProjectRoot(process.cwd())
bootstrap.setAllowedSettingSources(['userSettings'])
const envUtils = await import('../../../src/utils/envUtils')
const config = await import('../../../src/utils/config')
const settings = await import('../../../src/utils/settings/settings')
const cache = await import('../../../src/utils/settings/settingsCache')
const env = await import('../../../src/utils/env')
config.enableConfigs()
const rows = []
for (const [name, directory] of [
  ['absent', undefined],
  ['empty', ''],
  ['relative', 'owned-launch-relative'],
  ['relative-parent', '../owned-launch-parent'],
  ['decomposed', join(home, 'owned-launch-e\u0301')],
  ['absolute', join(home, 'owned-launch-absolute')],
] as const) {
  if (directory === undefined) delete process.env.CLAUDE_CONFIG_DIR
  else process.env.CLAUDE_CONFIG_DIR = directory
  const physical = resolve(envUtils.getClaudeConfigHomeDir())
  fs.mkdirSync(physical, { recursive: true })
  const content = JSON.stringify({ language: name, env: { ANTHROPIC_CUSTOM_HEADERS: `x-config-launch: ${name}` }, futureLaunch: { keep: name } }) + '\n'
  fs.writeFileSync(join(physical, 'settings.json'), content)
  cache.resetSettingsCache()
  env.getGlobalClaudeFile.cache.clear()
  rows.push({ name, directory: directory ?? null, cwd: process.cwd(), home: process.env.HOME, configHome: envUtils.getClaudeConfigHomeDir(), ccHahaDir: envUtils.getCcHahaDir(), physical, settingsPath: settings.getSettingsFilePathForSource('userSettings'), globalPath: env.getGlobalClaudeFile(), language: settings.getSettingsForSource('userSettings')?.language, content })
}
if (caps.blocked.length || caps.secureReads.length) throw new Error('Raw launch Source escaped private boundary ' + JSON.stringify(caps))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ rows, caps }), error => error ? reject(error) : resolve()))
process.exit(0)
