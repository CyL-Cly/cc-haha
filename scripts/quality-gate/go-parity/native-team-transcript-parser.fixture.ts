import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual Team transcript page parser')
if (process.env.CC_HAHA_TEAM_TRANSCRIPT_FIXTURE !== '1') throw new Error('Missing Team transcript fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { TeamService } = await import('../../../src/server/services/teamService')
const { projectTeamWorkbenchesFromTranscript } = await import('../../../src/server/services/teamService')
let index: any
if (input.indexed) {
  const { createLocalIndexCoordinator } = await import('../../../src/server/services/localIndex/coordinator')
  let onBatch: any
  index = createLocalIndexCoordinator({ resolveMode: () => ({ mode: 'on', warningCode: null }), resolveScope: () => input.homeDir + '/.claude', resolveDatabasePath: () => input.homeDir + '/.claude/cc-haha/db/index-v1.sqlite', schedule: () => {}, createWatcher: (options: any) => { onBatch = options.onBatch; return { start: async () => {}, stop: async () => {}, queueFullSweep() {}, queueTranscriptPath() {} } } })
  await index.start()
  await onBatch({ fullSweep: true, paths: [] })
}
const service: any = new TeamService(index ? { localIndexGateway: index } : {})
const pages: any[] = []
for (const row of input.rows) {
  const options = { ...row.options }
  if (row.previous !== undefined) { const previous = pages[row.previous]?.page ?? pages[row.previous]; options.signature = previous.signature; options.cursor = previous.cursor; options.afterOrdinal = previous.afterOrdinal }
  if (row.project) {
    pages.push(projectTeamWorkbenchesFromTranscript(row.project.session, row.project.messages))
  } else if (row.indexed) {
    if (!row.indexed.startsWith(input.homeDir + '/')) throw new Error('Non-private indexed transcript')
    if (row.stop) await index.stop()
    pages.push(await service.readIndexedTranscriptPage(row.indexed, options, row.projection ?? {}))
  } else if (row.member) {
    try { pages.push({ page: await service.getMemberTranscriptPage(row.member.team, row.member.agent, options), error: null }) }
    catch (error: any) { pages.push({ page: null, error: { status: error.statusCode ?? 500, code: error.code ?? null, message: error.message } }) }
  } else if (row.files) {
    for (const file of row.files) if (!file.filePath.startsWith(input.homeDir + '/')) throw new Error('Non-private transcript source')
    try {
      const one = row.files.length === 1 ? row.files[0] : undefined
      const page = one
        ? await service.parseTranscriptFilePage(one.filePath, options, one.ownerAgentId ? { ownerAgentId: one.ownerAgentId, ownerAgentIds: [one.ownerAgentId] } : { ownerAgentIds: [] })
        : await service.parseTranscriptFragmentsPage(row.files, options)
      pages.push({ page, error: null })
    }
    catch (error: any) { pages.push({ page: null, error: { status: error.statusCode ?? 500, code: error.code ?? null, message: error.message } }) }
  } else pages.push(service.parseTranscriptBufferPage(Buffer.from(row.text), options, row.ctime ?? 0, row.identity ?? null, row.projection ?? {}))
}
await index?.stop()
process.stdout.write(JSON.stringify({ pages, capabilities }))
