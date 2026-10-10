import { assertDisposableFixtureHome } from './fixture-guard'
import { join } from 'node:path'

const home = process.env.HOME!
assertDisposableFixtureHome(home, 'Connector HTTP Source comparison')
if (process.env.CC_HAHA_CONNECTOR_HTTP_FIXTURE !== '1') throw new Error('Missing isolated connector HTTP marker')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const { ConnectorService } = await import('../../../src/server/services/connectorService')
const { createConnectorsApi } = await import('../../../src/server/api/connectors')
const fail = () => { throw new Error('HTTP guard fixture unexpectedly started a native action') }
const service = new ConnectorService({ root: join(home, 'source-connectors'), platform: 'darwin-arm64', definitions: [input.definition], createAdapter: fail,
  bridge: { installConnectorPlugin: fail, setConnectorPluginEnabled: fail, removeConnectorPlugin: fail, isConnectorPluginReady: fail, reloadConnectorSessions: fail } })
const api = createConnectorsApi(async () => service)
const output = []
for (const item of input.requests) {
  const url = new URL(item.path, 'http://127.0.0.1')
  const request = new Request(url, { method: item.method, ...(item.body !== null ? { body: item.body } : {}) })
  const response = await api(request, url, url.pathname.split('/').filter(Boolean))
  output.push({ status: response.status, body: await response.json(), contentType: response.headers.get('content-type') })
}
process.stdout.write(JSON.stringify(output))
