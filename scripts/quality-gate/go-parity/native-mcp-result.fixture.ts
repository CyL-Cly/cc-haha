import { assertDisposableFixtureHome } from './fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'Native MCP results')
if (process.env.CC_HAHA_MCP_RESULT_FIXTURE !== '1') throw new Error('Missing MCP result fixture marker')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
await import('../../../preload')
const { transformMCPResult } = await import('../../../src/services/mcp/client')
const observations = []
for (const row of input.cases) {
  try { const result = await transformMCPResult(row, 'contract-tool', 'contract-server'); observations.push({ content: result.content, error: null }) }
  catch (error) { observations.push({ content: null, error: (error as Error).message }) }
}
process.stdout.write(JSON.stringify(observations))
