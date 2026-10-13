import { dirname, join } from 'node:path'
import { mkdir, writeFile } from 'node:fs/promises'
import { assertDisposableFixtureHome } from './fixture-guard'
import { exposeSourceBindings } from './source-exports'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual in-process wait/claim mailbox lifecycle')
if (process.env.CC_HAHA_INPROCESS_TEAM_INBOX_FIXTURE !== '1') throw new Error('Missing teammate inbox fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
Bun.plugin({ name: 'actual-inprocess-wait-private-binding', setup(build) {
  build.onLoad({ filter: /\/inProcessRunner\.ts$/ }, async args => ({ contents: exposeSourceBindings(await Bun.file(args.path).text(), ['waitForNextPromptOrShutdown']), loader: 'ts' }))
} })
const runner = await import('../../../src/utils/swarm/inProcessRunner')
const mailbox = await import('../../../src/utils/teammateMailbox')
const tasks = await import('../../../src/utils/tasks')
const original = (runner as any).waitForNextPromptOrShutdown
if (typeof original !== 'function') throw new Error('Original wait function unavailable')
const rows = []
for (let index = 0; index < input.rows.length; index++) {
  const row = input.rows[index], team = `Inbox-${index}`
  const path = join(input.homeDir, '.claude', 'teams', team, 'inboxes', 'worker.json')
  await mkdir(dirname(path), { recursive: true }); await writeFile(path, JSON.stringify(row.mailbox))
  for (const task of row.tasks) await tasks.createTask(tasks.getCanonicalTeamTaskListId(team), task)
  let state: any = { tasks: { actual: { type: 'in_process_teammate', pendingUserMessages: row.pending ?? [] } } }
  const abort = new AbortController(); if (row.abort) abort.abort()
  const identity: any = { agentId: `worker@${team}`, agentName: 'worker', teamName: team, planModeRequired: false, parentSessionId: 'parent' }
  const cancel = new AbortController(); if(row.cancel)cancel.abort()
  const result = await original(identity, abort, 'actual', () => state, (update: any) => { state = update(state) }, {...(row.deadlineMs===undefined?{}:{deadline:Date.now()+row.deadlineMs}),...(row.cancel?{cancelSignal:cancel.signal}:{}),...(row.claimTasks===undefined?{}:{claimTasks:row.claimTasks})})
  rows.push({ result, pending: state.tasks.actual.pendingUserMessages, mailbox: await mailbox.readMailbox('worker', team), tasks: await tasks.listTasks(tasks.getCanonicalTeamTaskListId(team)) })
}
process.stdout.write(JSON.stringify({ rows, capabilities }))
