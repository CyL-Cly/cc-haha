import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import { installClientFixtureCapabilities } from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Original statusline prompt command')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_STATUSLINE_PROMPT_SOURCE !== '1') throw new Error('Pinned private statusline source required')
if (Object.keys(input).some(key => !['home', 'arguments'].includes(key)) || !Array.isArray(input.arguments) || input.arguments.some(value => typeof value !== 'string')) throw new Error('Original statusline arguments required')
const capabilities = await installClientFixtureCapabilities(home)
const { default: command } = await import('../../../../src/commands/statusline')
const rows = []
for (const argument of input.arguments) rows.push(await command.getPromptForCommand(argument))
if (capabilities.blocked.length) throw new Error('Original statusline escaped private fixture: ' + JSON.stringify(capabilities))
process.stdout.write(JSON.stringify({ metadata: command, rows, capabilities }))
