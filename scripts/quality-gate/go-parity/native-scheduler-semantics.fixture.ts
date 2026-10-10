import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Scheduler semantics')
if (process.env.CC_HAHA_SCHEDULER_SEMANTICS_FIXTURE !== '1') throw new Error('Missing scheduler semantics fixture')
const absent = [...absentManagedSettingsFixturePaths()]
for (let directory = dirname(input.homeDir);; directory = dirname(directory)) {
  absent.push(join(directory, '.git'))
  if (directory === dirname(directory)) break
}
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absent })
await import('../../../preload')
const { fieldMatches, cronMatches, extractAssistantText, resolveCronTaskTimeoutMs } = await import('../../../src/server/services/cronScheduler')
const fields = input.fields.map((row: any) => fieldMatches(row.field, row.value))
const cron = input.cron.map((row: any) => cronMatches(row.cron, new Date(row.date)))
const text = input.text.map((value: string) => extractAssistantText(value))
const timeouts = input.timeouts.map((value: string) => resolveCronTaskTimeoutMs({ CC_HAHA_TASK_TIMEOUT_MS: value }))
process.stdout.write(JSON.stringify({ fields, cron, text, timeouts, capabilities }))
