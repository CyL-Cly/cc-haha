import { mkdir, writeFile, rm } from 'node:fs/promises'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Original TeamWatcher physical file events')
if (process.env.CC_HAHA_TEAM_WATCHER_FIXTURE !== '1') throw new Error('Missing TeamWatcher fixture binding')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { TeamWatcher } = await import('../../../src/server/services/teamWatcher')
const { TeamService } = await import('../../../src/server/services/teamService')
const service = new TeamService()
const deliveries: any[] = []
const watcher = new TeamWatcher((message, leadSessionId) => deliveries.push({ message, leadSessionId: leadSessionId ?? null }), service)
const rows = []
for (const step of input.steps) {
  for (const file of step.files ?? []) {
    const path = join(input.homeDir, file.path)
    await mkdir(dirname(path), { recursive: true })
    await writeFile(path, file.content)
  }
  for (const path of step.remove ?? []) await rm(join(input.homeDir, path), { recursive: true, force: true })
  await watcher.checkNow()
  rows.push(deliveries.splice(0))
}
watcher.stop()
process.stdout.write(JSON.stringify({ rows, capabilities }))
