import { dirname, join } from 'node:path'
import { mkdir, writeFile } from 'node:fs/promises'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'Session inspection transcript')
if (process.env.CC_HAHA_SESSION_INSPECTION_FIXTURE !== '1') throw new Error('Unowned session inspection fixture')
const absentPaths = absentManagedSettingsFixturePaths()
absentPaths.push(join(dirname(process.execPath), 'rg'), '/usr/bin/rg', '/bin/rg')
for (let path = dirname(home); ; path = dirname(path)) {
  for (const name of ['.git', '.claude/skills', '.claude/commands', '.agents/skills', '.agents/commands']) absentPaths.push(join(path, name))
  if (dirname(path) === path) break
}
const capabilities = await installClientFixtureCapabilities(home, { absentPaths })
await mkdir(join(home, '.claude'), { recursive: true })
await writeFile(join(home, '.claude', 'settings.json'), JSON.stringify(input.settings ?? {}))
await import('../../../preload')
process.chdir(home)
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home)
state.setOriginalCwd(home)
state.setProjectRoot(home)
state.setIsInteractive(false)
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = join(home, 'policy')
const settings = await import('../../../src/utils/settings/settings')
settings.getInitialSettings()
process.env.USER_TYPE = 'external'
const config = await import('../../../src/utils/config')
config.enableConfigs()
for (const [key, value] of Object.entries(input.environment)) process.env[key] = String(value)
const { SessionService } = await import('../../../src/server/services/sessionService')
const service = new SessionService({ getMode: () => 'off' } as any)
const project = join(home, '.claude', 'projects', 'private')
await mkdir(project, { recursive: true })
console.log = () => {}
const results = []
for (const row of input.cases) {
  const path = join(project, row.id + '.jsonl')
  await writeFile(path, row.content.replaceAll('$HOME', home))
  try {
    results.push({ name: row.name, snapshot: await service.getInspectionTranscriptSnapshot(row.id) })
  } catch (error) {
    const e = error as any
    results.push({ name: row.name, error: { status: e.statusCode, code: e.code, message: e.message } })
  }
}
const httpResults = []
if (input.httpCases) {
  const { handleSessionsApi } = await import('../../../src/server/api/sessions')
  const { conversationService } = await import('../../../src/server/services/conversationService')
  for (const row of input.httpCases) {
    const sent: any[] = []
    const session: any = {
      outputCallbacks: [], pendingControlRequests: new Map(), sdkMessages: [],
      pendingPermissionRequests: new Map(), sdkToken: 'private-fixture', pendingOutbound: [],
      workDir: home, permissionMode: 'default', initMessage: null,
    }
    const service: any = conversationService
    session.sdkSocket = { send: (line: string) => {
      const message = JSON.parse(line)
      sent.push(message.request)
      const output = row.responses[message.request.subtype]
      queueMicrotask(() => service.handleSdkPayload(row.id, JSON.stringify({
        type: 'control_response', response: {
          request_id: message.request_id,
          ...(output.error ? { subtype: 'error', error: output.error } : { subtype: 'success', response: output.value }),
        },
      })))
    } }
    if (row.active) {
      service.sessions.set(row.id, session)
      if (row.init) service.handleSdkPayload(row.id, JSON.stringify(row.init).replaceAll('$HOME', home))
    }
    const segments = ['api', 'sessions', row.id, 'inspection']
    const url = new URL('http://127.0.0.1/' + segments.join('/') + (row.query ?? ''))
    const response = await handleSessionsApi(new Request(url, { method: row.method ?? 'GET' }), url, segments)
    httpResults.push({ status: response.status, body: await response.json(), sent })
    service.sessions.delete(row.id)
  }
}
if (capabilities.blocked.length) throw new Error('Session inspection accessed blocked capability: ' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({ results, httpResults, capabilities }).replaceAll(home, '$HOME'))
