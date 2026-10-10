import { mock } from 'bun:test'
import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'

assertDisposableFixtureHome(process.env.HOME, 'Wechat runtime Source')
if (process.env.CC_HAHA_WECHAT_RUNTIME_FIXTURE !== '1') throw new Error('Unowned Wechat runtime fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
console.log = console.warn = console.error = () => {}
Date.now = () => input.now
mkdirSync(process.env.CLAUDE_CONFIG_DIR!, { recursive: true })
writeFileSync(join(process.env.CLAUDE_CONFIG_DIR!, 'adapters.json'), JSON.stringify(input.config))
const notices: any[] = [], packets: any[] = [], states: any[] = [], http: any[] = []
const sessions = new Map<string, string>()
mock.module('../../adapters/common/ws-bridge', () => ({ WsBridge: class {
  getSessionId(id: string) { return sessions.get(id) }
  hasSession(id: string) { return sessions.has(id) }
  isSessionOpen(id: string) { return sessions.has(id) }
  resetSession(id: string) { sessions.delete(id) }
  connectSession(id: string, sid: string) { sessions.set(id, sid) }
  onServerMessage() {}
  async waitForOpen() { return true }
  sendUserMessage(id: string, text: string, attachments?: any) {
    packets.push({ id: sessions.get(id), message: { type: 'user_message', content: text, ...(attachments ? { attachments } : {}) } })
    return sessions.has(id)
  }
  sendPermissionResponse(id: string, requestId: string, allowed: boolean, rule?: string) {
    packets.push({ id: sessions.get(id), message: { type: 'permission_response', requestId, allowed, ...(rule ? { rule } : {}) } })
    return sessions.has(id)
  }
  sendStopGeneration(id: string) { packets.push({ id: sessions.get(id), message: { type: 'stop_generation' } }); return sessions.has(id) }
  destroy() {}
} }))
mock.module('../../adapters/common/adapter-client', () => ({ createAdapterClient: () => ({ defaultWorkDir: input.work, httpClient: {
  sessionExists: async (id: string) => { http.push({ method: 'exists', id }); return true },
  createSession: async (work: string) => { http.push({ method: 'create', work }); return 'created-session' },
  listRecentProjects: async () => { http.push({ method: 'projects' }); return [] },
  getGitInfo: async (id: string) => { http.push({ method: 'git', id }); return { repoName: 'repo', workDir: input.work, branch: 'main' } },
  getTasksForSession: async (id: string) => { http.push({ method: 'tasks', id }); return [] },
} }) }))
const actualProtocol = await import('../../adapters/wechat/protocol')
mock.module('../../adapters/wechat/protocol', () => ({ ...actualProtocol,
  sendWechatText: async ({ to, text }: any) => { notices.push({ id: to, text }) },
  getWechatConfig: async () => ({ ret: 0 }),
  sendWechatTyping: async () => {},
}))
const runtime = await import('../../adapters/wechat/index')
const { enqueue } = await import('../../adapters/common/chat-queue')
runtime.sessionStore.set('U1', 'session-1', input.work)
runtime.bridge.connectSession('U1', 'session-1')
for (const action of input.actions) {
  if (action.kind === 'server') await runtime.handleServerMessage('U1', action.message)
  if (action.kind === 'inbound') {
    await runtime.routeUserMessage({ from_user_id: action.user ?? 'U1', message_id: action.key, item_list: [{ type: 1, text_item: { text: action.text } }] })
    await enqueue(action.user ?? 'U1', async () => {})
  }
  if (action.kind === 'clear') runtime.clearTransientChatState('U1')
  states.push({ ...runtime.getRuntimeState('U1') })
}
runtime.typingController.destroy()
runtime.dedup.destroy()
runtime.bridge.destroy()
process.stdout.write(JSON.stringify({ notices, packets, states, http }))
