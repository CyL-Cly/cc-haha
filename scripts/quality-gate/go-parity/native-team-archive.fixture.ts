import { mkdir, writeFile, readFile } from 'node:fs/promises'
import { dirname } from 'node:path'
import { setSystemTime } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual Team archive state machine')
if (process.env.CC_HAHA_TEAM_ARCHIVE_FIXTURE !== '1') throw new Error('Missing Team archive fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { TeamService } = await import('../../../src/server/services/teamService')
const service: any = new TeamService()
const observations = []
for (const file of input.files ?? []) { const path = input.homeDir + '/' + file.path; await mkdir(dirname(path), { recursive: true }); await writeFile(path, file.content) }
if (input.now !== undefined) setSystemTime(input.now)
for (const row of input.rows) {
  const path = service.getWorkbenchArchivePath(row.session)
  if (row.initial !== undefined) { await mkdir(dirname(path), { recursive: true }); await writeFile(path, row.initial) }
  let error: string | null = null
  try {
    for (const snapshot of row.snapshots ?? []) await service.archiveWorkbenchSnapshot(snapshot)
    if (row.parallel) await Promise.all(row.parallel.map((snapshot: any) => service.archiveWorkbenchSnapshot(snapshot)))
    if (row.mark) await service.markWorkbenchArchiveDeleted(row.mark.team, row.session, row.mark.incarnation)
    if (row.delete) await service.deleteTeam(row.delete)
  } catch (e) { error = (e as Error).message }
  const document = await service.readArchiveDocument(row.session)
  const selected = (row.lookups ?? []).map((lookup: any) => service.archiveEntryForLookup(document, lookup))
  let physical: string | null = null
  try { physical = await readFile(path, 'utf8') } catch {}
  if (row.delete) {
    const { access } = await import('node:fs/promises')
    const exists = async (path: string) => { try { await access(path); return true } catch { return false } }
    observations.push({ document, error, teamExists: await exists(input.homeDir + '/.claude/teams/' + row.delete), tasksExist: await exists(input.homeDir + '/.claude/tasks/' + row.delete) })
  } else if (row.timeline) {
    let value = null
    try { value = await service.getWorkbenchForSession(row.session, row.timeline) } catch (e) { error = (e as Error).message }
    observations.push({ value, error })
  } else observations.push({ document, selected, physical, error })
}
setSystemTime()
process.stdout.write(JSON.stringify({ observations, capabilities }))
