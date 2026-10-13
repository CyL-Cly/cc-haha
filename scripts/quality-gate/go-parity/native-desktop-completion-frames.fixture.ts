import fs from 'node:fs'
import path from 'node:path'
import { createRequire } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.home, 'Actual desktop completion frames')
if (process.env.CC_HAHA_COMPLETION_FRAMES_FIXTURE !== '1' || Bun.version !== '1.3.14') throw new Error('Missing completion frame fixture guard')
const sourcePath = path.resolve(import.meta.dir, '../../../src/server/ws/handler.ts')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
const text = fs.readFileSync(sourcePath, 'utf8')
const source = ts.createSourceFile(sourcePath, text, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)
const names = ['getCliPermissionModeBroadcast', 'handleCliPermissionModeBroadcast', 'replayPendingPermissionRequests', 'forwardCliMessageToClient', 'forwardCliMessageToSessionClients']
const functions = source.statements.filter(node => ts.isFunctionDeclaration(node) && node.name && names.includes(node.name.text))
if (functions.length !== names.length) throw new Error('Source forwarding function identity changed')
const syncCases: any[] = []
const permissionSnapshotCalls: any[] = []
const visit = (node: any) => {
  if (ts.isCaseClause(node) && ts.isStringLiteral(node.expression) && node.expression.text === 'sync_state') syncCases.push(node)
  if (ts.isCallExpression(node) && node.expression.getText(source) === 'sendMessage' && node.arguments.length === 2 && ts.isObjectLiteralExpression(node.arguments[1])) {
    const type = node.arguments[1].properties.find((property: any) => ts.isPropertyAssignment(property) && property.name.getText(source) === 'type')
    if (type && ts.isStringLiteral(type.initializer) && type.initializer.text === 'permission_requests_snapshot') permissionSnapshotCalls.push(node)
  }
  ts.forEachChild(node, visit)
}
visit(source)
if (permissionSnapshotCalls.length !== 1) throw new Error('Source reconnect permission snapshot producer changed')
if (syncCases.length !== 1 || (text.match(/type: 'session_state'/g) ?? []).length !== 1) throw new Error('Source snapshot production boundary changed')
const capabilities = await installClientFixtureCapabilities(input.home, { absentPaths: absentManagedSettingsFixturePaths() })
;(globalThis as any).MACRO = { VERSION: '2.1.288', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const { translateCliMessage, handleWebSocket, __markActiveTurnForTests, __settleActiveTurnForTests, __registerPendingUserTurnForTests } = await import('../../../src/server/ws/handler')
const frames: unknown[] = []
const ws = { data: { sessionId: input.sessionId } }
const conversationService = { getPendingPermissionRequests: () => input.pending }
const sendMessage = (_ws: unknown, frame: unknown) => { frames.push(frame) }
const activeSessions = new Map([[input.sessionId, new Set([ws])]])
const body = functions.map(node => node.getText(source)).join('\n')
const original = new Function('translateCliMessage', 'conversationService', 'sendMessage', 'activeSessions', new Bun.Transpiler({ loader: 'ts' }).transformSync(body + '\nreturn { forwardCliMessageToClient, forwardCliMessageToSessionClients, replayPendingPermissionRequests }'))(translateCliMessage, conversationService, sendMessage, activeSessions)
original.forwardCliMessageToClient(input.sessionId, ws, input.result)
const direct = frames.splice(0)
original.forwardCliMessageToSessionClients(input.sessionId, input.result)
const broadcast = frames.splice(0)
// The original sync_state branch supplies a requested authoritative snapshot.
// The forwarding functions above never manufacture one after a normal result.
const syncBody = syncCases[0]!.statements.filter(node => !ts.isBreakStatement(node)).map(node => node.getText(source)).join('\n')
new Function('ws', 'sendMessage', 'hasLiveUserTurnForClient', 'activeBackgroundTaskIds', new Bun.Transpiler({ loader: 'ts' }).transformSync(syncBody))(ws, sendMessage, () => false, new Map())
const requestedSnapshot = frames.splice(0)
// Exercise the actual private active-user-turn map through its existing
// exported test registration/completion seams and the real sync_state handler.
const socket = { data: { sessionId: input.sessionId }, send: (data: string) => frames.push(JSON.parse(data)) }
__markActiveTurnForTests(input.sessionId)
await handleWebSocket.message(socket as any, JSON.stringify({ type: 'sync_state' }))
const registeredSnapshot = frames.splice(0)
__settleActiveTurnForTests(input.sessionId, input.result)
await handleWebSocket.message(socket as any, JSON.stringify({ type: 'sync_state' }))
const settledSnapshot = frames.splice(0)
// Retain the actual CLI-running domain while leaving the user-turn domain empty.
// This is the original SDK mailbox condition, not a foreground registration.
const stateNames = ['trackCliRunState', 'trackCliRunForegroundBoundary', 'resumeCliRunForeground', 'isRootModelActivity', 'clearBackgroundTaskDisconnectCeiling', 'hasLiveUserTurnForClient']
const stateFunctions = source.statements.filter(node => ts.isFunctionDeclaration(node) && node.name && stateNames.includes(node.name.text))
if (stateFunctions.length !== stateNames.length) throw new Error('Source independent SDK/user domain changed')
const { cliParentToolUseId } = await import('../../../src/server/ws/cliMessageParsing')
const activeCliRuns = new Set<string>()
const activeUserTurns = new Map<string, unknown>()
const stateBody = stateFunctions.map(node => node.getText(source)).join('\n') + '\nreturn { trackCliRunState, hasLiveUserTurnForClient }'
const sdkState = new Function('activeCliRuns', 'activeUserTurns', 'cliRunsAwaitingBackgroundTasks', 'backgroundTaskCleanupTimers', 'conversationService', 'cliParentToolUseId', new Bun.Transpiler({ loader: 'ts' }).transformSync(stateBody))(activeCliRuns, activeUserTurns, new Set(), new Map(), { hasSession: () => true }, cliParentToolUseId)
const sdkRunState = sdkState.trackCliRunState(input.sessionId, { type: 'system', subtype: 'session_state_changed', state: 'running' })
new Function('ws', 'sendMessage', 'hasLiveUserTurnForClient', 'activeBackgroundTaskIds', new Bun.Transpiler({ loader: 'ts' }).transformSync(syncBody))(ws, sendMessage, sdkState.hasLiveUserTurnForClient, new Map())
const sdkOnlySnapshot = frames.splice(0)
const toolRequestIds = original.replayPendingPermissionRequests(ws, input.sessionId)
// Execute the exact original reconnect snapshot call with the same original
// foreground predicate. Pending permissions survive an idle foreground.
const permissionSnapshotBody = permissionSnapshotCalls[0]!.getText(source)
new Function('ws', 'sendMessage', 'hasLiveUserTurnForClient', 'toolRequestIds', 'computerUseRequestIds', 'sessionId', new Bun.Transpiler({ loader: 'ts' }).transformSync(permissionSnapshotBody))(ws, sendMessage, sdkState.hasLiveUserTurnForClient, toolRequestIds, [], input.sessionId)
const sdkOnlyPermissionsSnapshot = frames.splice(0)
__registerPendingUserTurnForTests(input.sessionId)
await handleWebSocket.message(socket as any, JSON.stringify({ type: 'sync_state' }))
const pendingStartupSnapshot = frames.splice(0)
__settleActiveTurnForTests(input.sessionId, input.result)
process.stdout.write(JSON.stringify({ direct, broadcast, requestedSnapshot, registeredSnapshot, settledSnapshot, sdkRunState, sdkCliRunning: activeCliRuns.has(input.sessionId), sdkOnlySnapshot, sdkOnlyPermissionsSnapshot, pendingStartupSnapshot, capabilities }))
