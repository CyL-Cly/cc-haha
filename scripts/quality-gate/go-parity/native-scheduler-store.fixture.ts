import { mkdirSync, writeFileSync, readFileSync, existsSync } from 'node:fs'
import { dirname, join } from 'node:path'
import * as crypto from 'node:crypto'
import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Scheduler store')
if (process.env.CC_HAHA_SCHEDULER_STORE_FIXTURE !== '1') throw new Error('Missing scheduler store fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
spyOn(Date, 'now').mockImplementation(() => input.now)
let random = 0
spyOn(crypto, 'randomBytes').mockImplementation((count: number) => Buffer.alloc(count, ++random) as any)
const { CronService } = await import('../../../src/server/services/cronService')
const service = new CronService(), sibling = new CronService()
const file = join(process.env.CLAUDE_CONFIG_DIR!, 'scheduled_tasks.json')
mkdirSync(dirname(file), { recursive: true })
const rows = []
for (const row of input.rows) {
  if (row.initial !== undefined) writeFileSync(file, row.initial)
  const results = []
  for (const action of row.actions) {
    const selected = action.sibling ? sibling : service
    try {
      let value
      if (action.type === 'list') value = await selected.listTasks()
      else if (action.type === 'create') value = await selected.createTask(action.input)
      else if (action.type === 'update') value = await selected.updateTask(action.id, action.input)
      else if (action.type === 'delete') value = await selected.deleteTask(action.id)
      else if (action.type === 'lastFired') value = await selected.updateLastFired(action.id, action.timestamp)
      results.push({ value: value ?? null })
    } catch (error) {
      results.push({ error: { statusCode: (error as any).statusCode, code: (error as any).code, message: (error as Error).message } })
    }
  }
  rows.push({ results, file: existsSync(file) ? readFileSync(file, 'utf8') : null })
}
process.stdout.write(JSON.stringify({ rows, capabilities }))
