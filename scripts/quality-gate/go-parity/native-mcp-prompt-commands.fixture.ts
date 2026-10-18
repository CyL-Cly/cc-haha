import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

assertDisposableFixtureHome(process.env.HOME, 'Native MCP prompt commands')
if (process.env.CC_HAHA_MCP_PROMPT_COMMANDS_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') {
  throw new Error('MCP prompt commands fixture requires isolated production configuration')
}
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
await installClientFixtureCapabilities(process.env.HOME!, { loopbackFetchOrigin: new URL(input.url).origin, absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setCwdState(input.cwd)
state.setOriginalCwd(input.cwd)
state.setIsInteractive(false)
const { connectToServer, clearServerCache, fetchCommandsForClient, getMcpClientCacheKey } = await import('../../../src/services/mcp/client')
const config = { type: 'http' as const, url: input.url, scope: 'user' as const }
const peer = await connectToServer(input.name, config)
if (peer.type !== 'connected') throw new Error(`Original MCP connection failed: ${peer.type}`)
const commands = await fetchCommandsForClient(peer)
const cached = await fetchCommandsForClient(peer)
const metadata = commands.map(command => ({ ...command, userFacingName: command.userFacingName() }))
const values = []
for (const args of input.args) {
  values.push(await commands[0]!.getPromptForCommand(args, {} as any))
}
fetchCommandsForClient.cache.delete(getMcpClientCacheKey(peer))
const refreshed = await fetchCommandsForClient(peer)
await clearServerCache(input.name, config)
process.stdout.write(JSON.stringify({ metadata, cachedCount: cached.length, refreshedCount: refreshed.length, values }))
