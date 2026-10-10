import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'Terminal Bash input')
if (process.env.CC_HAHA_NATIVE_TERMINAL_BASH_FIXTURE !== '1') throw new Error('Unowned terminal Bash fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { processBashCommand } = await import('../../../src/utils/processUserInput/processBashCommand')
const { BashTool } = await import('../../../src/tools/BashTool/BashTool')
const { ShellError } = await import('../../../src/utils/errors')
const priorCall = BashTool.call
const rows = []
try {
 for (const row of input.rows) {
  const calls = [] as any[]
  const ui = [] as any[]
  // Replace the external shell invocation only. Original processBashCommand,
  // processToolResultBlock, Bash mapping and message factories stay actual.
  BashTool.call = (async (args: any) => {
   calls.push(args)
   if (row.shellError) throw new ShellError(row.shellError.stdout, row.shellError.stderr, row.shellError.code, row.shellError.interrupted)
   if (row.error) throw new Error(row.error)
   return { data: row.data }
  }) as any
  const result = await processBashCommand(row.command, [], [], { options: { verbose: false } } as any, value => ui.push(value))
  rows.push({ messages: result.messages.map((message: any) => ({content: message.message?.content, isMeta: message.isMeta ?? null})), shouldQuery: result.shouldQuery, calls, uiStarted: ui[0] !== null, uiCleared: ui.at(-1) === null })
 }
} finally {BashTool.call=priorCall}
if (capabilities.blocked.length) throw new Error('Blocked terminal Bash fixture: '+capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({rows,capabilities:{blocked:capabilities.blocked}})+'\n')
