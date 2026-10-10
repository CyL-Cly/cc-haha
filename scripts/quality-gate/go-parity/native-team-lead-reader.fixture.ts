import { parse } from 'acorn'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual headless leader physical inbox consumer')
if (process.env.CC_HAHA_LEAD_READER_FIXTURE !== '1') throw new Error('Missing leader reader fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const mailbox = await import('../../../src/utils/teammateMailbox')
const helpers = await import('../../../src/utils/swarm/teamHelpers')
const tasks = await import('../../../src/utils/tasks')
const { partitionLeadMailboxMessages } = await import('../../../src/utils/swarm/printLeaderPermissionBridge')
const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/cli/print.ts', import.meta.url)).text())
const ast = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
let cleanup: any, format: any
function walk(node: any) {
  if (!node || typeof node !== 'object') return
  if (node.type === 'ForOfStatement' && source.slice(node.start, node.end).includes('isShutdownApproved(m.text)')) cleanup = node
  if (node.type === 'VariableDeclarator' && node.id?.name === 'formatted' && source.slice(node.init.start, node.init.end).includes('partitioned.remaining')) format = node.init
  for (const value of Object.values(node)) { if (Array.isArray(value)) value.forEach(walk); else if (value && typeof value === 'object') walk(value) }
}
walk(ast)
if (!cleanup || !format) throw new Error('Actual print leader consumer changed')
const AsyncFunction = Object.getPrototypeOf(async function () {}).constructor
const processApprovals = new AsyncFunction('partitioned','teamName','refreshedState','isShutdownApproved','logForDebugging','removeTeammateFromTeamFile','unassignTeammateTasks','setAppState',source.slice(cleanup.start, cleanup.end))
const formatMessages = new Function('partitioned','TEAMMATE_MESSAGE_TAG',`return (${source.slice(format.start, format.end)})`)
const rows = []
for (let index = 0; index < input.rows.length; index++) {
  const row = input.rows[index], team = `actual-reader-${index}`
  await helpers.writeTeamFileAsync(team, { name: team, createdAt: 1, members: row.members })
  for (const task of row.tasks) await tasks.createTask(team, task)
  for (const message of row.messages) await mailbox.writeToMailbox('team-lead', message, team)
  let current: any = { teamContext: row.snapshot }
  const unread = await mailbox.readUnreadMessages('team-lead', team)
  if (unread.length) await mailbox.markMessagesAsRead('team-lead', team)
  const partition = partitionLeadMailboxMessages(unread)
  await processApprovals(partition,team,current,mailbox.isShutdownApproved,()=>{},helpers.removeTeammateFromTeamFile,tasks.unassignTeammateTasks,(update:any)=>{current=update(current)})
  const snapshot = await tasks.readTaskListSnapshot(team)
  rows.push({ partition, context: current.teamContext, formatted: partition.remaining.length ? formatMessages(partition,'teammate-message') : null, mailbox: await mailbox.readMailbox('team-lead',team), members: (await helpers.readTeamFileAsync(team))?.members, tasks: snapshot.tasks, revision:snapshot.revision })
}
process.stdout.write(JSON.stringify({ rows, capabilities }))
