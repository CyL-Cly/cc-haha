import { mkdir, writeFile } from 'node:fs/promises'
import { dirname } from 'node:path'
import { setSystemTime } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual Teams HTTP contract')
if (process.env.CC_HAHA_TEAMS_API_FIXTURE !== '1') throw new Error('Missing Teams HTTP fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { handleTeamsApi } = await import('../../../src/server/api/teams')
for (const file of input.files) { const path = input.homeDir + '/' + file.path; await mkdir(dirname(path), { recursive: true }); await writeFile(path, file.content) }
setSystemTime(input.now)
const observations = []
for (const row of input.rows) {
  const request = new Request('http://127.0.0.1' + row.path, { method: row.method, ...(row.body !== undefined ? { body: row.body } : {}) })
  const url = new URL(request.url)
  const response = await handleTeamsApi(request, url, row.parts ?? url.pathname.split('/').filter(Boolean))
  observations.push({ status: response.status, body: await response.json() })
}
setSystemTime()
process.stdout.write(JSON.stringify({ observations, capabilities }))
