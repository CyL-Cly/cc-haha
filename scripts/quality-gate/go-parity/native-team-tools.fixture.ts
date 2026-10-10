import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Team tools schema/prompt/parser')
if (process.env.CC_HAHA_TEAM_TOOLS_FIXTURE !== '1') throw new Error('Missing Team tools fixture')
const absent = [...absentManagedSettingsFixturePaths()]
for (let directory = dirname(input.homeDir);; directory = dirname(directory)) {
  absent.push(join(directory, '.git'))
  if (directory === dirname(directory)) break
}
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absent })
await import('../../../preload')
process.env.USER_TYPE = 'external'
process.env.CC_HAHA_AGENT_TEAMS_ENABLED = '1'
const state = await import('../../../src/bootstrap/state')
state.setCwdState(input.homeDir)
state.setOriginalCwd(input.homeDir)
state.setProjectRoot(input.homeDir)
state.setIsInteractive(false)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const { TeamCreateTool } = await import('../../../src/tools/TeamCreateTool/TeamCreateTool')
const { TeamDeleteTool } = await import('../../../src/tools/TeamDeleteTool/TeamDeleteTool')
const { TeamPlanTool } = await import('../../../src/tools/TeamPlanTool/TeamPlanTool')
const { toolToAPISchema } = await import('../../../src/utils/api')
const { getEmptyToolPermissionContext } = await import('../../../src/Tool')
const { clearToolSchemaCache } = await import('../../../src/utils/toolSchemaCache')
const tools = { TeamCreate: TeamCreateTool, TeamDelete: TeamDeleteTool, TeamPlan: TeamPlanTool }
const rows = input.rows.map((row: any) => {
  const parsed = tools[row.tool as keyof typeof tools].inputSchema.safeParse(JSON.parse(row.input))
  return parsed.success ? { success: true, data: JSON.stringify(parsed.data) } : { success: false, issues: JSON.stringify(parsed.error.issues) }
})
const profiles = []
for (const review of [false, true]) {
  clearToolSchemaCache()
  if (review) process.env.CC_HAHA_TEAM_REVIEW_REQUIRED = '1'
  else delete process.env.CC_HAHA_TEAM_REVIEW_REQUIRED
  const definitions = []
  for (const tool of Object.values(tools)) {
    definitions.push({
      schema: await toolToAPISchema(tool as any, { tools: Object.values(tools), agents: [], model: 'deepseek-flash', getToolPermissionContext: async () => getEmptyToolPermissionContext() } as any),
      prompt: await tool.prompt({} as any), description: await tool.description({} as any),
      hint: tool.searchHint, deferred: !!tool.shouldDefer, always: !!tool.alwaysLoad,
    })
  }
  profiles.push(definitions)
}
process.stdout.write(JSON.stringify({ rows, profiles, capabilities }))
