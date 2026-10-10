import { dirname, join } from 'node:path'
import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual Team submit query boundary')
if (process.env.CC_HAHA_TEAM_TURN_FIXTURE !== '1' || new URL(input.origin).hostname !== '127.0.0.1') throw new Error('Missing loopback Team turn fixture')
const absent = [...absentManagedSettingsFixturePaths(), '/home/claude/.claude/remote/.oauth_token']
for (let p = dirname(input.homeDir);; p = dirname(p)) { absent.push(join(p, '.git')); if (p === dirname(p)) break }
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absent, loopbackFetchOrigin: input.origin })
await import('../../../preload')
process.env.USER_TYPE = 'external'
process.env.CC_HAHA_AGENT_TEAMS_ENABLED = '1'
process.env.CC_HAHA_TEAM_REVIEW_REQUIRED = '1'
process.env.CC_HAHA_TEAM_LEADER_RUNTIME = JSON.stringify({ providerId: 'fixture-provider', modelId: 'fixture-model' })
const state = await import('../../../src/bootstrap/state')
process.chdir(input.homeDir)
state.setCwdState(process.cwd()); state.setOriginalCwd(process.cwd()); state.setProjectRoot(process.cwd()); state.setIsInteractive(false)
state.switchSession('10000000-0000-4000-8000-000000000001' as any)
const config = await import('../../../src/utils/config'); config.enableConfigs()
const { getDefaultAppState } = await import('../../../src/state/AppStateStore')
const { createFileStateCacheWithSizeLimit } = await import('../../../src/utils/fileStateCache')
const { GENERAL_PURPOSE_AGENT } = await import('../../../src/tools/AgentTool/built-in/generalPurposeAgent')
const { TeamCreateTool } = await import('../../../src/tools/TeamCreateTool/TeamCreateTool')
const { TeamPlanTool } = await import('../../../src/tools/TeamPlanTool/TeamPlanTool')
const { TaskCreateTool } = await import('../../../src/tools/TaskCreateTool/TaskCreateTool')
const attachments = await import('../../../src/utils/attachments')
const originalAttachments = attachments.getAttachmentMessages
let attachmentCalls = 0
spyOn(attachments, 'getAttachmentMessages').mockImplementation(async function* (...args: any[]) { attachmentCalls++; yield* originalAttachments(...args as Parameters<typeof originalAttachments>) })
let app = getDefaultAppState()
const tools = [TeamPlanTool, TaskCreateTool]
const context: any = { options: { commands: [], debug: false, mainLoopModel: 'fixture-model', tools, verbose: false, thinkingConfig: { type: 'disabled' }, mcpClients: [], mcpResources: {}, isNonInteractiveSession: true, agentDefinitions: { activeAgents: [GENERAL_PURPOSE_AGENT], allAgents: [GENERAL_PURPOSE_AGENT] } }, abortController: new AbortController(), readFileState: createFileStateCacheWithSizeLimit(), getAppState: () => app, setAppState: (update: any) => { app = update(app) }, setInProgressToolUseIDs: () => {}, setResponseLength: () => {}, updateFileHistoryState: () => {}, updateAttributionState: () => {}, messages: [] }
await TeamCreateTool.call({ team_name: 'turn-boundary', plan: { members: [{ id: 'm', name: 'worker', prompt: 'Wait for actual human review.' }], tasks: [{ id: 'task', subject: 'Reviewed task', ownerId: 'm', dependencies: [] }] } }, context)
const { query } = await import('../../../src/query')
const { productionDeps } = await import('../../../src/query/deps')
const { createUserMessage } = await import('../../../src/utils/messages')
const { asSystemPrompt } = await import('../../../src/utils/systemPromptType')
const { readTeamPlan } = await import('../../../src/utils/swarm/teamPlanStore')
const { listTasks, getCanonicalTeamTaskListId } = await import('../../../src/utils/tasks')
const observations = []
for (let i = 0; i < 2; i++) {
  const before = attachmentCalls
  const output = []
  const run = query({ messages: [createUserMessage({ content: i ? 'Continue after review.' : 'Submit the proposal.' })], systemPrompt: asSystemPrompt(['Actual Team boundary system.']), userContext: {}, systemContext: {}, canUseTool: async (_tool: any, value: any) => ({ behavior: 'allow', updatedInput: value }), toolUseContext: context, querySource: 'sdk', maxTurns: 3, deps: productionDeps() })
  let terminal: any
  while (true) { const next = await run.next(); if (next.done) { terminal = next.value; break }; output.push(next.value) }
  observations.push({ terminal, output, attachments: attachmentCalls - before, aborted: context.abortController.signal.aborted })
}
process.stdout.write(JSON.stringify({ observations, plan: await readTeamPlan('turn-boundary'), tasks: await listTasks(getCanonicalTeamTaskListId('turn-boundary')), capabilities }))
