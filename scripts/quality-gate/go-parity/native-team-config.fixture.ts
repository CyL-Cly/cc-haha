import { mkdir, readFile, writeFile, stat, readdir } from 'node:fs/promises'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Team config')
if (process.env.CC_HAHA_TEAM_CONFIG_FIXTURE !== '1') throw new Error('Missing team config fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const source = await import('../../../src/utils/swarm/teamHelpers')
const rows = []
for (const row of input.cases) {
  const directory = source.getTeamDir(row.name), path = source.getTeamFilePath(row.name)
  if (row.initial !== undefined) { await mkdir(directory, { recursive: true }); await writeFile(path, row.initial) }
  let error: string | null = null, unchanged: boolean | null = null
  try {
    if (row.write) await source.writeTeamFileAsync(row.name, row.write)
    if (row.mutate) await source.mutateTeamFileAsync(row.name, current => Object.assign(current, row.mutate))
    if (row.noop) {
      const before = await stat(path)
      await source.mutateTeamFileAsync(row.name, () => {})
      unchanged = (await stat(path)).mtimeMs === before.mtimeMs
    }
    if (row.parallel) await Promise.all(row.parallel.map((id: string) => source.mutateTeamFileAsync(row.name, current => { current.members.push({ name: id }) })))
  } catch (err) { error = (err as Error).message }
  let physical: string | null = null, mode: number | null = null, files: string[] = []
  try { physical = await readFile(path, 'utf8'); mode = (await stat(path)).mode & 0o777 } catch {}
  try { files = await readdir(directory) } catch {}
  rows.push({ sync: source.readTeamFile(row.name), async: await source.readTeamFileAsync(row.name), physical, mode, files, error, unchanged })
}
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ rows, capabilities }), () => resolve()))
process.exit(0)
