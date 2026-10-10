import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual stream recovery projection')
if (process.env.CC_HAHA_STREAM_PROJECTION_FIXTURE !== '1') throw new Error('Missing stream projection fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
spyOn(console, 'log').mockImplementation(() => {})
const { translateCliMessage } = await import('../../../src/server/ws/handler')
const { resetCurrentStreamAttempt } = await import('../../../src/server/ws/streamBlocks')
const frames = input.rows.map((row: any[], index: number) => row.map(message => translateCliMessage(message, `private-stream-${index}`)))
const state: any = {
  streamedAssistantMessageIds: new Set(['old']), unidentifiedStreamScopes: new Set(['old']),
  activeMessageIdsByScope: new Map([['old', 'old']]), activeBlockScopesByIndex: new Map([[0, new Set(['old'])]]),
  activeBlockTypes: new Map([['old', 'text']]), activeToolBlocks: new Map([['old', {}]]),
  pendingToolBlocks: new Map([['old', {}]]), toolParentUseIds: new Map([['old', new Set(['old'])]]),
  pendingLocalCommand: { name: 'private', args: 'retained' }, lastApiError: { message: 'retained error', code: 'API_ERROR' },
}
resetCurrentStreamAttempt(state)
process.stdout.write(JSON.stringify({ frames, state: Object.fromEntries(Object.entries(state).map(([key, value]: any) => [key, value instanceof Map || value instanceof Set ? value.size : value])), capabilities }))
process.exit(0)
