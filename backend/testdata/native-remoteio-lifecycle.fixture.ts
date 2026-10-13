import fs from 'node:fs'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from '../../scripts/quality-gate/go-parity/client-options-capabilities'
const home = assertDisposableFixtureHome(process.env.HOME, 'Original RemoteIO input lifecycle')
if (process.env.CC_HAHA_REMOTEIO_FIXTURE !== '1') throw new Error('Unowned RemoteIO lifecycle fixture')
const input = await Bun.stdin.json()
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../preload')
const { StructuredIO } = await import('../../src/cli/StructuredIO')
const { setCommandLifecycleListener, notifyCommandLifecycle } = await import('../../src/utils/commandLifecycle')
const source = fs.readFileSync(new URL('../../src/cli/print.ts', import.meta.url), 'utf8')
const begin = source.indexOf("      const eventId = 'uuid' in message ? message.uuid : undefined")
const end = source.indexOf("      if (message.type === 'control_request')", begin)
if (begin < 0 || end < 0) throw new Error('Original inline lifecycle consumer missing')
const consumeInline = new Function('message', 'notifyCommandLifecycle', new Bun.Transpiler({ loader: 'ts' }).transformSync(source.slice(begin, end)))
const lifecycle: Array<{ id: string; state: string }> = []
setCommandLifecycleListener((id, state) => lifecycle.push({ id, state }))
const stream = new StructuredIO((async function* () { for (const row of input.messages) yield JSON.stringify(row) + '\n' })())
const yielded: any[] = []
for await (const message of stream.structuredInput) {
  consumeInline(message, notifyCommandLifecycle)
  yielded.push(message)
}
setCommandLifecycleListener(null)
if (capabilities.blocked.length) throw new Error('Original lifecycle capability escape: ' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({ yielded, lifecycle, capabilities }))
