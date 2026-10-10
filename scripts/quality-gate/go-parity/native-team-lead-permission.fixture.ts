import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual headless leader permission bridge')
if (process.env.CC_HAHA_LEAD_PERMISSION_FIXTURE !== '1') throw new Error('Missing permission bridge fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { resolveTeammatePermissionRequests, partitionLeadMailboxMessages } = await import('../../../src/utils/swarm/printLeaderPermissionBridge')
const { readMailbox } = await import('../../../src/utils/teammateMailbox')
const RealDate = Date
class FixtureDate extends RealDate { constructor(...args: any[]) { if (!args.length) super('2026-10-07T00:00:00.000Z'); else super(...args as [any]) } static now() { return 1791331200000 } }
globalThis.Date = FixtureDate as any
const rows = []
for (let index=0;index<input.rows.length;index++) {
  const row=input.rows[index],teamName=`actual-permission-${index}`, asked:any[]=[]
  const host = { askHostForToolPermission: async (request:any) => { asked.push(request); const decision = row.decisions[request.displayName]; if (decision?.error) throw new Error(decision.error); return decision } }
  const partition=partitionLeadMailboxMessages(row.messages)
  await resolveTeammatePermissionRequests({host,canPromptHost:row.canPromptHost,teamName,...partition})
  const inboxes:any={}
  for (const name of row.recipients) inboxes[name]=(await readMailbox(name,teamName)).map(({id,...message}:any)=>message)
  asked.sort((a,b)=>String(a.displayName).localeCompare(String(b.displayName)))
  rows.push({asked,inboxes})
}
globalThis.Date=RealDate
process.stdout.write(JSON.stringify({rows,capabilities}))
