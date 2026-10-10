import { join } from 'node:path'
import { mkdir, writeFile, appendFile } from 'node:fs/promises'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'Side chats')
if (process.env.CC_HAHA_SIDE_CHATS_FIXTURE !== '1') throw new Error('Unowned side chat fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { handleSessionsApi } = await import('../../../src/server/api/sessions')
const { sessionService } = await import('../../../src/server/services/sessionService')
const { getSideChat, sideChatSummary, SIDE_CHAT_BOUNDARY } = await import('../../../src/server/services/sideChatRegistry')
const realDate = Date
;(globalThis as any).Date = class extends realDate {
  constructor(...args: any[]) { super(args.length ? args[0] : input.now) }
}
const project = join(home, '.claude', 'projects', 'private')
await mkdir(project, { recursive: true })
const path = join(project, input.parentId + '.jsonl')
await writeFile(path, input.content.replaceAll('$HOME', home))
const results = []
console.log = () => {}
for (const row of input.operations) {
  if(row.append) { await appendFile(path,row.append);(sessionService as any).invalidateSessionListCache() }
  const parts = ['api','sessions',row.parentId ?? input.parentId,...(row.resource === '' ? [] : [row.resource ?? 'side-chats']),...(row.childId ? [row.childId] : [])]
  const url = new URL('http://127.0.0.1/' + parts.join('/'))
  const req = new Request(url, { method:row.method ?? 'POST',...(row.body !== undefined ? { body:row.body } : {}) })
  const response = await handleSessionsApi(req,url,parts)
  const text = await response.text()
  const entry = getSideChat(input.sideId)
  results.push({status:response.status,body:text ? JSON.parse(text) : null,entry:entry ? {parentSessionId:entry.parentSessionId,cliSessionId:entry.cliSessionId,resumeAt:entry.resumeAt,resumePath:entry.resumePath,started:entry.started,closed:entry.closed,summary:sideChatSummary(entry),repositoryCleared:entry.launchInfo.repository === undefined,worktreeCleared:entry.launchInfo.worktreeSession === undefined} : null})
}
if (capabilities.blocked.length) throw new Error('Side chat accessed blocked external capability: ' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({results,boundary:SIDE_CHAT_BOUNDARY,capabilities}).replaceAll(home,'$HOME'))
