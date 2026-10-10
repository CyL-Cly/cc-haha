import { assertDisposableFixtureHome } from './fixture-guard'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'

const home = process.env.HOME!
assertDisposableFixtureHome(home, 'Native connector contract')
if (process.env.CC_HAHA_CONNECTOR_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') {
  throw new Error('Connector contract requires the isolated production fixture')
}

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const clock = Date.parse(input.clock)
const OriginalDate = Date
globalThis.Date = class extends OriginalDate {
  constructor(value?: string | number) { super(value === undefined ? clock : value) }
  static now() { return clock }
} as DateConstructor

// These are the original production implementations. Only adapter/bridge I/O
// and the clock are controlled; no model, provider, settings or user graph is
// loaded. Native downloads and processes have separate actual-I/O tests.
const { ConnectorsPersistence } = await import('../../../src/server/services/connectorsPersistence')
const { ConnectorService } = await import('../../../src/server/services/connectorService')
const { parseConnectorCheck, trustedAuthorizationUrl } = await import('../../../src/services/connectors/cliAdapter')
const { ALL_CONNECTORS } = await import('../../../src/services/connectors/catalog')
const { REMOTE_RECIPES } = await import('../../../src/services/connectors/remoteCatalog')
const { SKILL_RECIPES } = await import('../../../src/services/connectors/skillCatalog')

const persistence = input.persistence.map((item: any, index: number) => {
  const root = join(home, 'persistence', String(index))
  mkdirSync(root, { recursive: true })
  if (item.source !== null) writeFileSync(join(root, 'state.json'), item.source)
  try {
    const store = new ConnectorsPersistence(root)
    const before = store.get('sample')
    if (item.patch) store.set('sample', item.patch)
    return { before, after: store.get('sample'), ...(item.patch ? { disk: readFileSync(store.path, 'utf8') } : {}) }
  } catch (error) { return { error: (error as Error).message } }
})

async function settled(service: InstanceType<typeof ConnectorService>) {
  for (let i = 0; i < 5000 && service.get('sample').operation; i++) await Bun.sleep(1)
  if (service.get('sample').operation) throw new Error('Source connector action did not settle')
}
const services = []
for (const [index, scenario] of input.services.entries()) {
  const root = join(home, 'services', String(index))
  mkdirSync(root, { recursive: true })
  if (scenario.source) writeFileSync(join(root, 'state.json'), scenario.source)
  const def = { ...scenario.definition }
  const calls: unknown[][] = []
  let behavior: Record<string, unknown> = {}
  let authenticated = def.transport === 'skills'
  const step = (name: string, extra: unknown[] = []) => {
    calls.push([name, ...extra])
    if (behavior.fail === name) throw new Error('offline-secret-must-never-be-exposed')
  }
  const service = new ConnectorService({ root, platform: 'darwin-arm64', definitions: [def],
    createAdapter: selected => {
      step('factory', [selected.version])
      return {
        async configure(config) { step('configure', [config]) },
        async prepare(_signal, progress) { step('prepare'); progress('downloading'); return { directory: '/owned/connector', command: 'owned-cli', args: [], env: {} } },
        async check() { step('check'); return { authenticated, verification: behavior.verification ?? (def.transport === 'skills' ? 'local' : 'remote'), ...(authenticated ? { accountLabel: 'Account' } : {}) } as any },
        async authenticate(_installation, _signal, progress) { step('authenticate'); progress('awaiting-authorization', 'https://accounts.feishu.cn/login'); authenticated = true },
        async deactivate() { step('deactivate') },
        async remove() { step('remove') },
      }
    },
    bridge: {
      async installConnectorPlugin(selected) { step('install', [selected.version]) },
      async setConnectorPluginEnabled(_selected, enabled) { step('enabled', [enabled]) },
      async removeConnectorPlugin() { step('remove-plugin') },
      async reloadConnectorSessions(id, required) { step('reload', [id ?? null, required?.version ?? null]) },
      async isConnectorPluginReady() { step('ready'); return behavior.ready !== false },
    },
  })
  const states = []
  for (const action of scenario.actions) {
    behavior = action.behavior ?? {}
    if (typeof behavior.authenticated === 'boolean') authenticated = behavior.authenticated
    if (action.version) def.version = action.version
    try {
      service.action('sample', action.action, action.options ?? {})
      await settled(service)
      states.push({ state: service.get('sample') })
    } catch (error) {
      states.push({ error: (error as Error).message, status: (error as any).statusCode ?? null })
    }
  }
  services.push({ calls, states, disk: readFileSync(join(root, 'state.json'), 'utf8') })
}
const checks = input.checks.map((item: any) => {
  try { return { check: parseConnectorCheck(item.id, item.result) } }
  catch (error) { return { error: (error as Error).message } }
})
const urls = input.urls.map((item: any) => trustedAuthorizationUrl(item.id, item.value) ?? null)
process.stdout.write(JSON.stringify({ persistence, services, checks, urls, catalog: { definitions: ALL_CONNECTORS, remote: REMOTE_RECIPES, skills: SKILL_RECIPES } }))
