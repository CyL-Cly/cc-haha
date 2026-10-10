import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'SDK control')
if (process.env.CC_HAHA_SDK_CONTROL_FIXTURE !== '1') throw new Error('Missing SDK control fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
spyOn(console, 'log').mockImplementation(() => {})
const { ConversationService } = await import('../../../src/server/services/conversationService')
const rows = []
for (const row of input.cases) {
  const service: any = new ConversationService(), sent: any[] = []
  const session: any = { outputCallbacks: [], pendingControlRequests: new Map(), sdkMessages: [], pendingPermissionRequests: new Map(), sdkToken: 'private-fixture', pendingOutbound: [], workDir: input.homeDir }
  const socket = { send: (line: string) => {
    const message = JSON.parse(line); sent.push({ type: message.type, request: message.request })
    if (row.cancel) queueMicrotask(() => controller.abort(new Error('private cancelled')))
    else if (row.close) queueMicrotask(() => service.cancelPendingControlRequests(session))
    else if (!row.noResponse) queueMicrotask(() => service.handleSdkPayload('private', JSON.stringify({ type: 'control_response', response: { ...row.response, request_id: message.request_id } })))
  } }
  if (!row.notReady) session.sdkSocket = socket
  if (!row.missing) service.sessions.set('private', session)
  const controller = new AbortController()
  if (row.aborted) controller.abort(new Error('private cancelled'))
  if (row.lateReady) setTimeout(() => { session.sdkSocket = socket }, 5)
  let value: any = null, error: string | null = null
  try { value = await service.requestControl('private', row.request, row.timeout ?? 200, controller.signal, () => !row.cannotSend) }
  catch (err) { error = (err as Error).message }
  rows.push({ value, error, sent, callbacks: session.outputCallbacks.length, pending: session.pendingControlRequests.size })
}
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ rows, capabilities }), () => resolve()))
process.exit(0)
