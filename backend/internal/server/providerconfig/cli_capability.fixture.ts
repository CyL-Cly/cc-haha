import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
const input = await Bun.stdin.json()
assertDisposableFixtureHome(input.home, 'CLI provider capabilities actual Source')
if (process.env.CC_HAHA_CLI_PROVIDER_CAPABILITY_FIXTURE !== '1') throw new Error('Unowned capability fixture')
const { getClaudeCodeModelCapabilities } = await import('../../../../src/shared/modelReasoning')
process.stdout.write(JSON.stringify(input.cases.map((item: any) => getClaudeCodeModelCapabilities(item.model, item.format, undefined, item.kind))) + '\n')
