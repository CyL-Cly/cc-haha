import { join } from 'node:path'
import { mkdir, writeFile, readFile, stat, chmod } from 'node:fs/promises'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'Session deletion')
if (process.env.CC_HAHA_SESSION_DELETE_FIXTURE !== '1') throw new Error('Unowned deletion fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { handleSessionsApi } = await import('../../../src/server/api/sessions')
const { sessionService } = await import('../../../src/server/services/sessionService')
const { conversationService } = await import('../../../src/server/services/conversationService')
// Source emits an informational adapter cleanup log. Keep JSON stdout singular.
const logs: string[] = []
console.log = (...args: any[]) => { logs.push(args.map(String).join(' ')) }
const results = []
for (const row of input.cases) {
  const project = join(home, '.claude', 'projects', 'private')
  await mkdir(project, { recursive: true })
  for (const id of input.seedIds) await writeFile(join(project, id + '.jsonl'), JSON.stringify({ type: 'session-meta', workDir: home }) + '\n')
  await writeFile(join(home, '.claude', 'adapter-sessions.json'), input.mapping)
  ;(sessionService as any).invalidateSessionListCache()
  if(row.readOnly) await chmod(project,0o555)
  const segments = ['api', 'sessions', row.single ?? 'batch-delete']
  const url = new URL('http://127.0.0.1/' + segments.join('/'))
  const response = await handleSessionsApi(new Request(url, { method: row.method ?? 'POST', ...(row.body !== undefined ? { body: row.body, headers: { 'content-type': 'application/json' } } : {}) }), url, segments)
  if(row.readOnly) await chmod(project,0o700)
  const exists = []
  for (const id of input.seedIds) {
    try { await stat(join(project, id + '.jsonl')); exists.push(true) } catch { exists.push(false) }
  }
  results.push({ status: response.status, response: await response.json(), exists, mapping: await readFile(join(home, '.claude', 'adapter-sessions.json'), 'utf8'), deleted: input.seedIds.map((id: string) => (conversationService as any).deletedSessions.has(id)) })
  for (const id of input.seedIds) conversationService.unmarkSessionDeleted(id)
}
if (capabilities.blocked.length) throw new Error('Deletion accessed blocked external capability: ' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({ results, capabilities }).replaceAll(home,'$HOME'))
