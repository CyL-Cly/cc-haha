import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from './client-options-capabilities'
import { exposeSourceBindings } from './source-exports'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual team supervisor helpers')
if (process.env.CC_HAHA_TEAM_SUPERVISOR_FIXTURE !== '1') throw new Error('Missing supervisor fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
Bun.plugin({ name: 'actual-supervisor-private-bindings', setup(build) {
  build.onLoad({ filter: /\/teamPlanRuntime\.ts$/ }, async args => ({ contents: exposeSourceBindings(await Bun.file(args.path).text(), ['memberBrief', 'readyTasksOf', 'waitsForDependencies', 'capText', 'firstLine', 'autoContinuePrompt', 'approvedShutdown', 'isShutdownApproval']), loader: 'ts' }))
} })
const runtime: any = await import('../../../src/server/services/teamPlanRuntime')
const results = (input.rows ?? []).map((row: any) => ({
  brief: runtime.memberBrief(row.member, row.members, row.proposalTasks, row.mapping),
  ready: runtime.readyTasksOf(row.member.name, row.tasks),
  waiting: runtime.waitsForDependencies(row.member.name, row.tasks),
  failures: row.failures.map((text: string) => ({ transient: runtime.isTransientWorkerFailure(text), reason: runtime.firstLine(text), capped: runtime.capText(text, 200), retry: runtime.autoContinuePrompt(runtime.firstLine(text), 1) })),
}))
const shutdownRows = []
if (input.shutdownRows) {
  const mailbox = await import('../../../src/utils/teammateMailbox')
  for (const [i, row] of input.shutdownRows.entries()) {
    const teamName = 'physical-shutdown-' + i
    for (const message of row.messages) {
      if (!await mailbox.writeToMailbox('team-lead', message, teamName)) throw new Error('Original physical shutdown history producer failed')
    }
    const worker = { member: { name: row.name }, shutdownRequestedAt: row.since }
    const approved = await runtime.approvedShutdown({ plan: { teamName } }, worker)
    shutdownRows.push({ approved, clearedRequest: worker.shutdownRequestedAt === undefined, parsed: row.messages.map((message: any) => mailbox.isShutdownApproved(message.text)), processTypes: row.messages.map((message: any) => runtime.isShutdownApproval(message.text)) })
  }
}
if (capabilities.blocked.length) throw new Error('Blocked actual supervisor shutdown fixture: ' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({ results, shutdownRows, capabilities }))
process.exit(0)
