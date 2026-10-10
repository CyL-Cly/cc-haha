import { join } from 'node:path'
import { mkdir, writeFile } from 'node:fs/promises'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'Side chat cold runtime')
if (process.env.CC_HAHA_SIDE_CHAT_COLD_FIXTURE !== '1') throw new Error('Unowned side runtime fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { handleSessionsApi } = await import('../../../src/server/api/sessions')
const { getSideChat } = await import('../../../src/server/services/sideChatRegistry')
const { conversationService } = await import('../../../src/server/services/conversationService')
const { handleWebSocket, __enqueueRuntimeTransitionForTests, __resetWebSocketHandlerStateForTests } = await import('../../../src/server/ws/handler')
console.log = () => {}
const project = join(home, '.claude', 'projects', 'private')
await mkdir(project, { recursive: true })
await writeFile(join(project,input.parentId+'.jsonl'),input.content.replaceAll('$HOME',home))
const url = new URL('http://127.0.0.1/api/sessions/'+input.parentId+'/side-chats')
const response = await handleSessionsApi(new Request(url,{method:'POST',body:JSON.stringify({sideChatId:input.cliId})}),url,['api','sessions',input.parentId,'side-chats'])
if (response.status !== 200) throw new Error(await response.text())
const id = 'side-'+input.cliId
const events: any[] = []
const socket = { data: { sessionId:id,connectedAt:0,channel:'client' as const,sdkToken:null,serverPort:1,serverHost:'127.0.0.1' },send: (data: string) => { events.push(JSON.parse(data));return data.length },close: () => {} }
handleWebSocket.open(socket)
await __enqueueRuntimeTransitionForTests(id,Promise.resolve())
const results = []
for (const message of input.controls) {
  events.length = 0
  handleWebSocket.message(socket,JSON.stringify(message))
  await __enqueueRuntimeTransitionForTests(id,Promise.resolve())
  const side = getSideChat(id)!
  results.push({events:events.filter(event => ['error','runtime_config_applied'].includes(event.type)),started:side.started,active:conversationService.hasSession(id),model:side.launchInfo.runtimeModelId,provider:side.launchInfo.runtimeProviderId ?? null,effort:side.launchInfo.effortLevel})
}
__resetWebSocketHandlerStateForTests()
if (capabilities.blocked.length) throw new Error('Blocked side runtime capability: '+capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({results,capabilities}))
