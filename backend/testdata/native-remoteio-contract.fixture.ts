import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from '../../scripts/quality-gate/go-parity/client-options-capabilities'
assertDisposableFixtureHome(process.env.HOME, 'Actual RemoteIO transport contracts')
if (process.env.CC_HAHA_REMOTEIO_FIXTURE !== '1') throw new Error('Unowned RemoteIO fixture')
const input = await Bun.stdin.json()
const capabilities = await installClientFixtureCapabilities(process.env.HOME!, { absentPaths: absentManagedSettingsFixturePaths() })
;(globalThis as any).MACRO = { VERSION: 'source-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../preload')
const { getTransportForUrl } = await import('../../src/cli/transports/transportUtils')
const { parseSSEFrames } = await import('../../src/cli/transports/SSETransport')
const { CCRClient, createStreamAccumulator, accumulateStreamEvents, clearStreamAccumulatorForMessage } = await import('../../src/cli/transports/ccrClient')
const selections = []
for (const row of input.selections ?? []) {
  for (const key of ['CLAUDE_CODE_USE_CCR_V2', 'CLAUDE_CODE_POST_FOR_SESSION_INGRESS_V2']) delete process.env[key]
  Object.assign(process.env, row.env)
  try {
    const url = new URL(row.url)
    const transport: any = getTransportForUrl(url, {}, 'source-session')
    let session
    if (process.env.CLAUDE_CODE_USE_CCR_V2 === '1') {
      const client: any = new CCRClient(transport, url)
      session = client.sessionBaseUrl
      client.close()
    }
    selections.push({ kind: transport.constructor.name, stream: transport.url.href, ...(transport.postUrl && { events: transport.postUrl }), ...(session && { session }) })
    transport.close()
  } catch (error) { selections.push({ error: (error as Error).message }) }
}
const frames = []
for (const buffer of input.frames ?? []) {
  try { frames.push(parseSSEFrames(buffer)) } catch (error) { frames.push({ error: (error as Error).message }) }
}
const stream = []
const state = createStreamAccumulator()
for (const turn of input.stream ?? []) {
  if (turn.clear) clearStreamAccumulatorForMessage(state, turn.clear)
  stream.push(accumulateStreamEvents(turn.messages ?? [], state))
}
if (capabilities.blocked.length) throw new Error(`Unexpected outside access: ${capabilities.blocked.join(', ')}`)
const streamUnits = stream.map(rows => rows.map(row => typeof row.event?.delta?.text === 'string' ? row.event.delta.text.split('').map(unit => unit.charCodeAt(0)) : null))
process.stdout.write(JSON.stringify({ selections, frames, stream, streamUnits, capabilities }))
