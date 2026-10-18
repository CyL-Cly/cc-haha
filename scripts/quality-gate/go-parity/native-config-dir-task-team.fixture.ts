import fs from 'node:fs'
import { join } from 'node:path'
import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Current task/team configuration-directory consumers')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_CONFIG_DIR_TASK_TEAM_SOURCE !== '1' || !['between', 'held-lifecycle', 'held-mailbox', 'held-config'].includes(input.profile)) throw new Error('Pinned declared task/team directory profile required')
const initial = join(home, '.claude'), next = join(home, 'owned-next')
for (const directory of [initial, next]) fs.mkdirSync(directory, { recursive: true })
const caps = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
;(globalThis as any).MACRO = { VERSION: '999.0.0-current-task-directory', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setOriginalCwd(home)
bootstrap.setCwdState(home)
const tasks = await import('../../../src/utils/tasks')
const teams = await import('../../../src/utils/swarm/teamHelpers')
const mailbox = await import('../../../src/utils/teammateMailbox')
const locks = await import('../../../src/utils/lockfile')
const list = 'Owned_List-é😀', team = 'Owned_Team-é😀', member = 'worker-é😀'
const taskData = (subject: string) => ({ subject, description: subject + ' description', status: 'pending' as const, blocks: [], blockedBy: [], metadata: { future: { keep: subject } } })
const message = (id: string) => ({ id, from: 'team-lead', text: id, timestamp: '2026-10-14T00:00:00.000Z' })
const config = (description: string) => ({ name: team, description, createdAt: 1700000000000, leadAgentId: 'team-lead', members: [], futureDirectory: { keep: description } })
const lockEvents: Array<{ action: string, path: string }> = []
const originalLock = locks.lock
let holdPath: string | null = null
let markEntered: (() => void) | null = null
let unblock: (() => void) | null = null
let gate: Promise<void> | null = null
spyOn(locks, 'lock').mockImplementation(async (path, options) => {
  const release = await originalLock(path, options)
  lockEvents.push({ action: 'acquired', path })
  if (path === holdPath && gate) {
    holdPath = null
    markEntered!()
    await gate
  }
  return async () => {
    await release()
    lockEvents.push({ action: 'released', path })
  }
})
const hold = (path: string) => {
  holdPath = path
  const entered = new Promise<void>(resolve => { markEntered = resolve })
  gate = new Promise<void>(resolve => { unblock = resolve })
  return entered
}
const paths = () => ({ tasks: tasks.getTasksDir(list), inbox: mailbox.getInboxPath(member, team), config: teams.getTeamFilePath(team) })
const read = (path: string) => { try { return fs.readFileSync(path, 'utf8') } catch { return null } }
const files = (directory: string): Record<string, string> => {
  const out: Record<string, string> = {}
  const walk = (path: string) => {
    for (const entry of fs.readdirSync(path, { withFileTypes: true })) {
      const child = join(path, entry.name)
      if (entry.isDirectory()) { if (!entry.name.endsWith('.lock')) walk(child) }
      else if (entry.isFile()) out[child.slice(directory.length + 1)] = fs.readFileSync(child, 'utf8')
    }
  }
  walk(directory)
  return out
}
process.env.CLAUDE_CONFIG_DIR = initial
const oldPaths = paths()
const first = await tasks.createTaskWithCommit(list, taskData('old-task'))
await teams.writeTeamFileAsync(team, config('old-config'))
if (!await mailbox.writeToMailbox(member, message('old-message'), team)) throw new Error('Original old inbox write failed')
process.env.CLAUDE_CONFIG_DIR = next
const newPaths = paths()
const second = await tasks.createTaskWithCommit(list, taskData('new-task'))
await teams.writeTeamFileAsync(team, config('new-config'))
if (!await mailbox.writeToMailbox(member, message('new-message'), team)) throw new Error('Original new inbox write failed')
process.env.CLAUDE_CONFIG_DIR = initial
const before = { task: await tasks.getTask(list, first.taskId), config: await teams.readTeamFileAsync(team), inbox: await mailbox.readMailbox(member, team) }
const beforeFiles = { initial: files(initial), next: files(next) }
let outcome: unknown = null
let leaseAtSwitch: string | null = null
if (input.profile === 'between') {
  process.env.CLAUDE_CONFIG_DIR = next
  const updated = await tasks.updateTask(list, second.taskId, { subject: 'new-updated' })
  await teams.mutateTeamFileAsync(team, current => { current.description = 'new-updated'; (current as any).futureMutation = { keep: true } })
  const unread = await mailbox.readUnreadMessages(member, team)
  const claimed = await mailbox.claimMailboxMessages(member, team, unread)
  if (!await mailbox.writeToMailbox(member, message('new-after-switch'), team)) throw new Error('Original current inbox write failed')
  outcome = { updated, claimed }
} else {
  const target = input.profile === 'held-lifecycle' ? join(initial, 'tasks', '.lifecycle-locks', list.replace(/[^a-zA-Z0-9_-]/g, '-') + '.lock') : input.profile === 'held-mailbox' ? oldPaths.inbox : oldPaths.config
  const entered = hold(target)
  const work = input.profile === 'held-lifecycle'
    ? tasks.withTaskListLifecycleLock(list, () => tasks.createTaskWithCommit(list, taskData('after-held-lifecycle')))
    : input.profile === 'held-mailbox'
      ? mailbox.writeToMailbox(member, message('after-held-mailbox'), team)
      : teams.mutateTeamFileAsync(team, current => { current.description = 'after-held-config'; (current as any).futureMutation = { keep: true } })
  await entered
  const leasePath = target + '.lock'
  if (!fs.statSync(leasePath).isDirectory()) throw new Error('Actual Source lease was not physically held')
  leaseAtSwitch = leasePath
  process.env.CLAUDE_CONFIG_DIR = next
  unblock!()
  outcome = await work
}
const after = { task: await tasks.getTask(list, second.taskId), config: await teams.readTeamFileAsync(team), inbox: await mailbox.readMailbox(member, team), history: await mailbox.readMailboxHistory(member, team) }
const afterFiles = { initial: files(initial), next: files(next) }
if (caps.blocked.length || caps.secureReads.length) throw new Error('Task/team current-directory Source escaped private boundary ' + JSON.stringify(caps))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ initial, next, list, team, member, oldPaths, newPaths, first, second, before, outcome, after, beforeFiles, afterFiles, leaseAtSwitch, lockEvents, caps, scope: { originalTaskTransactions: true, originalMailboxTransactions: true, originalTeamConfigTransactions: true, physicalHeldLease: input.profile !== 'between', mainConstructor: false, modelHTTP: false } }), error => error ? reject(error) : resolve()))
process.exit(0)
