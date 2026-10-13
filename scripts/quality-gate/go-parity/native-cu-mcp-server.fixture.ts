import { assertDisposableFixtureHome } from './fixture-guard'

assertDisposableFixtureHome(process.env.HOME, 'Computer Use MCP stdio contract')
if (process.env.CC_HAHA_CU_MCP_FIXTURE !== '1') throw new Error('Unowned Computer Use MCP fixture')
const input = await Bun.stdin.json()
const { createComputerUseMcpServer, buildPlatformComputerUseTools } = await import('../../../src/vendor/computer-use-mcp/mcpServer')
const { InMemoryTransport } = await import('@modelcontextprotocol/sdk/inMemory.js')
const definitions = Object.fromEntries(['pixels', 'normalized_0_100'].map(mode => [mode, buildPlatformComputerUseTools({ platform: 'win32', screenshotFiltering: 'none' }, mode as any)]))
const [client, peer] = InMemoryTransport.createLinkedPair()
const server = createComputerUseMcpServer({ serverName: 'computer-use', logger: { debug() {}, warn() {}, error() {}, info() {}, silly() {} }, executor: { capabilities: { platform: input.platform, screenshotFiltering: input.platform === 'win32' ? 'none' : 'native' } }, isDisabled: () => input.disabled === true } as any, 'pixels')
const responses: unknown[] = []
let resolveDone: () => void
const done = new Promise<void>(resolve => { resolveDone = resolve })
client.onmessage = message => {
  responses.push(message)
  if (responses.length === input.requests.length) resolveDone()
}
await server.connect(peer)
await client.start()
for (const request of input.requests) await client.send(request)
const timer = setTimeout(() => { throw new Error('actual Source MCP response timeout') }, 5000)
await done
clearTimeout(timer)
await server.close()
await client.close()
process.stdout.write(JSON.stringify({ responses, definitions }))
