import { assertDisposableFixtureHome } from './fixture-guard'
import { mkdirSync, writeFileSync, readFileSync } from 'node:fs'
import { join } from 'node:path'

const home = assertDisposableFixtureHome(process.env.HOME, 'Native remote settings')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
process.env.CLAUDE_CODE_LOCAL_VERSION = 'remote-settings-fixture'
process.env.CLAUDE_CODE_LOCAL_BUILD_TIME = 'fixture-build'
await import('../../../preload')
mkdirSync(join(home, '.claude', 'cc-haha'), { recursive: true })
writeFileSync(join(home, '.claude', '.credentials.json'), '{}')
const { handleApiRequest } = await import('../../../src/server/router')
const results = []
for (const row of input.cases) {
  writeFileSync(join(home, '.claude', 'settings.json'), JSON.stringify(input.settings))
  writeFileSync(join(home, '.claude', 'cc-haha', 'providers.json'), JSON.stringify(input.index))
  const url = new URL(row.path, 'http://127.0.0.1:3000')
  const request = new Request(url, { method: row.method ?? 'GET', ...(row.body !== undefined ? { headers: { 'content-type': 'application/json' }, body: row.body } : {}) })
  const response = await handleApiRequest(request, url, { remoteBrowser: true })
  results.push({ status: response.status, body: await response.json(), settings: JSON.parse(readFileSync(join(home, '.claude', 'settings.json'), 'utf8')), index: JSON.parse(readFileSync(join(home, '.claude', 'cc-haha', 'providers.json'), 'utf8')) })
}
process.stdout.write(JSON.stringify(results))
