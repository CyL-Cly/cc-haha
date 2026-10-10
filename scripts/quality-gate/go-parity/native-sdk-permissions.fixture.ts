import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Dedicated SDK permissions')
if (process.env.CC_HAHA_SDK_PERMISSIONS_FIXTURE !== '1') throw new Error('Missing dedicated SDK fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
spyOn(console, 'log').mockImplementation(() => {})
const { ConversationService } = await import('../../../src/server/services/conversationService')
const { translateCliMessage } = await import('../../../src/server/ws/handler')
const rows = []
for (const row of input.cases) {
 const service: any = new ConversationService()
 const sent: any[] = [], forwarded: any[] = []
 const session: any = { sdkMessages: [], pendingPermissionRequests: new Map(), outputCallbacks: [(value: any) => forwarded.push(value)], sdkToken: 'private-fixture-token', sdkSocket: { send: (line: string) => sent.push(JSON.parse(line)) }, pendingOutbound: [], workDir: input.homeDir }
 service.sessions.set('fixture', session)
 for (const payload of row.payloads) service.handleSdkPayload('fixture', JSON.stringify(payload), { canAcceptPermissionRequest: () => !row.stopped })
 const pending = service.getPendingPermissionRequests('fixture')
 const frames = forwarded.flatMap(message => translateCliMessage(message, 'fixture'))
 if (row.response) {
  const r = row.response
  service.respondToPermission('fixture', r.requestId, r.allowed, r.rule, r.updatedInput, r.denyMessage, r.permissionUpdates)
 }
 rows.push({ pending, sent, after: service.getPendingPermissionRequests('fixture'), forwarded, frames })
}
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ rows, capabilities }), () => resolve()))
process.exit(0)
