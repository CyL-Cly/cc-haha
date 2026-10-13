import { mkdir, readFile, stat, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as { homeDir: string }
assertDisposableFixtureHome(input.homeDir, 'Task assignment failure')
if (process.env.CC_HAHA_TASK_ASSIGNMENT_FAILURE_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Task assignment failure requires isolated production profile')
await mkdir(join(input.homeDir, '.git'), { recursive: true })
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
process.env.CC_HAHA_AGENT_TEAMS_ENABLED = '1'
process.env.CLAUDE_CODE_TASK_LIST_ID = 'committed'
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(input.homeDir)
bootstrap.setOriginalCwd(input.homeDir)
bootstrap.setProjectRoot(input.homeDir)
bootstrap.switchSession('00000000-0000-4000-8000-000000000000' as Parameters<typeof bootstrap.switchSession>[0])
bootstrap.setInitialMainLoopModel('deepseek-flash')
bootstrap.setIsInteractive(true)
const config = await import('../../../src/utils/config')
config.enableConfigs()
config.saveGlobalConfig(previous => ({ ...previous, userID: 'fixture-device' }))
const { setDynamicTeamContext } = await import('../../../src/utils/teammate')
setDynamicTeamContext({ agentId: 'lead@committed', agentName: 'team-lead', teamName: 'committed', planModeRequired: false })
const { TaskCreateTool } = await import('../../../src/tools/TaskCreateTool/TaskCreateTool')
const { TaskUpdateTool } = await import('../../../src/tools/TaskUpdateTool/TaskUpdateTool')
const { writeToMailbox, getInboxPath } = await import('../../../src/utils/teammateMailbox')
let appState: any = { todos: {}, expandedView: 'none' }
const context: any = { abortController: new AbortController(), getAppState: () => appState, setAppState: (update: any) => { appState = update(appState) } }
const created = await (TaskCreateTool.call as any)({ subject: 'committed task', description: 'actual blocked mailbox' }, context)
const taskId = created.data.task.id
const blocker = join(input.homeDir, '.claude/teams/committed/inboxes')
await mkdir(join(blocker, '..'), { recursive: true })
await writeFile(blocker, 'existing file blocks inbox creation')
// Prove the same actual mailbox writer fails at the physical boundary. Its
// false receipt is deliberately distinct from the committed TaskUpdate result.
const sent = await writeToMailbox('worker', { from: 'team-lead', text: 'probe', timestamp: new Date().toISOString() }, 'committed')
const updated = await (TaskUpdateTool.call as any)({ taskId, owner: 'worker' }, context)
const task = JSON.parse(await readFile(join(input.homeDir, '.claude/tasks/committed', `${taskId}.json`), 'utf8'))
const inbox = getInboxPath('worker', 'committed')
let inboxExists = true
try { await stat(inbox) } catch { inboxExists = false }
const mapped = TaskUpdateTool.mapToolResultToToolResultBlockParam(updated.data, 'actual-task-assignment')
const { getInMemoryErrors } = await import('../../../src/utils/log')
process.stdout.write(JSON.stringify({ sent, data: updated.data, mapped, task, blockerIsFile: (await stat(blocker)).isFile(), inboxExists, diagnosticErrors: getInMemoryErrors().map(item => item.error), capabilities }))
process.exit(0)
