import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual in-process teammate context and physical sidechain')
if (process.env.CC_HAHA_INPROCESS_TEAM_CONTEXT_FIXTURE !== '1') throw new Error('Missing teammate context fixture')
const absent = [...absentManagedSettingsFixturePaths()]
for (let path = input.homeDir;; path = dirname(path)) { absent.push(join(path, '.git')); if (dirname(path) === path) break }
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absent })
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
process.chdir(input.homeDir)
bootstrap.setCwdState(process.cwd()); bootstrap.setOriginalCwd(process.cwd()); bootstrap.setProjectRoot(process.cwd()); bootstrap.setIsInteractive(false)
bootstrap.switchSession(input.sessionId)
const config = await import('../../../src/utils/config'); config.enableConfigs()
const teammate = await import('../../../src/utils/teammate')
const context = await import('../../../src/utils/teammateContext')
const tasks = await import('../../../src/utils/tasks')
const storage = await import('../../../src/utils/sessionStorage')
const { createUserMessage } = await import('../../../src/utils/messages')
process.env.CLAUDE_CODE_TASK_LIST_ID = 'unrelated-standalone'
teammate.setDynamicTeamContext({ agentId: 'wrong@fallback', agentName: 'wrong', teamName: 'fallback', planModeRequired: false })
const rows = await Promise.all(input.rows.map(async (row: any) => {
  const original = context.createTeammateContext({ ...row.identity, abortController: new AbortController() })
  return context.runWithTeammateContext(original, async () => {
    await Promise.resolve()
    const identity = { TeamName: teammate.getTeamName(), AgentName: teammate.getAgentName(), AgentID: teammate.getAgentId(), ParentSessionID: teammate.getParentSessionId(), Color: teammate.getTeammateColor(), PlanModeRequired: teammate.isPlanModeRequired() }
    const list = tasks.getTaskListId(row.writerId)
    const message = createUserMessage({ content: `Actual ${row.writerId}` })
    await storage.recordSidechainTranscript([message], row.writerId)
    return { identity, list, path: storage.getAgentTranscriptPath(row.writerId) }
  })
}))
await storage.flushSessionStorage()
for (const row of rows) {
  const messages = (await Bun.file(row.path).text()).trim().split('\n').map(text => JSON.parse(text)).filter(message => message.type === 'user')
  ;(row as any).records = messages.map(message => ({ isSidechain: message.isSidechain, agentId: message.agentId, teamName: message.teamName ?? null, agentName: message.agentName ?? null, sessionId: message.sessionId }))
  delete (row as any).path
}
process.stdout.write(JSON.stringify({ rows, capabilities }))
