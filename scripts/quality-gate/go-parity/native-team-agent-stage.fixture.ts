import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual Agent draft staging')
if (process.env.CC_HAHA_TEAM_STAGE_FIXTURE !== '1') throw new Error('Missing Team stage fixture')
const absent = [...absentManagedSettingsFixturePaths(), '/home/claude/.claude/remote/.oauth_token']
for (let p = dirname(input.homeDir);; p = dirname(p)) { absent.push(join(p, '.git')); if (p === dirname(p)) break }
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absent })
await import('../../../preload')
process.env.USER_TYPE = 'external'; process.env.CC_HAHA_AGENT_TEAMS_ENABLED = '1'; process.env.CC_HAHA_TEAM_REVIEW_REQUIRED = '1'
process.env.CC_HAHA_TEAM_LEADER_RUNTIME = JSON.stringify({ providerId: 'fixture-provider', modelId: 'fixture-model', effortLevel: 'high' })
const bootstrap = await import('../../../src/bootstrap/state')
process.chdir(input.homeDir); bootstrap.setCwdState(process.cwd()); bootstrap.setOriginalCwd(process.cwd()); bootstrap.setProjectRoot(process.cwd()); bootstrap.setIsInteractive(false)
const config = await import('../../../src/utils/config'); config.enableConfigs()
const { getDefaultAppState } = await import('../../../src/state/AppStateStore')
const { createFileStateCacheWithSizeLimit } = await import('../../../src/utils/fileStateCache')
const { GENERAL_PURPOSE_AGENT } = await import('../../../src/tools/AgentTool/built-in/generalPurposeAgent')
const { TeamCreateTool } = await import('../../../src/tools/TeamCreateTool/TeamCreateTool')
const { AgentTool } = await import('../../../src/tools/AgentTool/AgentTool')
const { readTeamPlan } = await import('../../../src/utils/swarm/teamPlanStore')
bootstrap.switchSession('10000000-0000-4000-8000-000000000001' as any)
let app = getDefaultAppState()
const context: any = { options: { commands: [], tools: [], mainLoopModel: 'fixture-model', thinkingConfig: { type: 'disabled' }, mcpClients: [], mcpResources: {}, isNonInteractiveSession: true, agentDefinitions: { activeAgents: [GENERAL_PURPOSE_AGENT], allAgents: [GENERAL_PURPOSE_AGENT] } }, abortController: new AbortController(), readFileState: createFileStateCacheWithSizeLimit(), getAppState: () => app, setAppState: (update: any) => { app = update(app) }, setResponseLength: () => {}, messages: [] }
await TeamCreateTool.call({ team_name: 'draft-stage' }, context)
const rows = []
for (const row of input.rows) {
  if (row.envModel !== undefined) process.env.CLAUDE_CODE_SUBAGENT_MODEL = row.envModel
  else delete process.env.CLAUDE_CODE_SUBAGENT_MODEL
  let data: any = null, error: any = null, mapError: any = null
  try {
    data = (await AgentTool.call(row.input, context, async (_tool: any, value: any) => ({ behavior: 'allow', updatedInput: value }), undefined as any)).data
    try { AgentTool.mapToolResultToToolResultBlockParam(data, 'stage-id') } catch (e) { mapError = (e as Error).message }
  } catch (e) { error = (e as Error).message }
  if (data) delete data.plan_id
  const plan = await readTeamPlan('draft-stage')
  rows.push({ data, error, mapError, state: plan?.state, revision: plan?.revision, members: plan?.members })
}
process.stdout.write(JSON.stringify({ rows, capabilities }))
