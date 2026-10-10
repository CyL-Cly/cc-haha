import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
import { realpathSync } from 'node:fs'
import { dirname, isAbsolute, join, relative, sep } from 'node:path'
import { mock } from 'bun:test'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(process.env.HOME, 'Native capability mentions')
process.env.HOME = home
process.env.USERPROFILE = home
if (process.env.CC_HAHA_NATIVE_CAPABILITY_MENTIONS_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Unowned capability mentions fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: [...absentManagedSettingsFixturePaths(), join(dirname(process.execPath), 'rg'), '/usr/bin/rg', '/bin/rg'] })
const { buildCapabilityMentions } = await import('../../../src/server/services/capabilityMentionService')
if (input.snapshots) {
  const results = input.snapshots.map((snapshot: any) => buildCapabilityMentions({
    ...snapshot,
    commands: snapshot.commands.map((command: any) => ({ ...command,
      ...(Object.hasOwn(command, 'userFacingName') ? { userFacingName: () => command.userFacingName } : {}),
      ...(Object.hasOwn(command, 'isEnabled') ? { isEnabled: () => command.isEnabled } : {}),
    })),
  }))
  process.stdout.write(JSON.stringify({ results, capabilities }))
  process.exit(0)
}
const cwd = input.cwd
const within = relative(realpathSync(home), realpathSync(cwd))
if (!isAbsolute(cwd) || within === '..' || within.startsWith(`..${sep}`) || isAbsolute(within)) throw new Error('Unowned mentions project')
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = `${home}/managed`
const { getManagedFilePath } = await import('../../../src/utils/settings/managedPath')
if (getManagedFilePath() !== `${home}/managed`) throw new Error('Unowned mentions managed path')
process.env.USER_TYPE = 'external'
// Only the unavailable OS prefetch primitive is supplied; all discovery,
// settings, provider selection, plugin MCP loading and projection run Source.
mock.module('../../../src/utils/secureStorage/keychainPrefetch', () => ({ getLegacyApiKeyPrefetchResult: () => ({ stdout: '' }), getLegacyApiKeyPrefetchPromise: () => undefined, startKeychainPrefetch: () => {}, clearLegacyApiKeyPrefetch: () => {} }))
;(globalThis as any).MACRO = { BUILD_TIME: '' }
process.chdir(cwd)
const state = await import('../../../src/bootstrap/state')
state.setCwdState(cwd); state.setOriginalCwd(cwd); state.setProjectRoot(cwd)
state.setAllowedSettingSources(['userSettings', 'projectSettings', 'localSettings', 'policySettings'])
state.setIsInteractive(false)
const { enableConfigs } = await import('../../../src/utils/config')
enableConfigs()
const { handleSkillsApi } = await import('../../../src/server/api/skills')
const results = []
for (const path of input.paths) {
  const url = new URL(path, 'http://127.0.0.1')
  if (url.origin !== 'http://127.0.0.1' || url.pathname !== '/api/skills/mentions') throw new Error('Undeclared mentions endpoint')
  const response = await handleSkillsApi(new Request(url), url, url.pathname.split('/').filter(Boolean))
  results.push({ status: response.status, body: await response.json() })
}
process.stdout.write(JSON.stringify({ results, capabilities }))
