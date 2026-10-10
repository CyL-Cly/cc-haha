import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.homeDir, 'Native Bash result')
if (process.env.CC_HAHA_NATIVE_BASH_RESULT_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Unowned Bash result fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { interpretCommandResult } = await import('../../../src/tools/BashTool/commandSemantics')
const { ShellError } = await import('../../../src/utils/errors')
const { formatError } = await import('../../../src/utils/toolErrors')
const { BashTool } = await import('../../../src/tools/BashTool/BashTool')
const semantics = input.commands.map((row: any) => interpretCommandResult(row.command, row.code, '', '').isError)
const errors = input.errors.map((row: any) => formatError(new ShellError(row.stdout, row.stderr, row.code, row.interrupted)))
const mapped = input.results.map((row: any) => BashTool.mapToolResultToToolResultBlockParam(row, 'bash-result-id'))
process.stdout.write(JSON.stringify({ semantics, errors, mapped, maxResultSizeChars: BashTool.maxResultSizeChars, capabilities }))
