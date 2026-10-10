import { mkdir, writeFile, readFile, utimes } from 'node:fs/promises'
import { dirname, join } from 'node:path'
import { spyOn } from 'bun:test'
import * as crypto from 'node:crypto'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual Team reader files and member messages')
if (process.env.CC_HAHA_TEAM_READER_FIXTURE !== '1') throw new Error('Missing Team reader fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
process.chdir(input.homeDir)
state.setCwdState(process.cwd()); state.setOriginalCwd(process.cwd()); state.setProjectRoot(process.cwd())
for (const file of input.files) {
  const path = join(input.homeDir, file.path)
  await mkdir(dirname(path), { recursive: true })
  await writeFile(path, file.content.replaceAll('$HOME', input.homeDir))
  if (file.mtime !== undefined) await utimes(path, file.mtime / 1000, file.mtime / 1000)
}
const OriginalDate = Date
globalThis.Date = class extends OriginalDate { constructor(value?: any) { super(value === undefined ? input.now : value) }; static now() { return input.now } } as DateConstructor
spyOn(crypto, 'randomUUID').mockReturnValue('fixture-reader-id' as any)
const { TeamService } = await import('../../../src/server/services/teamService')
const service = new TeamService()
const tasks = await import('../../../src/utils/tasks')
for (const task of input.tasks ?? []) await tasks.createTask(tasks.getCanonicalTeamTaskListId(task.team), task.value)
const rows = []
for (const row of input.rows) {
  let data: any = null, status = 200, error: any = null
  try {
    if (row.method === 'list') data = await service.listTeams()
    if (row.method === 'get') data = await service.getTeam(row.name)
    if (row.method === 'messages') data = await (service as any).readWorkbenchMessages(row.name)
    if (row.method === 'workbench') data = await service.getWorkbench(row.name)
    if (row.method === 'send') { await service.sendMemberMessage(row.name, row.id, row.content); data = { ok: true } }
  } catch (failure) { status = (failure as any).statusCode ?? 500; error = { code: (failure as any).code ?? 'INTERNAL_ERROR', message: status === 500 ? 'An unexpected error occurred' : (failure as Error).message } }
  let inbox: any = null
  if (row.inbox) { try { inbox = JSON.parse(await readFile(join(input.homeDir, row.inbox), 'utf8')) } catch {} }
  rows.push({ data, status, error, inbox })
}
process.stdout.write(JSON.stringify({ rows, capabilities }))
