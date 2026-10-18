import { readFileSync } from 'node:fs'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const home = assertDisposableFixtureHome(process.env.HOME, 'Native MCP timeouts')
if (process.env.CC_HAHA_MCP_TIMEOUTS_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Unowned MCP timeout fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const assign = (name: string, value: string | null) => { if (value === null) delete process.env[name]; else process.env[name] = value }
if (input.mode === 'getters') {
  // Execute the actual private declarations unchanged. Only lexical binding
  // of their shipped default and process.env is supplied by this fixture.
  const source = readFileSync(new URL('../../../src/services/mcp/client.ts', import.meta.url), 'utf8')
  const declaration = (name: string) => {
    const start = source.indexOf(`function ${name}(): number {`)
    const end = source.indexOf('\n}', start) + 2
    if (start < 0 || end < 2) throw new Error(`Missing original ${name}`)
    return source.slice(start, end)
  }
  const body = new Bun.Transpiler({ loader: 'ts' }).transformSync(declaration('getConnectionTimeoutMs') + '\n' + declaration('getMcpToolTimeoutMs'))
  const getters = new Function('process', 'DEFAULT_MCP_TOOL_TIMEOUT_MS', body + '\nreturn {getConnectionTimeoutMs,getMcpToolTimeoutMs}') (process, 100_000_000)
  const rows = input.values.map((value: string | null) => {
    assign('MCP_TIMEOUT', value)
    assign('MCP_TOOL_TIMEOUT', value)
    const connection = getters.getConnectionTimeoutMs()
    const tool = getters.getMcpToolTimeoutMs()
    return { connection, tool, connectionText: String(connection), toolSecondsText: String(Math.floor(tool / 1000)) }
  })
  process.stdout.write(JSON.stringify({ rows }))
  process.exit(0)
}
await installClientFixtureCapabilities(home, { loopbackFetchOrigin: new URL(input.url).origin, absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home)
state.setOriginalCwd(home)
state.setIsInteractive(false)
const { enableConfigs } = await import('../../../src/utils/config')
enableConfigs()
const { connectToServer, clearServerCache, fetchToolsForClient } = await import('../../../src/services/mcp/client')
assign('MCP_TIMEOUT', input.connection ?? null)
const config = { type: 'http' as const, url: input.url, scope: 'user' as const }
const begin = performance.now()
const reload = input.connectionReload ? setTimeout(() => assign('MCP_TIMEOUT', input.connectionReload.value), input.connectionReload.after) : undefined
const peer = await connectToServer('timeout-peer', config)
if (reload) clearTimeout(reload)
if (peer.type !== 'connected') {
  process.stdout.write(JSON.stringify({ connection: peer.type, error: 'error' in peer ? peer.error : null, elapsed: performance.now() - begin }))
  process.exit(0)
}
if (input.mode === 'disabled-completion') {
  const { setMcpServerEnabled, isMcpServerDisabled } = await import('../../../src/services/mcp/config')
  const { discardConnectionAttemptIfDisabled } = await import('../../../src/services/mcp/useManageMCPConnections')
  const pending = fetchToolsForClient(peer)
  // This handshake waits for the physical tools/list handler; no timing delay
  // substitutes for the actual incomplete startup work.
  await fetch(new URL('/control/list-started', input.url))
  setMcpServerEnabled(peer.name, false)
  await clearServerCache(peer.name, config)
  const tools = await pending
  let update: { type: string } | undefined
  const discarded = discardConnectionAttemptIfDisabled(peer, isMcpServerDisabled(peer.name), value => { update = value })
  process.stdout.write(JSON.stringify({ connection: peer.type, discarded, status: update?.type, tools: tools.length }))
  process.exit(0)
}
const tools = await fetchToolsForClient(peer)
const rows = []
for (const row of input.calls) {
  assign('MCP_TOOL_TIMEOUT', row.timeout)
  const abortController = new AbortController()
  const cancel = row.cancelAfter ? setTimeout(() => abortController.abort(), row.cancelAfter) : undefined
  const reload = row.reload ? setTimeout(() => assign('MCP_TOOL_TIMEOUT', row.reload.value), row.reload.after) : undefined
  const begin = performance.now()
  try {
    const result = await tools[0]!.call({ mode: row.mode }, { abortController, setAppState: (fn: any) => fn({ mcp: { clients: [peer], tools } }) } as any, () => {}, { message: { content: [] } } as any)
    rows.push({ result, elapsed: performance.now() - begin })
  } catch (error: any) {
    rows.push({ error: { name: error.name, message: error.message, code: error.code ?? null }, elapsed: performance.now() - begin })
  } finally { if (cancel) clearTimeout(cancel); if (reload) clearTimeout(reload) }
}
if (input.expectedCancellations) {
  // The Source wrapper returns before the SDK notification POST settles. Join
  // its physical receipt before fixture cache teardown/process exit.
  await fetch(new URL(`/control/cancel-count?count=${input.expectedCancellations}`, input.url))
}
await clearServerCache('timeout-peer', config)
process.stdout.write(JSON.stringify({ connection: peer.type, rows }))
