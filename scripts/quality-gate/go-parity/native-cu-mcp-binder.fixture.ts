import { assertDisposableFixtureHome } from './fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'Computer Use MCP binder Source comparison')
if (process.env.CC_HAHA_CU_BINDER_FIXTURE !== '1') throw new Error('Missing binder fixture marker')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const { bindSessionContext, buildPlatformComputerUseTools } = await import('../../../src/vendor/computer-use-mcp/mcpServer')
const observations = []
let disabled = false
const adapter = {
  serverName: 'computer-use',
  logger: { debug() {}, warn() {}, error() {}, info() {} },
  isDisabled: () => disabled,
  executor: { capabilities: { platform: 'darwin', screenshotFiltering: 'native' } },
}
const dispatch = bindSessionContext(adapter as any, 'logical', {
  getAllowedApps: () => [], getGrantFlags: () => ({}),
} as any)
for (const row of input.cases) {
  disabled = row.disabled === true
  observations.push(await dispatch(row.name, row.args))
}
process.stdout.write(JSON.stringify({ observations, tools: buildPlatformComputerUseTools({ platform: 'darwin', screenshotFiltering: 'native' }, 'logical') }))
