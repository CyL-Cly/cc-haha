import { assertDisposableFixtureHome } from './fixture-guard'
import { mkdirSync, writeFileSync, rmSync } from 'node:fs'
import { join } from 'node:path'
import { mock } from 'bun:test'

const home = assertDisposableFixtureHome(process.env.HOME, 'Standalone desktop models')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
process.env.CLAUDE_CODE_LOCAL_VERSION = 'standalone-model-fixture'
process.env.CLAUDE_CODE_LOCAL_BUILD_TIME = 'fixture-build'
await import('../../../preload')
mkdirSync(join(home, '.claude'), { recursive: true })
writeFileSync(join(home, '.claude', '.credentials.json'), '{}')
const portable = await import('../../../src/utils/execFileNoThrowPortable')
const execa = await import('execa')
mock.module('../../../src/utils/execFileNoThrowPortable', () => ({ ...portable, execSyncWithDefaults_DEPRECATED() { throw new Error('isolated unavailable Keychain') } }))
mock.module('execa', () => ({ ...execa, execaSync() { return { exitCode: 1 } } }))
const { handleModelsApi } = await import('../../../src/server/api/models')
const { handleProvidersApi } = await import('../../../src/server/api/providers')
const { SettingsService } = await import('../../../src/server/services/settingsService')
const keys = new Set<string>()
const results = []
for (const row of input.cases) {
  mkdirSync(join(home, '.claude', 'cc-haha'), { recursive: true })
  const oauth = join(home, '.claude', 'cc-haha', 'oauth.json')
  if (row.oauth) writeFileSync(oauth, JSON.stringify(row.oauth))
  else rmSync(oauth, { force: true })
  writeFileSync(join(home, '.claude', 'settings.json'), JSON.stringify(row.settings ?? {}))
  for (const key of keys) delete process.env[key]
  keys.clear()
  for (const [key, value] of Object.entries(row.env ?? {})) { keys.add(key); process.env[key] = value as string }
  const url = new URL(row.path, 'http://127.0.0.1:3000')
  const request = new Request(url, { method: row.method ?? 'GET', ...(row.body ? { headers: { 'content-type': 'application/json' }, body: row.body } : {}) })
  const segments = url.pathname.split('/').filter(Boolean)
  const response = row.path.startsWith('/api/providers/') ? await handleProvidersApi(request, url, segments) : await handleModelsApi(request, url, segments)
  results.push({ status: response.status, body: await response.json(), settings: await new SettingsService().getUserSettings() })
}
process.stdout.write(JSON.stringify(results))
