import { assertDisposableFixtureHome } from './fixture-guard'
import { mkdirSync, writeFileSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
assertDisposableFixtureHome(process.env.HOME, 'Native shared IM runtime')
if (process.env.CC_HAHA_IM_CHAT_RUNTIME_FIXTURE !== '1') throw new Error('Unowned chat fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
console.log = console.error = console.warn = () => {}
Date.now = () => input.now
mkdirSync(process.env.CLAUDE_CONFIG_DIR!, { recursive: true })
writeFileSync(join(process.env.CLAUDE_CONFIG_DIR!, 'adapters.json'), JSON.stringify(input.config))
const { ImChatRuntime } = await import('../../../adapters/common/chat-runtime')
const { SessionStore } = await import('../../../adapters/common/session-store')
const { MessageDedup } = await import('../../../adapters/common/message-dedup')
const store = new SessionStore()
const dedup = new MessageDedup()
const notices: any[] = [], streams: any[] = [], packets: any[] = [], http: any[] = [], clears: any[] = [], busy: any[] = []
const states = []
const bridgeSessions = new Map<string, string>(), handlers = new Map()
let nextID = 0, responseID = 0, downloads = 0
const bridge: any = {
  getSessionId: (id: string) => bridgeSessions.get(id),
  hasSession: (id: string) => bridgeSessions.has(id),
  isSessionOpen: (id: string) => bridgeSessions.has(id),
  resetSession: (id: string) => { bridgeSessions.delete(id) },
  connectSession: (id: string, sid: string) => { bridgeSessions.set(id, sid) },
  onServerMessage: (id: string, fn: any) => { handlers.set(id, fn) },
  waitForOpen: async () => true,
  sendUserMessage: (id: string, text: string, attachments?: any) => { packets.push({ id: bridgeSessions.get(id), message: { type: 'user_message', content: text, ...(attachments ? { attachments } : {}) } }); return bridgeSessions.has(id) },
  sendPermissionResponse: (id: string, requestId: string, allowed: boolean, rule?: string) => { packets.push({ id: bridgeSessions.get(id), message: { type: 'permission_response', requestId, allowed, ...(rule ? { rule } : {}) } }); return bridgeSessions.has(id) },
  sendStopGeneration: (id: string) => { packets.push({ id: bridgeSessions.get(id), message: { type: 'stop_generation' } }); return bridgeSessions.has(id) },
}
const projects = [{ projectName: 'repo', realPath: input.work, projectPath: input.work, branch: 'main' }]
const client: any = {
  createSession: async (work: string) => { http.push({ method: 'create', work }); return `session-${++nextID}` },
  sessionExists: async (id: string) => { http.push({ method: 'exists', id }); return true },
  listRecentProjects: async () => { http.push({ method: 'projects' }); return projects },
  matchProject: async (q: string) => { http.push({ method: 'projects' }); return q === '1' || q === 'repo' ? { project: projects[0] } : {} },
  getGitInfo: async (id: string) => { http.push({ method: 'git', id }); return { repoName: 'repo', branch: 'main', workDir: input.work } },
  getTasksForSession: async (id: string) => { http.push({ method: 'tasks', id }); return [{status:'pending'}, {status:'in_progress'}, {status:'completed'}] },
}
const runtime = new ImChatRuntime({ port: { platform: 'slack', logPrefix: '[Slack]', sendNotice: async (id: string, text: string) => { notices.push({id,text}) }, setBusy: (id: string, value: boolean) => { busy.push({id,value}) }, clearChat: (id: string) => { clears.push(id) }, createResponse: (id: string) => { const response = ++responseID; return { append: async (text: string) => { streams.push({id,response,append:text}) }, finish: async () => { streams.push({id,response,finish:true}) } } } }, config: input.config, platformConfig: input.config.slack, bridge, sessionStore: store, httpClient: client, defaultWorkDir: input.work, dedup, flushCharThreshold: 1, flushIntervalMs: 60_000 })
for (const action of input.actions) {
  if (action.kind === 'inbound') await runtime.handleInbound({chatId: action.chat ?? 'D1', userId: action.user ?? 'U1', displayName: 'Private User', dedupKey: action.key, text: action.text, hasAttachments: action.media ?? false, loadAttachments: async () => { downloads++; return action.refs ?? [] } })
  if (action.kind === 'server') await runtime.handleServerMessage(action.chat ?? 'D1', action.message)
  if (action.kind === 'clear') runtime.clearTransientChatState(action.chat ?? 'D1')
  await new Promise(resolve => setImmediate(resolve))
  states.push({...runtime.getRuntimeState(action.chat ?? 'D1')})
}
dedup.destroy()
process.stdout.write(JSON.stringify({notices,streams,packets,http,clears,busy,downloads,states,bindings: JSON.parse(readFileSync(join(process.env.CLAUDE_CONFIG_DIR!,'adapter-sessions.json'),'utf8'))}))
