import { parse } from 'acorn'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual lead mailbox partition and print formatter')
if (process.env.CC_HAHA_LEAD_PARTITION_FIXTURE !== '1') throw new Error('Missing lead partition fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { partitionLeadMailboxMessages } = await import('../../../src/utils/swarm/printLeaderPermissionBridge')
const { isShutdownApproved, formatTeammateMessages } = await import('../../../src/utils/teammateMailbox')
const { SHUTDOWN_TEAM_PROMPT } = await import('../../../src/utils/swarm/teamShutdownPrompt')
const file = new URL('../../../src/cli/print.ts', import.meta.url)
const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(file).text())
const parsed = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
let expression: string | undefined
function visit(node: any) {
  if (node.type === 'VariableDeclarator' && node.id?.name === 'formatted' && node.init && source.slice(node.init.start, node.init.end).includes('partitioned.remaining')) expression = source.slice(node.init.start, node.init.end)
  for (const key of Object.keys(node)) {
    const value = node[key]
    if (Array.isArray(value)) { for (const child of value) if (child?.type) visit(child) }
    else if (value?.type) visit(value)
  }
}
visit(parsed)
if (!expression) throw new Error('Missing actual print formatter')
const js = new Bun.Transpiler({ loader: 'ts' }).transformSync(`globalThis.__actualFormat = (partitioned: any, TEAMMATE_MESSAGE_TAG: string) => (${expression})`)
new Function('formatTeammateMessages', js)(formatTeammateMessages)
const format = (globalThis as any).__actualFormat
const rows = input.rows.map((messages: any[]) => {
  const partition = partitionLeadMailboxMessages(messages)
  try { return { partition, formatted: format(partition, 'teammate-message') } } catch (error) {
    if (!(error instanceof TypeError)) throw error
    return { partition, formatError: true }
  }
})
process.stdout.write(JSON.stringify({ rows, approvals: input.approvals.map(isShutdownApproved), shutdownPrompt: SHUTDOWN_TEAM_PROMPT, capabilities }))
