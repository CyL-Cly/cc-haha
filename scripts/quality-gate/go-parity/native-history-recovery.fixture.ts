import { join } from 'node:path'
import { mkdir, writeFile, appendFile } from 'node:fs/promises'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'History recovery')
if (process.env.CC_HAHA_HISTORY_RECOVERY_FIXTURE !== '1') throw new Error('Unowned history recovery fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { SessionService } = await import('../../../src/server/services/sessionService')
const service = new SessionService({ getMode: () => 'off' } as any)
const project = join(home,'.claude','projects','private')
await mkdir(project,{recursive:true})
console.log = () => {}
const results = []
for (const row of input.cases) {
  const path=join(project,row.id+'.jsonl')
  await writeFile(path,row.content)
  const first=await service.getSessionHistoryRecovery(row.id)
  const cached=await service.getSessionHistoryRecovery(row.id)
  const observation: any = { first, sameCachedObject:first===cached }
  if (row.append !== undefined) {
    await appendFile(path,row.append)
    observation.after=await service.getSessionHistoryRecovery(row.id)
    observation.changedCacheObject=observation.after!==first
  }
  results.push({name:row.name,observation})
}
const httpResults = []
if (input.httpCases) {
  const { handleSessionsApi } = await import('../../../src/server/api/sessions')
  for (const row of input.httpCases) {
    const segments = ['api', 'sessions', row.id, 'history-recovery', ...(row.extra ?? [])]
    const url = new URL('http://127.0.0.1/' + segments.join('/'))
    const response = await handleSessionsApi(new Request(url, {
      method: row.method ?? 'GET',
      ...(row.body !== undefined ? { body: row.body } : {}),
    }), url, segments)
    httpResults.push({ status: response.status, body: await response.json() })
  }
}
if(capabilities.blocked.length) throw new Error('History recovery blocked capability: '+capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({results,httpResults,capabilities}))
