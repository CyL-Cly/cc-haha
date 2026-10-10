import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from './client-options-capabilities'
import { join } from 'node:path'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual Team generation and terminal tasks')
if (process.env.CC_HAHA_TEAM_LIFECYCLE_STATE_FIXTURE !== '1') throw new Error('Missing lifecycle fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const tasks = await import('../../../src/utils/tasks')
const tokens = new Map<string, any>(), rows = []
const path = join(input.homeDir, '.claude/tasks/.lifecycle-locks/team.state.json')
for (const step of input.steps) {
  let value: any = null, error: string | null = null
  try {
    if (step.seed) { await mkdir(join(path, '..'), { recursive: true }); await writeFile(path, JSON.stringify(step.seed)) }
    else if (step.action === 'read') value = await tasks.readTaskListLifecycleState('team')
    else if (step.action === 'begin') { value = await tasks.beginTaskListLifecycle('team', step.identity); tokens.set(step.token, value) }
    else if (step.action === 'complete') value = await tasks.completeTaskListLifecycle('team', step.snapshot, tokens.get(step.token))
    else if (step.action === 'create') value = await tasks.createTask('team', { subject: 'blocked stale writer', description: '', status: 'pending', blocks: [], blockedBy: [] })
  } catch (err) { error = (err as Error).message }
  rows.push({ value, error, file: await readFile(path, 'utf8').catch(() => null) })
}
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ rows, capabilities }), () => resolve()))
process.exit(0)
