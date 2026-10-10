import { mkdir, writeFile, appendFile, utimes } from 'node:fs/promises'
import { dirname } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual bounded Team transcript projection')
if (process.env.CC_HAHA_TEAM_PROJECTION_FIXTURE !== '1') throw new Error('Missing bounded Team fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { readTeamTranscriptProjection } = await import('../../../src/server/services/teamTranscriptProjection')
const observations = []
for (const row of input.rows) {
  if (!row.path.startsWith(input.homeDir + '/')) throw new Error('Non-private projection path')
  if (row.write !== undefined) { await mkdir(dirname(row.path), { recursive: true }); await writeFile(row.path, row.write) }
  if (row.append !== undefined) await appendFile(row.path, row.append)
  if (row.mtime !== undefined) await utimes(row.path, row.mtime, row.mtime)
  const project = await readTeamTranscriptProjection(row.path)
  observations.push({ messages: project.messages, complete: project.complete, sourceVersion: project.sourceVersion, readBytes: project.readBytes })
}
process.stdout.write(JSON.stringify({ observations, capabilities }))
