import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
import { readFile } from 'node:fs/promises'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Native AskUserQuestion')
if (process.env.CC_HAHA_NATIVE_QUESTION_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Question fixture requires isolated production configuration')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
;(globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }).MACRO = { BUILD_TIME: '' }
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(input.homeDir)
bootstrap.setOriginalCwd(input.homeDir)
bootstrap.setProjectRoot(input.homeDir)
bootstrap.setIsInteractive(false)
bootstrap.setInitialMainLoopModel('deepseek-flash')
const config = await import('../../../src/utils/config')
config.enableConfigs()
const { AskUserQuestionTool } = await import('../../../src/tools/AskUserQuestionTool/AskUserQuestionTool')
const { toolToAPISchema } = await import('../../../src/utils/api')
const { getToolSchemaCache } = await import('../../../src/utils/toolSchemaCache')
const { getEmptyToolPermissionContext } = await import('../../../src/Tool')
if (input.permissionRejects) {
  const { createPermissionContext } = await import('../../../src/hooks/toolPermission/PermissionContext')
  const outputs = input.permissionRejects.map((item: any) => {
    const abortController = new AbortController()
    const context = createPermissionContext(AskUserQuestionTool, {}, { abortController } as any, { message: { id: 'original-question-api' } } as any, 'question-id', () => {})
    const decision = context.cancelAndAbort(item.feedback)
    return { decision, aborted: abortController.signal.aborted }
  })
  process.stdout.write(JSON.stringify({ outputs, capabilities }))
  process.exit(0)
}
if (input.ui) {
  const original = await readFile(new URL('../../../src/components/permissions/AskUserQuestionPermissionRequest/AskUserQuestionPermissionRequest.tsx', import.meta.url), 'utf8')
  const { ASK_USER_QUESTION_CLARIFY_WITH_QUESTIONS_PREFIX } = await import('../../../src/constants/messages')
  const transpiler = new Bun.Transpiler({ loader: 'tsx' })
  // Execute the actual original closures with their UI primitives observed.
  // No callback output is recorded or replaced. These cases have no images.
  function closure(variable: string, startMarker: string, endMarker: string) {
    const start = original.indexOf(startMarker)
    const end = original.indexOf(endMarker, start)
    if (start < 0 || end < 0) throw new Error('Original Question UI callback changed')
    const code = transpiler.transformSync(`let ${variable}; ${original.slice(start, end)}; return ${variable}`)
    return new Function('questions', 'questionStates', 'answers', 'toolUseConfirm', 'onDone', 'allImageAttachments', 'convertImagesToBlocks', 'metadataSource', 'isInPlanMode', 'ASK_USER_QUESTION_CLARIFY_WITH_QUESTIONS_PREFIX', code)
  }
  const converterStart = original.indexOf('async function convertImagesToBlocks(')
  const converterEnd = original.indexOf('\n//# sourceMappingURL=', converterStart)
  if (converterStart < 0 || converterEnd < 0) throw new Error('Original image converter changed')
  const converter = new Function(transpiler.transformSync(original.slice(converterStart, converterEnd)) + '; return convertImagesToBlocks')()
  const submit = closure('t15', 't15 = async answersToSubmit => {', '\n    $[48] =')
  const clarify = closure('t13', 't13 = async () => {', '\n    $[32] =')
  const finish = closure('t14', 't14 = async () => {', '\n    $[40] =')
  const outputs = []
  for (const item of input.ui) {
    const parsed = AskUserQuestionTool.inputSchema.parse(JSON.parse(item.inputSource))
    let observed: unknown
    let done = 0
    const confirm = { input: parsed, onAllow(updatedInput: unknown) { observed = { allowed: true, updatedInput } }, onReject(message: unknown) { observed = { allowed: false, message } } }
    const args = [parsed.questions, item.questionStates ?? {}, item.answers ?? {}, confirm, () => { done++ }, [], converter, undefined, item.plan ?? false, ASK_USER_QUESTION_CLARIFY_WITH_QUESTIONS_PREFIX]
    if (item.kind === 'clarify') await clarify(...args)()
    else if (item.kind === 'finish') await finish(...args)()
    else await submit(...args)(item.answers ?? {})
    outputs.push({ ...observed as object, done })
  }
  process.stdout.write(JSON.stringify({ outputs, capabilities }))
  process.exit(0)
}
const schemas = []
for (const format of [undefined, 'markdown', 'html']) {
  bootstrap.setQuestionPreviewFormat(format as any)
  getToolSchemaCache().clear()
  const schema = await toolToAPISchema(AskUserQuestionTool, { tools: [AskUserQuestionTool], agents: [], model: 'deepseek-flash', getToolPermissionContext: async () => getEmptyToolPermissionContext() })
  schemas.push(JSON.stringify(schema))
}
const observations = []
for (const step of input.steps) {
  bootstrap.setQuestionPreviewFormat(step.previewFormat as any)
  const parsed = AskUserQuestionTool.inputSchema.safeParse(JSON.parse(step.inputSource))
  if (!parsed.success) {
    observations.push({ name: step.name, success: false, issuesSource: JSON.stringify(parsed.error.issues) })
    continue
  }
  const data = parsed.data
  const permission = await AskUserQuestionTool.checkPermissions(data, {} as any)
  const validation = await AskUserQuestionTool.validateInput(data, {} as any)
  if (step.permissionUpdate) Object.assign(data, step.permissionUpdate)
  const result = await AskUserQuestionTool.call(data, {} as any)
  const mapped = AskUserQuestionTool.mapToolResultToToolResultBlockParam(result.data, 'actual-question-id')
  observations.push({ name: step.name, success: true, dataSource: JSON.stringify(parsed.data), permissionSource: JSON.stringify(permission), validationSource: JSON.stringify(validation), resultSource: JSON.stringify(result.data), mappedSource: JSON.stringify(mapped), classifier: AskUserQuestionTool.toAutoClassifierInput(data) })
}
process.stdout.write(JSON.stringify({ schemas, observations, enabled: AskUserQuestionTool.isEnabled(), readOnly: AskUserQuestionTool.isReadOnly(), concurrencySafe: AskUserQuestionTool.isConcurrencySafe({} as any), requiresUserInteraction: AskUserQuestionTool.requiresUserInteraction(), capabilities }))
