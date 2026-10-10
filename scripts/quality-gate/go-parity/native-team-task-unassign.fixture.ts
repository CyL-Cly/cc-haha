import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual teammate task unassignment transaction')
if (process.env.CC_HAHA_TEAM_UNASSIGN_FIXTURE !== '1') throw new Error('Missing unassign fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { createTask, unassignTeammateTasks, readTaskListSnapshot, updateTaskAtomically } = await import('../../../src/utils/tasks')
const rows = []
for (let index = 0; index < input.rows.length; index++) {
  const row = input.rows[index], team = `actual-unassign-${index}`
  for (const task of row.tasks) await createTask(team, task)
  const result = await unassignTeammateTasks(team, row.id, row.name, row.reason)
  const snapshot = await readTaskListSnapshot(team)
  rows.push({ result, tasks: snapshot.tasks, revision: snapshot.revision })
}
process.stdout.write(JSON.stringify({ rows, capabilities }))
