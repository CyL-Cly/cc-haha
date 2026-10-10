import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
import { readFile, writeFile, mkdir } from 'node:fs/promises'
import { join } from 'node:path'

async function writeOutput(value: unknown) { await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify(value), error => error ? reject(error) : resolve())) }
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Native PlanMode')
if (process.env.CC_HAHA_NATIVE_PLAN_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Plan fixture requires isolated production configuration')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
;(globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }).MACRO = { BUILD_TIME: '' }
const state = await import('../../../src/bootstrap/state')
state.setCwdState(input.homeDir)
state.setOriginalCwd(input.homeDir)
state.setProjectRoot(input.homeDir)
state.setIsInteractive(false)
state.setInitialMainLoopModel('deepseek-flash')
const config = await import('../../../src/utils/config')
config.enableConfigs()
const { EnterPlanModeTool } = await import('../../../src/tools/EnterPlanModeTool/EnterPlanModeTool')
const { ExitPlanModeV2Tool } = await import('../../../src/tools/ExitPlanModeTool/ExitPlanModeV2Tool')
const plans = await import('../../../src/utils/plans')
const { toolToAPISchema, normalizeToolInput, normalizeToolInputForAPI } = await import('../../../src/utils/api')
const { getToolSchemaCache } = await import('../../../src/utils/toolSchemaCache')
const { getEmptyToolPermissionContext } = await import('../../../src/Tool')
const tools = [EnterPlanModeTool, ExitPlanModeV2Tool]
if (input.taskHints) {
  const { AgentTool } = await import('../../../src/tools/AgentTool/AgentTool')
  const observations = []
  for (const [index, row] of input.taskHints.entries()) {
    process.env.CC_HAHA_AGENT_TEAMS_ENABLED = row.enabled ? '1' : '0'
    state.switchSession(`10000000-0000-4000-8000-${String(index).padStart(12, '0')}` as any)
    let app = { toolPermissionContext: { ...getEmptyToolPermissionContext(), mode: 'plan' } }
    const context: any = { options: { tools: row.names.map((name: string) => name === 'Agent' ? AgentTool : { name, aliases: [] }) }, getAppState: () => app, setAppState: (update: any) => { app = update(app) } }
    const result = await ExitPlanModeV2Tool.call({ plan: 'Actual Teams-enabled ordinary plan' }, context)
    observations.push(result.data.hasTaskTool ?? false)
  }
  await writeOutput({ observations, capabilities })
  process.exit(0)
}
if (input.attachments || input.estimates) {
  const { spyOn } = await import('bun:test')
  const auth = await import('../../../src/utils/auth')
  const growthbook = await import('../../../src/services/analytics/growthbook')
  let options: any = {}
  spyOn(auth, 'getSubscriptionType').mockImplementation(() => options.subscriptionType ?? null)
  spyOn(auth, 'getRateLimitTier').mockImplementation(() => options.rateLimitTier ?? null)
  // Project configuration is the shared getter boundary. Observe the actual
  // renderer without walking an unrelated repository outside disposable HOME.
  spyOn(config, 'getCurrentProjectConfig').mockImplementation(() => ({ allowedTools: options.allowedTools ?? [] } as any))
  spyOn(growthbook, 'getFeatureValue_CACHED_MAY_BE_STALE').mockImplementation((key: string, fallback: any) => key === 'tengu_pewter_ledger' ? options.phase4Variant ?? null : key === 'tengu_amber_stoat' ? options.exploreAgents ?? true : fallback)
  const { normalizeAttachmentForAPI } = await import('../../../src/utils/messages')
  const { tokenCountWithEstimation } = await import('../../../src/utils/tokens')
  const { roughTokenCountEstimationForMessages } = await import('../../../src/services/tokenEstimation')
  const observations = []
  const estimates = []
  for (const row of input.attachments ?? input.estimates) {
    options = row.options ?? {}
    for (const key of ['CLAUDE_CODE_PLAN_V2_AGENT_COUNT', 'CLAUDE_CODE_PLAN_V2_EXPLORE_AGENT_COUNT', 'CLAUDE_CODE_PLAN_MODE_INTERVIEW_PHASE', 'EMBEDDED_SEARCH_TOOLS', 'USER_TYPE', 'CLAUDE_CODE_ENTRYPOINT']) delete process.env[key]
    Object.assign(process.env, options.environment ?? {})
    process.env.CLAUDE_CODE_PLAN_MODE_INTERVIEW_PHASE = options.interviewPhase ? 'true' : 'false'
    if (options.embeddedSearchTools) process.env.EMBEDDED_SEARCH_TOOLS = 'true'
    if (input.estimates) {
      const history = JSON.parse(row.history)
      const observe = (fn: () => number) => {
        try { return { value: fn() } } catch (error) { return { error: { name: error instanceof Error ? error.name : 'Error', message: error instanceof Error ? error.message : String(error) } } }
      }
      estimates.push({ tokens: observe(() => tokenCountWithEstimation(history)), rough: observe(() => roughTokenCountEstimationForMessages(history)) })
    } else {
      const output = normalizeAttachmentForAPI(JSON.parse(row.payload))
      observations.push(output.map((record: any) => ({ type: record.type, message: record.message, isMeta: record.isMeta })))
    }
  }
  await writeOutput(input.estimates ? { estimates, capabilities } : { observations, capabilities })
  process.exit(0)
}
const apiInputs = (input.apiInputs ?? []).map((value: any) => normalizeToolInputForAPI(ExitPlanModeV2Tool, JSON.parse(value)))
if (input.updateSchemas) {
  const { PermissionUpdateSchema } = await import('../../../src/entrypoints/sdk/coreSchemas')
  const { permissionUpdateSchema } = await import('../../../src/utils/permissions/PermissionUpdateSchema')
  const schemas = input.updateSchemas.map((source: string) => {
    const value = JSON.parse(source)
    const project = (parsed: any) => parsed.success ? { success: true, data: parsed.data } : { success: false, issues: parsed.error.issues }
    return { sdk: project(PermissionUpdateSchema().safeParse(value)), internal: project(permissionUpdateSchema().safeParse(value)) }
  })
  await writeOutput({ schemas, apiInputs, capabilities })
  process.exit(0)
}
if (input.permissionUpdates) {
  const updates = await import('../../../src/utils/permissions/PermissionUpdate')
  const observations = input.permissionUpdates.map((item: any) => {
    state.setHasExitedPlanMode(false)
    state.setNeedsPlanModeExitAttachment(false)
    const initial = { ...getEmptyToolPermissionContext(), ...item.context, additionalWorkingDirectories: new Map(item.directories ?? []) }
    const result = updates.applyPermissionUpdates(initial, item.updates)
    return { context: { ...result, additionalWorkingDirectories: [...result.additionalWorkingDirectories] }, hasExited: state.hasExitedPlanModeInSession(), needsExit: state.needsPlanModeExitAttachment() }
  })
  await writeOutput({ observations, apiInputs, capabilities })
  process.exit(0)
}
const schemas = []
for (const userType of ['external', 'ant']) {
  process.env.USER_TYPE = userType
  for (const interview of ['false', 'true']) {
    process.env.CLAUDE_CODE_PLAN_MODE_INTERVIEW_PHASE = interview
    getToolSchemaCache().clear()
    schemas.push(JSON.stringify(await Promise.all(tools.map(tool => toolToAPISchema(tool, { tools, agents: [], model: 'deepseek-flash', getToolPermissionContext: async () => getEmptyToolPermissionContext() })))))
  }
}
process.env.USER_TYPE = 'external'
const observations = input.inputs.map((item: any) => {
  const tool = item.tool === 'EnterPlanMode' ? EnterPlanModeTool : ExitPlanModeV2Tool
  const parsed = tool.inputSchema.safeParse(JSON.parse(item.input))
  return parsed.success ? { success: true, data: JSON.stringify(parsed.data) } : { success: false, issues: JSON.stringify(parsed.error.issues) }
})
const calls = []
for (const item of input.calls ?? []) {
  const tool = item.tool === 'EnterPlanMode' ? EnterPlanModeTool : ExitPlanModeV2Tool
  state.switchSession(item.sessionId)
  plans.setPlanSlug(item.sessionId, item.slug)
  state.setHasExitedPlanMode(false)
  state.setNeedsPlanModeExitAttachment(item.needsExit ?? false)
  process.env.CLAUDE_CODE_PLAN_MODE_INTERVIEW_PHASE = item.interview ? 'true' : 'false'
  let app = { toolPermissionContext: { ...getEmptyToolPermissionContext(), mode: item.mode, ...(item.prePlanMode ? { prePlanMode: item.prePlanMode } : {}) } }
  const context: any = { getAppState: () => app, setAppState: (update: any) => { app = update(app) }, agentId: item.agentId, options: { tools, mainLoopModel: 'deepseek-flash' } }
  const path = plans.getPlanFilePath(item.agentId)
  if (item.disk !== undefined) { await mkdir(join(path, '..'), { recursive: true }); await writeFile(path, item.disk, 'utf8') }
  const parsed = tool.inputSchema.parse(JSON.parse(item.input ?? '{}'))
  const validation = await tool.validateInput?.(parsed, context)
  if (validation && !validation.result) { calls.push({ validation, mode: app.toolPermissionContext.mode }); continue }
  const normalized = normalizeToolInput(tool, parsed, item.agentId)
  const permission = await tool.checkPermissions(normalized, context)
  if (item.updatedInput !== undefined) Object.keys(normalized).forEach(key => delete (normalized as any)[key])
  if (item.updatedInput !== undefined) Object.assign(normalized, item.updatedInput)
  try {
    const result = await tool.call(normalized, context)
    const mapped = tool.mapToolResultToToolResultBlockParam(result.data, 'plan-tool-id')
    let disk: string | null = null
    try { disk = (await readFile(path)).toString('hex') } catch {}
    calls.push({ result: result.data, mapped, normalized, permission, mode: app.toolPermissionContext.mode, prePlanMode: app.toolPermissionContext.prePlanMode ?? null, hasExited: state.hasExitedPlanModeInSession(), needsExit: state.needsPlanModeExitAttachment(), disk })
  } catch (error) { calls.push({ error: error instanceof Error ? error.message : String(error), mode: app.toolPermissionContext.mode }) }
}
await writeOutput({ schemas, observations, calls, apiInputs, capabilities })
