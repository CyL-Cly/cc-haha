import { dirname, join } from 'node:path'
import { mkdir, writeFile } from 'node:fs/promises'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual in-process wait/claim mailbox lifecycle')
if (process.env.CC_HAHA_INPROCESS_TEAM_INBOX_FIXTURE !== '1') throw new Error('Missing teammate inbox fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const runner = await import('../../../src/utils/swarm/inProcessRunner')
const mailbox = await import('../../../src/utils/teammateMailbox')
const tasks = await import('../../../src/utils/tasks')
const { logForDebugging } = await import('../../../src/utils/debug')
const { sleep } = await import('../../../src/utils/sleep')
const { parse } = await import('acorn')
const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/utils/swarm/inProcessRunner.ts', import.meta.url)).text())
const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
const declaration = ast.body.find((node: any) => node.type === 'FunctionDeclaration' && node.id.name === 'waitForNextPromptOrShutdown')
if (!declaration) throw new Error('Original wait function unavailable')
const original = new Function('logForDebugging', 'sleep', 'readMailbox', 'isShutdownRequest', 'count', 'markMessageAsReadByIndex', 'TEAM_LEAD_NAME', 'claimNextInProcessTask', `${source.slice(declaration.start, declaration.end)}; return waitForNextPromptOrShutdown`)(logForDebugging, sleep, mailbox.readMailbox, mailbox.isShutdownRequest, (values: any[], predicate: any) => values.filter(predicate).length, mailbox.markMessageAsReadByIndex, 'team-lead', runner.claimNextInProcessTask)
const rows = []
for (let index = 0; index < input.rows.length; index++) {
  const row = input.rows[index], team = `Inbox-${index}`
  const path = join(input.homeDir, '.claude', 'teams', team, 'inboxes', 'worker.json')
  await mkdir(dirname(path), { recursive: true }); await writeFile(path, JSON.stringify(row.mailbox))
  for (const task of row.tasks) await tasks.createTask(tasks.getCanonicalTeamTaskListId(team), task)
  let state: any = { tasks: { actual: { type: 'in_process_teammate', pendingUserMessages: row.pending ?? [] } } }
  const abort = new AbortController(); if (row.abort) abort.abort()
  const identity: any = { agentId: `worker@${team}`, agentName: 'worker', teamName: team, planModeRequired: false, parentSessionId: 'parent' }
  const result = await original(identity, abort, 'actual', () => state, (update: any) => { state = update(state) })
  rows.push({ result, pending: state.tasks.actual.pendingUserMessages, mailbox: await mailbox.readMailbox('worker', team), tasks: await tasks.listTasks(tasks.getCanonicalTeamTaskListId(team)) })
}
process.stdout.write(JSON.stringify({ rows, capabilities }))
