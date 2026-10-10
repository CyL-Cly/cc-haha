import { dirname, join } from 'node:path'
import { mock, spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual desktop Team child environment')
if (process.env.CC_HAHA_DESKTOP_TEAM_ENV_FIXTURE !== '1') throw new Error('Missing desktop Team environment fixture')
const absent = [...absentManagedSettingsFixturePaths()]
for (let path = dirname(input.homeDir);; path = dirname(path)) {
  absent.push(join(path, '.git'))
  if (path === dirname(path)) break
}
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absent })
let current: any
const terminal = await import('../../../src/utils/terminalShellEnvironment')
mock.module('../../../src/utils/terminalShellEnvironment', () => ({ ...terminal, getProcessEnvWithTerminalShellEnvironment: async () => ({ ...current.environment }) }))
await import('../../../preload')
const { ProviderService } = await import('../../../src/server/services/providerService')
const { SettingsService } = await import('../../../src/server/services/settingsService')
const { hahaOAuthService } = await import('../../../src/server/services/hahaOAuthService')
const network = await import('../../../src/server/services/networkSettings')
spyOn(ProviderService.prototype, 'getProvider').mockImplementation(async id => current.providerEnvironment ? { id } as any : null)
spyOn(ProviderService.prototype, 'getProviderRuntimeEnv').mockImplementation(async () => current.providerEnvironment ? { ...current.providerEnvironment } : null as any)
// Isolate the preference getter primitive, keeping buildChildEnv itself real.
spyOn(SettingsService.prototype, 'getAgentTeamsEnabled').mockImplementation(async () => current.enabled)
spyOn(hahaOAuthService, 'ensureFreshAccessToken').mockImplementation(async () => null)
spyOn(network, 'loadNetworkSettings').mockImplementation(async () => network.normalizeNetworkSettings({ proxy: { mode: 'direct' } }))
const { ConversationService } = await import('../../../src/server/services/conversationService')
const service = new ConversationService() as any
const fields = ['CLAUDE_CODE_ENABLE_TASKS', 'CC_HAHA_AGENT_TEAMS_ENABLED', 'CC_HAHA_TEAM_REVIEW_REQUIRED', 'CC_HAHA_TEAM_LEADER_RUNTIME']
const rows = []
for (current of input.rows) {
  const env = await service.buildChildEnv(input.homeDir, 'ws://127.0.0.1:3456/sdk/private-id?token=fake', current.selection)
  rows.push(Object.fromEntries(fields.map(field => [field, env[field]])))
}
await service.stopAllSessionsAndWait()
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ rows, capabilities }), () => resolve()))
process.exit(0)
