import { mock } from 'bun:test'
import { readFileSync, mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'

assertDisposableFixtureHome(process.env.HOME, 'DingTalk original index consumer')
if (process.env.CC_HAHA_DINGTALK_RUNTIME_FIXTURE !== '1') throw new Error('Unowned DingTalk runtime fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
console.log = console.warn = console.error = () => {}
Date.now = () => input.now
Math.random = () => 0.125
mkdirSync(process.env.CLAUDE_CONFIG_DIR!, { recursive: true })
writeFileSync(join(process.env.CLAUDE_CONFIG_DIR!, 'adapters.json'), JSON.stringify(input.config))
Bun.plugin({ name: 'original-dingtalk-index-observation', setup(build) {
  build.onLoad({ filter: /[\\/]adapters[\\/]dingtalk[\\/]index\.ts$/ }, args => ({
    contents: readFileSync(args.path, 'utf8') + '\nexport { handleCardCallback }; export const consumerSnapshot = (id) => ({ cards: Array.from(streamingCards.keys()), texts: Array.from(streamingCardText.entries()), buffers: Array.from(aiCardBuffers.keys()), permissions: Array.from(pendingPermissions.entries()).map(([id,set]) => [id, Array.from(set)]), permissionChats: Array.from(pendingPermissionChats.entries()) });\n', loader: 'ts',
  }))
} })
const realFetch = globalThis.fetch
globalThis.fetch = ((url: any, options: any) => realFetch(String(url).replace('https://api.dingtalk.com', input.peer), options)) as any
const sessions = new Map<string, string>(), packets: any[] = [], states: any[] = [], snapshots: any[] = []
mock.module('../../adapters/common/ws-bridge', () => ({ WsBridge: class {
  getSessionId(id: string) { return sessions.get(id) }
  hasSession(id: string) { return sessions.has(id) }
  isSessionOpen(id: string) { return sessions.has(id) }
  resetSession(id: string) { sessions.delete(id) }
  connectSession(id: string, sid: string) { sessions.set(id, sid) }
  onServerMessage() {}
  async waitForOpen() { return true }
  sendUserMessage(id: string, text: string, attachments?: any) { packets.push({ id: sessions.get(id), message: { type: 'user_message', content: text, ...(attachments ? { attachments } : {}) } }); return sessions.has(id) }
  sendPermissionResponse(id: string, requestId: string, allowed: boolean, rule?: string) { packets.push({ id: sessions.get(id), message: { type: 'permission_response', requestId, allowed, ...(rule ? { rule } : {}) } }); return sessions.has(id) }
  sendStopGeneration(id: string) { packets.push({ id: sessions.get(id), message: { type: 'stop_generation' } }); return sessions.has(id) }
  destroy() {}
} }))
mock.module('../../adapters/common/adapter-client', () => ({ createAdapterClient: () => ({ defaultWorkDir: input.work, httpClient: {
  sessionExists: async () => true, createSession: async () => 'created-session',
  listRecentProjects: async () => input.projects ?? [],
  matchProject: async (query: string) => query === '1' ? { project: input.projects?.[0] } : { project: null },
  getGitInfo: async () => ({ repoName: 'repo', workDir: input.work, branch: 'main' }), getTasksForSession: async () => [],
} }) }))
const runtime = await import('../../adapters/dingtalk/index')
const { enqueue } = await import('../../adapters/common/chat-queue')
runtime.sessionStore.set('dingtalk:dm:U1', 'session-1', input.work)
runtime.bridge.connectSession('dingtalk:dm:U1', 'session-1')
for (const action of input.actions) {
  if (action.kind === 'server') await runtime.handleServerMessage('dingtalk:dm:U1', action.message)
  if (action.kind === 'inbound') await runtime.handleRobotMessage({ conversationType: '1', senderStaffId: action.user ?? 'U1', senderNick: 'tester', sessionWebhook: input.peer + '/reply', msgtype: 'text', text: { content: action.text } })
  if (action.kind === 'route') await runtime.routeUserMessage('dingtalk:dm:U1', action.text, action.attachments ?? [])
  if (action.kind === 'clear') runtime.clearTransientChatState('dingtalk:dm:U1')
  if (action.kind === 'card') await (runtime as any).handleCardCallback(action.raw)
  await enqueue('dingtalk:dm:U1', async () => {})
  states.push({ ...runtime.getRuntimeState('dingtalk:dm:U1') })
  snapshots.push((runtime as any).consumerSnapshot('dingtalk:dm:U1'))
}
runtime.clearTransientChatState('dingtalk:dm:U1')
runtime.dedup.destroy()
runtime.bridge.destroy()
process.stdout.write(JSON.stringify({ packets, states, snapshots }))
