import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.homeDir, 'Native Session Bridge')
if (process.env.CC_HAHA_NATIVE_SESSION_BRIDGE_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Unowned session bridge fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths(), loopbackFetchOrigin: input.endpoint })
;(globalThis as any).MACRO = { BUILD_TIME: '' }
const { getSessionBridgeConfig, isSessionBridgeAvailable, callSessionBridge } = await import('../../../src/tools/SessionCollaborationTool/bridge.js')
const { authenticateCollaborationCaller, collaborationToolAction } = await import('../../../src/server/sessionCollaborationAuth.js')
const { resolveSessionReferenceContext } = await import('../../../src/server/services/sessionReferenceContext.js')
const keys = ['CC_HAHA_DESKTOP_SERVER_URL', 'CC_HAHA_SESSION_COLLABORATION_TOKEN', 'CC_HAHA_SESSION_ID']
const configurations = []
for (const environment of input.configurations) {
  for (const key of keys) delete process.env[key]
  Object.assign(process.env, environment)
  try { configurations.push({ available: isSessionBridgeAvailable(), config: getSessionBridgeConfig() ?? null }) }
  catch (error: any) { configurations.push({ available: isSessionBridgeAvailable(), error: error.message }) }
}
const auth = input.credentials.map((row: any) => {
  const request = new Request('http://127.0.0.1' + row.path, { method: row.method, headers: row.headers })
  return { action: collaborationToolAction(new URL(request.url).pathname), caller: authenticateCollaborationCaller(request, (id, token) => id === 'caller' && token === 'session-secret') }
})
Object.assign(process.env, { CC_HAHA_DESKTOP_SERVER_URL: input.endpoint, CC_HAHA_SESSION_COLLABORATION_TOKEN: 'fixture-secret', CC_HAHA_SESSION_ID: 'fixture-caller' })
const responses = []
for (const row of input.responses) {
  try { responses.push({ result: await callSessionBridge(row.action, row.input) }) }
  catch (error: any) { responses.push({ error: error.message }) }
}
const references = []
for (const row of input.references ?? []) {
  const seen: string[] = []
  try {
    const content = await resolveSessionReferenceContext(row.content, row.references, async id => { seen.push(id); return !(row.missing ?? []).includes(id) })
    references.push({ content, seen })
  } catch (error: any) { references.push({ error: { code: error.code, message: error.message, status: error.statusCode }, seen }) }
}
process.stdout.write(JSON.stringify({ configurations, auth, responses, references, capabilities }) + '\n')
