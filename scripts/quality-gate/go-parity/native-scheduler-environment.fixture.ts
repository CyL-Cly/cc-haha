import { mkdirSync, writeFileSync, unlinkSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { mock, spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Scheduler environment')
if (process.env.CC_HAHA_SCHEDULER_ENVIRONMENT_FIXTURE !== '1') throw new Error('Missing scheduler environment fixture')
const absent = [...absentManagedSettingsFixturePaths()]
for (let directory = dirname(input.homeDir);; directory = dirname(directory)) {absent.push(join(directory, '.git'));if (directory === dirname(directory)) break}
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absent })
let current: any
const actualTerminal = await import('../../../src/utils/terminalShellEnvironment')
mock.module('../../../src/utils/terminalShellEnvironment', () => ({ ...actualTerminal, getProcessEnvWithTerminalShellEnvironment: async () => ({...current.processEnv}) }))
await import('../../../preload')
const { ProviderService } = await import('../../../src/server/services/providerService')
const { SettingsService } = await import('../../../src/server/services/settingsService')
const { hahaOAuthService } = await import('../../../src/server/services/hahaOAuthService')
const network = await import('../../../src/server/services/networkSettings')
spyOn(ProviderService.prototype, 'getProviderRuntimeEnv').mockImplementation(async () => current.providerEnv === null ? null as any : {...current.providerEnv})
spyOn(SettingsService.prototype, 'getAgentTeamsEnabled').mockImplementation(async () => current.teams)
spyOn(hahaOAuthService,'ensureFreshAccessToken').mockImplementation(async () => { if (current.oauthError) throw new Error(current.oauthError);return current.oauthToken })
spyOn(network, 'loadNetworkSettings').mockImplementation(async () => ({} as any))
spyOn(network, 'buildNetworkEnvironment').mockImplementation((_settings: any, env: any) => {current.networkInput={...env};return {...current.networkEnv}})
const { CronScheduler } = await import('../../../src/server/services/cronScheduler')
const scheduler = new CronScheduler() as any
const folder = join(process.env.CLAUDE_CONFIG_DIR!, 'cc-haha')
mkdirSync(folder, {recursive:true})
const rows=[]
const oldError=console.error
console.error=()=>{}
for (current of input.rows) {
 for (const name of ['settings.json','providers.json']) {try {unlinkSync(join(folder,name))}catch{}}
 if (current.settings !== undefined) writeFileSync(join(folder,'settings.json'),typeof current.settings === 'string' ? current.settings : JSON.stringify(current.settings))
 if (current.hasProviders) writeFileSync(join(folder,'providers.json'), '{}')
 try {rows.push({env:await scheduler.buildTaskChildEnv('/fixture/work',current.task),networkInput:current.networkInput})}
 catch(error) {rows.push({error:(error as Error).message})}
}
console.error=oldError
process.stdout.write(JSON.stringify({ rows, capabilities }))
