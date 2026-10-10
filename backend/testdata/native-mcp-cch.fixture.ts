import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'ordinary MCP CCH Source')
if (process.env.CC_HAHA_MCP_CCH_FIXTURE !== '1') throw new Error('Unowned MCP CCH Source')
await import('../../preload')
const { signClaudeCodeCCHInString } = await import('../../src/utils/claudeCodeCch')
const inputs: string[] = await Bun.stdin.json()
process.stdout.write(JSON.stringify(inputs.map(signClaudeCodeCCHInString)) + '\n')
