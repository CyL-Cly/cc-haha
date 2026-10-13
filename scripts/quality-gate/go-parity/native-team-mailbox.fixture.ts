import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { dirname } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Team mailbox')
if (process.env.CC_HAHA_TEAM_MAILBOX_FIXTURE !== '1') throw new Error('Missing team mailbox fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const mailbox = await import('../../../src/utils/teammateMailbox')
const { setDynamicTeamContext } = await import('../../../src/utils/teammate')
setDynamicTeamContext({ agentId: 'fixture@inherited', agentName: 'fixture', teamName: 'inherited', planModeRequired: false })
const rows = []
for (const row of input.cases) {
  const path = mailbox.getInboxPath(row.name, row.team)
  if (row.initial !== undefined) {
    await mkdir(dirname(path), { recursive: true })
    await writeFile(path, row.initial)
  }
  for (const operation of row.operations) {
    if (operation.type === 'write') await mailbox.writeToMailbox(row.name, operation.message, row.team)
    if (operation.type === 'parallel') await Promise.all(operation.messages.map((message: any) => mailbox.writeToMailbox(row.name, message, row.team)))
    if (operation.type === 'index') { if (typeof mailbox.claimMailboxMessages === 'function') { const current = await mailbox.readMailbox(row.name, row.team); const selected = current[operation.index]; if (selected) await mailbox.claimMailboxMessages(row.name, row.team, [selected]) } else await mailbox.markMessageAsReadByIndex(row.name, row.team, operation.index) }
    if (operation.type === 'predicate') await mailbox.markMessagesAsReadByPredicate(row.name, message => message.from === operation.from, row.team)
    if (operation.type === 'all') { if (typeof mailbox.claimMailboxMessages === 'function') await mailbox.claimMailboxMessages(row.name, row.team, await mailbox.readUnreadMessages(row.name, row.team)); else await mailbox.markMessagesAsRead(row.name, row.team) }
    if (operation.type === 'clear') await mailbox.clearMailbox(row.name, row.team)
  }
  const value = await mailbox.readMailbox(row.name, row.team)
  let unread: any = null, error: string | null = null
  try { unread = await mailbox.readUnreadMessages(row.name, row.team) } catch (err) { error = (err as Error).message }
  let physical: string | null = null
  try { physical = await readFile(path, 'utf8') } catch {}
  rows.push({ path: path.slice(input.homeDir.length), value, unread, error, physical, formatted: Array.isArray(value) && value.every(item => item && typeof item === 'object') ? mailbox.formatTeammateMessages(value) : null })
}
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ rows, capabilities }), () => resolve()))
process.exit(0)
