import { assertDisposableFixtureHome } from './fixture-guard'
import { isAbsolute, relative, resolve, sep } from 'node:path'

const home = assertDisposableFixtureHome(process.env.HOME, 'Native Remote Adapter')
if (process.env.CC_HAHA_NATIVE_REMOTE_ADAPTER_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Remote Adapter requires isolated production fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const root = resolve(input.root)
const within = relative(home, root)
if (!isAbsolute(input.root) || within === '..' || within.startsWith(`..${sep}`) || isAbsolute(within)) throw new Error('Unowned remote adapter root')
for (const [key, value] of Object.entries(input.environment)) process.env[key] = String(value)
const { createRemoteConnectorAdapter } = await import('../../../src/services/connectors/remoteAdapter')
const { createRemoteConnectorBridge, saveRemoteApiKey } = await import('../../../src/services/connectors/remoteConnector')
const slots: Record<string, Record<string, unknown>> = structuredClone(input.options)
const calls: unknown[] = []
let behavior = { status: 'connected', toolCount: 2, authUrl: 'https://accounts.example/authorize?state=fixture', fail: '' }
const deps = {
  async probe(name: string, config: unknown, signal: AbortSignal) {
    calls.push(['probe', name, config])
    signal.throwIfAborted()
    if (behavior.fail === 'probe') throw new Error('sensitive-detail-must-be-hidden')
    return { status: behavior.status, toolCount: behavior.toolCount }
  },
  async oauth(name: string, config: unknown, onUrl: (url: string) => void, signal: AbortSignal) {
    calls.push(['oauth', name, config])
    signal.throwIfAborted()
    if (behavior.fail === 'oauth') throw new Error('sensitive-detail-must-be-hidden')
    onUrl(behavior.authUrl)
  },
  async disconnect(name: string, config: unknown) {
    calls.push(['disconnect', name, config])
    if (behavior.fail === 'disconnect') throw new Error('sensitive-detail-must-be-hidden')
  },
  loadOptions: (id: string) => slots[id] ?? {},
  saveOptions(id: string, values: Record<string, unknown>, schema: unknown) { calls.push(['save', id, values, schema]); slots[id] = values },
  deleteOptions(id: string) { calls.push(['delete', id]); delete slots[id] },
  clearOAuth(name: string, config: unknown) { calls.push(['clearOAuth', name, config]) },
}
const bridge = createRemoteConnectorBridge(input.recipe, deps)
const adapter = createRemoteConnectorAdapter(input.definition, root, input.recipe, {
  createBridge: recipe => createRemoteConnectorBridge(recipe, deps),
  saveApiKey: (recipe, key) => saveRemoteApiKey(recipe, key, deps),
})
let installation = input.installation
const observations = []
for (const op of input.operations) {
  behavior = { ...behavior, ...op.behavior }
  const phases: unknown[] = []
  const progress = (phase: string, url?: string) => phases.push(url === undefined ? [phase] : [phase, url])
  const controller = new AbortController()
  if (op.cancelled) controller.abort()
  try {
    let value
    if (op.action === 'configure') await adapter.configure!(op.configuration)
    else if (op.action === 'prepare') { installation = await adapter.prepare(controller.signal, progress); value = installation }
    else if (op.action === 'check') value = await adapter.check(op.installation ?? installation, controller.signal)
    else if (op.action === 'authenticate') await adapter.authenticate(op.installation ?? installation, controller.signal, progress)
    else if (op.action === 'deactivate') await adapter.deactivate()
    else if (op.action === 'remove') await adapter.remove(op.installation ?? installation)
    else if (op.action === 'bridge-check') value = await bridge.check(controller.signal)
    else if (op.action === 'bridge-authenticate') await bridge.authenticate(controller.signal, progress)
    else if (op.action === 'config') value = { configured: bridge.isConfigured() }
    else throw new Error('Undeclared remote adapter operation')
    observations.push({ value: value ?? null, error: null, phases })
  } catch (error) {
    observations.push({ value: null, error: error instanceof Error ? error.message : String(error), phases })
  }
}
process.stdout.write(JSON.stringify({ observations, calls, options: slots }))
