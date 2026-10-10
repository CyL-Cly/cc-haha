import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual Task claim lifecycle/list/file transaction')
if (process.env.CC_HAHA_TASK_CLAIM_FIXTURE !== '1') throw new Error('Missing Task claim fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { createTask, claimTask, readTaskListSnapshot } = await import('../../../src/utils/tasks')
const rows = []
for (let index = 0; index < input.rows.length; index++) {
  const row = input.rows[index], list = `actual-claim-${index}`
  for (const task of row.tasks) await createTask(list, task)
  let result: any = null, error: string | null = null
  try { result = await claimTask(list, row.id, row.claimant, row.options) }
  catch (failure) { error = (failure as Error).message }
  const snapshot = await readTaskListSnapshot(list)
  rows.push({ result, error, tasks: snapshot.tasks, revision: snapshot.revision })
}
process.stdout.write(JSON.stringify({ rows, capabilities }))
