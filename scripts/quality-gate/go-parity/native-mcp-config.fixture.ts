import { assertDisposableFixtureHome } from './fixture-guard'
import { mkdirSync, writeFileSync } from 'node:fs'
import { mock } from 'bun:test'
import { join } from 'node:path'

const home = assertDisposableFixtureHome(process.env.HOME, 'Native MCP configuration Source comparison')
if (process.env.CC_HAHA_MCP_CONFIG_FIXTURE !== '1') throw new Error('Missing MCP config marker')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
process.env.CLAUDE_CODE_LOCAL_VERSION = input.runtime?.version ?? 'fixture-version'
process.env.CLAUDE_CODE_LOCAL_BUILD_TIME = 'fixture-build'
await import('../../../preload')
const config = join(home, '.claude'), managed = join(home, 'managed')
mkdirSync(config, { recursive: true }); mkdirSync(managed, { recursive: true })
writeFileSync(join(config, 'settings.json'), '{}')
writeFileSync(join(config, '.claude.json'), '{}')
if (input.runtime) {
  for (const [path, value] of Object.entries(input.runtime.files)) { mkdirSync(join(path, '..'), { recursive: true }); writeFileSync(path, value as string) }
  writeFileSync(join(config, '.credentials.json'), '{}')
  const portable = await import('../../../src/utils/execFileNoThrowPortable')
  const execa = await import('execa')
  // Isolate native Keychain IO only. Configuration, plugin discovery,
  // settings, policy, storage merge and cache business all run unchanged.
  mock.module('../../../src/utils/execFileNoThrowPortable', () => ({ ...portable, execSyncWithDefaults_DEPRECATED() { throw new Error('isolated unavailable Keychain') } }))
  mock.module('execa', () => ({ ...execa, execaSync() { return { exitCode: 1 } } }))
}
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = managed
const { getManagedFilePath } = await import('../../../src/utils/settings/managedPath')
if (getManagedFilePath() !== managed) throw new Error('Unowned managed path')
process.env.USER_TYPE = 'external'
const state = await import('../../../src/bootstrap/state')
const cwd = input.runtime?.cwd ?? home
state.setOriginalCwd(cwd); state.setProjectRoot(cwd); state.setCwdState(cwd)
state.setAllowedSettingSources(['userSettings', 'projectSettings', 'localSettings', 'policySettings'])
const { enableConfigs } = await import('../../../src/utils/config')
enableConfigs()
const cache = await import('../../../src/utils/settings/settingsCache')
const api = await import('../../../src/services/mcp/config')
const utils = await import('../../../src/services/mcp/utils')
const observations = []
const keys = new Set<string>()
for (const row of input.cases) {
  writeFileSync(join(config, 'settings.json'), JSON.stringify(row.settings ?? {}))
  writeFileSync(join(managed, 'managed-settings.json'), JSON.stringify(row.policy ?? {}))
  cache.resetSettingsCache()
  for (const key of keys) delete process.env[key]
  keys.clear()
  for (const [key, value] of Object.entries(row.env ?? {})) { keys.add(key); process.env[key] = value as string }
  state.setIsInteractive(!(row.nonInteractive ?? false))
  state.setAllowedSettingSources(row.sources ?? ['userSettings', 'projectSettings', 'localSettings', 'policySettings'])
  let value: unknown
  if (row.action === 'parse') value = api.parseMcpConfig({ configObject: row.raw, expandVars: row.expand, scope: row.scope, filePath: row.file })
  else if (row.action === 'file') {
    if (row.data !== null) writeFileSync(row.file, row.data)
    value = api.parseMcpConfigFromFilePath({ filePath: row.file, expandVars: row.expand, scope: row.scope })
  } else if (row.action === 'signature') value = api.getMcpServerSignature(row.config)
  else if (row.action === 'unwrap') value = api.unwrapCcrProxyUrl(row.url)
  else if (row.action === 'dedup') value = row.plugins ? api.dedupPluginMcpServers(row.servers, row.manual) : api.dedupClaudeAiMcpServers(row.servers, row.manual)
  else if (row.action === 'policy') value = api.filterMcpServersByPolicy(row.servers)
  else if (row.action === 'approval') value = utils.getProjectMcpServerStatus(row.name)
  else if (row.action === 'configurations') value = await api.getClaudeCodeMcpConfigs(row.dynamic ?? {}, Promise.resolve(row.extra ?? {}))
  else if (row.action === 'scope') value = api.getMcpConfigsByScope(row.scope)
  else if (row.action === 'byName') value = api.getMcpConfigByName(row.name)
  else if (row.action === 'hash') value = utils.hashMcpConfig(row.config)
  else if (row.action === 'api') {
    const { handleMcpApi } = await import('../../../src/server/api/mcp')
    const url = new URL(row.path, 'http://127.0.0.1:3000')
    const response = await handleMcpApi(new Request(url, { method: row.method, headers: { 'content-type': 'application/json' }, ...(row.body === undefined ? {} : { body: row.body }) }), url, url.pathname.split('/').filter(Boolean))
    value = { status: response.status, body: await response.json() }
  }
  else throw new Error('Undeclared MCP config fixture action')
  observations.push(value ?? null)
}
process.stdout.write(JSON.stringify(observations))
