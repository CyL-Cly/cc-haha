import fs from 'node:fs'
import { fileURLToPath } from 'node:url'
import { parse } from 'acorn'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'

// Bind the immutable pinned source through its physical path before the capability boundary.
const agentHookSourcePath = fs.realpathSync(fileURLToPath(new URL('../../../../src/utils/hooks/execAgentHook.ts', import.meta.url)))
const modelSourcePath = fs.realpathSync(fileURLToPath(new URL('../../../../src/utils/model/model.ts', import.meta.url)))
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'Agent Hook')
if (process.env.CC_HAHA_AGENT_HOOK_FIXTURE !== '1') throw new Error('Unowned Agent Hook fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../../preload')
const { createUserMessage, handleMessageFromStream, DONT_ASK_REJECT_MESSAGE } = await import('../../../../src/utils/messages')
const { createAttachmentMessage } = await import('../../../../src/utils/attachments')
const { toolMatchesName } = await import('../../../../src/Tool')
const { ALL_AGENT_DISALLOWED_TOOLS } = await import('../../../../src/constants/tools')
const { createAbortController } = await import('../../../../src/utils/abortController')
const { createCombinedAbortSignal } = await import('../../../../src/utils/combinedAbortSignal')
const { addArgumentsToPrompt, createStructuredOutputTool, hookResponseSchema, registerStructuredOutputEnforcement } = await import('../../../../src/utils/hooks/hookHelpers')
const { clearSessionHooks } = await import('../../../../src/utils/hooks/sessionHooks')
const { errorMessage } = await import('../../../../src/utils/errors')
const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(agentHookSourcePath).text())
const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
const implementation = ast.body.map((n: any) => n.type === 'ExportNamedDeclaration' ? n.declaration : n).find((n: any) => n?.type === 'FunctionDeclaration' && n.id.name === 'execAgentHook')
if (!implementation) throw new Error('Actual execAgentHook Source missing')
const modelSource = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(modelSourcePath).text())
const modelAst: any = parse(modelSource, { ecmaVersion: 'latest', sourceType: 'module' })
const runtimeDeclaration = modelAst.body.map((n: any) => n.type === 'ExportNamedDeclaration' ? n.declaration : n).find((n: any) => n?.type === 'FunctionDeclaration' && n.id.name === 'getRuntimeMainLoopModel')
if (!runtimeDeclaration) throw new Error('Actual runtime model Source missing')
const runtime = new Function('getUserSpecifiedModelSetting', 'getDefaultOpusModel', 'getDefaultSonnetModel', modelSource.slice(runtimeDeclaration.start, runtimeDeclaration.end) + ';return getRuntimeMainLoopModel')(() => 'opusplan', () => { throw new Error('dontAsk must not select plan model') }, () => { throw new Error('dontAsk must not select plan model') })
const noop = () => {}
const rows = []
const keepAlive = setInterval(noop, 1000)
try {
  for (const row of input.rows) {
    const app: any = { sessionHooks: new Map(), toolPermissionContext: { mode: 'default', alwaysAllowRules: { session: ['Read(/already)'], cliArg: ['Bash(echo *)'] } } }
    let captured: any
    let enforcement: any
    let yields = 0
    let returned = 0
    let responseLength = 0
    const streamModes: any[] = []
    const context: any = {
      ...(row.child ? { agentId: 'parent-agent' } : {}),
      options: { tools: ['Read', 'Write', 'Edit', 'Bash', 'Glob', 'Grep', 'TaskOutput', 'ExitPlanMode', 'EnterPlanMode', 'Agent', 'AskUserQuestion', 'TaskStop', 'Workflow', 'StructuredOutput', 'mcp__peer__echo'].map(name => ({ name })) },
      getAppState: () => app,
      setAppState: (updater: any) => updater(app),
      setResponseLength: (updater: any) => { responseLength = updater(responseLength) },
      setStreamMode: (mode: any) => streamModes.push(mode),
      abortController: createAbortController(),
    }
    const query = async function* (args: any) {
      const child = args.toolUseContext
      const projected = child.getAppState()
      const declaration = app.sessionHooks.get(child.agentId)?.hooks.Stop[0].hooks[0].hook
      enforcement = { type: declaration.type, timeout: declaration.timeout, errorMessage: declaration.errorMessage, empty: await declaration.callback([]), successful: await declaration.callback([{ type: 'assistant', message: { content: [{ type: 'tool_use', name: 'StructuredOutput', id: 'structured-id' }] } }, { type: 'user', message: { content: [{ type: 'tool_result', tool_use_id: 'structured-id', content: 'ok' }] } }]) }
      captured = {
        content: args.messages[0].message.content,
        systemPrompt: args.systemPrompt,
        model: child.options.mainLoopModel,
        querySource: args.querySource,
        agentID: child.agentId,
        thinking: child.options.thinkingConfig,
        nonInteractive: child.options.isNonInteractiveSession,
        tools: child.options.tools.map((t: any) => t.name),
        schema: child.options.tools.at(-1).inputJSONSchema,
        structuredPrompt: await child.options.tools.at(-1).prompt(),
        permission: projected.toolPermissionContext,
        parentPermissionUnchanged: app.toolPermissionContext.mode === 'default' && app.toolPermissionContext.alwaysAllowRules.session.length === 1,
        runtimeModel: runtime({ permissionMode: child.getAppState().toolPermissionContext.mode, mainLoopModel: child.options.mainLoopModel }),
        dontAskRejection: DONT_ASK_REJECT_MESSAGE('Write'),
        parses: (row.messages ?? []).filter((m: any) => m.type === 'attachment' && m.attachment.type === 'structured_output').map((m: any) => {
          const parsed = child.options.tools.at(-1).inputSchema.safeParse(m.attachment.data)
          return parsed.success ? { success: true, data: parsed.data } : { success: false, issues: parsed.error.issues }
        }),
      }
      try {
        if (row.waitAbort) {
          await new Promise((_, reject) => child.abortController.signal.addEventListener('abort', () => reject(new Error('aborted')), { once: true }))
        }
        if (row.error) throw new Error(row.error)
        for (const message of row.messages ?? []) {
          yields++
          yield message
        }
      } finally { returned++ }
    }
    const execute = new Function('dependencies', `with(dependencies){${source.slice(implementation.start, implementation.end)};return execAgentHook}`)({
      randomUUID: () => 'fixture', query, logEvent: noop, toolMatchesName,
      SYNTHETIC_OUTPUT_TOOL_NAME: 'StructuredOutput', ALL_AGENT_DISALLOWED_TOOLS,
      asAgentId: (v: any) => v, createAbortController, createAttachmentMessage,
      createCombinedAbortSignal, logForDebugging: noop, errorMessage, createUserMessage,
      handleMessageFromStream, getSmallFastModel: () => 'small-fast-model',
      hasPermissionsToUseTool: noop, getAgentTranscriptPath: () => '/original/child/transcript.jsonl',
      getTranscriptPath: () => '/original/main/transcript.jsonl', jsonStringify: JSON.stringify,
      asSystemPrompt: (v: any) => v, addArgumentsToPrompt, createStructuredOutputTool,
      hookResponseSchema, registerStructuredOutputEnforcement, clearSessionHooks,
    })
    const controller = createAbortController()
    if (row.parentAborted) controller.abort()
    const result = await execute(row.hook, 'Stop', 'Stop', '{"session_id":"actual-session"}', controller.signal, context, row.id, row.parentMessages ?? [])
    if (result.message) { const { uuid, timestamp, ...rest } = result.message; result.message = rest }
    rows.push({ result, captured, enforcement, yields, returned, leaked: app.sessionHooks.size, responseLength, streamModes })
  }
} finally { clearInterval(keepAlive) }
if (capabilities.blocked.length) throw new Error('Blocked original Agent Hook: ' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({ rows }) + '\n')
