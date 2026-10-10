import { assertDisposableFixtureHome } from './fixture-guard'
import { parse } from 'acorn'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Original UserPromptSubmit consumer')
if (process.env.CC_HAHA_NATIVE_HOOK_COMMAND_FIXTURE !== '1') throw new Error('Missing user prompt fixture')
await import('../../../preload')
const { hookJSONOutputSchema } = await import('../../../src/types/hooks')
let currentPrompt: any
let promptCapture: any
let execPromptHook: any
let capabilities: any
if (input.rows.some((row: any) => row.output.type === 'prompt')) {
  capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
  const state = await import('../../../src/bootstrap/state')
  state.setIsInteractive(false)
  const config = await import('../../../src/utils/config')
  config.enableConfigs()
  const { mock } = await import('bun:test')
  const api = await import('../../../src/services/api/claude')
  mock.module('../../../src/services/api/claude', () => ({ ...api, queryModelWithoutStreaming: async (args: any) => {
    promptCapture = { messages: args.messages, options: { ...args.options, getToolPermissionContext: undefined }, systemPrompt: args.systemPrompt, thinkingConfig: args.thinkingConfig, tools: args.tools }
    if (currentPrompt.error) throw new Error(currentPrompt.error)
    return { type: 'assistant', message: { content: [{ type: 'text', text: currentPrompt.response }] } }
  } }))
  execPromptHook = (await import('../../../src/utils/hooks/execPromptHook')).execPromptHook
}
const read = async (path: string) => {
  const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL(path, import.meta.url)).text())
  const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
  const nodes = ast.body.map((node: any) => node.type === 'ExportNamedDeclaration' ? node.declaration : node).filter((node: any) => node?.type === 'FunctionDeclaration')
  const extract = (names: string[]) => names.map(name => {
    const node = nodes.find((node: any) => node.id.name === name)
    if (!node) throw new Error('Missing actual user prompt Source ' + name)
    return source.slice(node.start, node.end)
  }).join('\n')
  return { source, nodes, extract }
}
const hooks = await read('../../../src/utils/hooks.ts')
const processing = await read('../../../src/utils/processUserInput/processUserInput.ts')
const messages = await read('../../../src/utils/messages.ts')
const attachments = await read('../../../src/utils/attachments.ts')
const executor = hooks.nodes.find((node: any) => node.id.name === 'executeHooks')
const loop = executor?.body.body.find((node: any) => node.type === 'ForOfStatement' && node.await)
const promises = executor?.body.body.find((node: any) => node.type === 'VariableDeclaration' && node.declarations.some((d: any) => d.id.name === 'hookPromises'))?.declarations.find((d: any) => d.id.name === 'hookPromises')?.init?.arguments?.[0]
if (!loop || !promises) throw new Error('Missing original command producer/completion loop')
const noop = () => {}
const rows = []
for (const row of input.rows) {
  currentPrompt = row.output
  promptCapture = undefined
  let allocations = 0
  const uuid = () => 'fixture-uuid-' + (++allocations)
  const factory = new Function('randomUUID', 'NO_CONTENT_MESSAGE', messages.extract(['createUserMessage', 'createSystemMessage', 'getContentText', 'extractTextContent']) + '\n' + attachments.extract(['createAttachmentMessage']) + '\nreturn {createUserMessage,createSystemMessage,getContentText,createAttachmentMessage}')(uuid, '(no content)')
  const outputs = new Function('hookJSONOutputSchema', 'jsonParse', 'jsonStringify', 'logForDebugging', 'createAttachmentMessage', hooks.extract(['validateHookJson', 'parseHookOutput', 'parseHttpHookOutput', 'processHookJSONOutput']) + '\nreturn {parseHookOutput,parseHttpHookOutput,processHookJSONOutput}')(hookJSONOutputSchema, JSON.parse, JSON.stringify, noop, factory.createAttachmentMessage)
  const captured: any[] = []
  const aggregate = new Function('all', 'createAttachmentMessage', 'logForDebugging', 'getHookDisplayText', 'getSessionId', 'getSessionHookCallback', 'logError', `return async function* (hookPromises,hookEvent,hookName,toolUseID){let permissionBehavior;const outcomes={success:0,blocking:0,non_blocking_error:0,cancelled:0};const matchingHooks=hookPromises.map(result=>({hook:result.hook,hookSource:'settings'}));const appState=undefined,matchQuery=undefined;${hooks.source.slice(loop.start, loop.end)}}`)(async function* (values: any[]) { for (const value of values) yield value }, factory.createAttachmentMessage, noop, (hook: any) => hook.command, () => row.base.session_id, () => undefined, noop)
  const produce = new Function('dependencies', `with(dependencies){return (${hooks.source.slice(promises.start, promises.end)})}`)
  const execute = async function* (options: any) {
    captured.push(options.hookInput)
    if (row.aborted) return
    const hook = row.output.type === 'prompt' || row.output.type === 'http' ? row.output.hook : { type: 'command', command: row.output.command }
    const context = { options: { tools: [] }, getAppState: () => ({ toolPermissionContext: { mode: row.permissionMode } }), setResponseLength: noop }
    const original = produce({ signal: undefined, timeoutMs: 600000, messages: undefined, toolUseContext: hook.type === 'prompt' ? context : undefined, hookName: 'UserPromptSubmit', hookEvent: 'UserPromptSubmit', toolUseID: options.toolUseID, createCombinedAbortSignal: () => ({ signal: new AbortController().signal, cleanup: noop }), randomUUID: uuid, getHookDisplayText: (h: any) => h.statusMessage || h.command || h.prompt || h.url, getJsonInput: () => ({ ok: true, value: JSON.stringify(options.hookInput) }), boundRequestPrompt: undefined, forceSyncExecution: false, emitHookStarted: noop, emitHookResponse: noop, execPromptHook, execHttpHook: async () => row.output.result, parseHttpHookOutput: outputs.parseHttpHookOutput, execCommandHook: async () => ({ stdout: row.output.stdout, stderr: row.output.stderr ?? '', output: row.output.stdout + (row.output.stderr ?? ''), status: row.output.code ?? 0, aborted: false }), createAttachmentMessage: factory.createAttachmentMessage, parseHookOutput: outputs.parseHookOutput, processHookJSONOutput: outputs.processHookJSONOutput, isAsyncHookJSONOutput: (value: any) => value.async === true, isSyncHookJSONOutput: (value: any) => value.async !== true, chalk: { bold: (value: any) => value }, errorMessage: (error: any) => error.message })
    const values = []
    for await (const value of original({ hook }, 0)) values.push(value)
    yield* aggregate(values, 'UserPromptSubmit', 'UserPromptSubmit', options.toolUseID)
  }
  const wrapper = new Function('getSessionId', 'getTranscriptPathForSession', 'getCwd', 'getMainThreadAgentType', 'randomUUID', 'hasHookForEvent', 'executeHooks', 'TOOL_HOOK_EXECUTION_TIMEOUT_MS', hooks.extract(['createBaseHookInput', 'executeUserPromptSubmitHooks', 'getUserPromptSubmitHookBlockingMessage']) + '\nreturn {executeUserPromptSubmitHooks,getUserPromptSubmitHookBlockingMessage}')(() => row.base.session_id, () => row.base.transcript_path, () => row.base.cwd, () => row.base.agent_type, uuid, () => row.present !== false, execute, 600000)
  // The input-base seam performs the independent ordinary-input producer, using
  // original message constructors. No prepared native hook decisions enter it.
  const base = async () => ({ messages: [factory.createUserMessage({ content: row.content, permissionMode: row.permissionMode, uuid: row.inputUUID }), ...(row.attachments ?? []).map(factory.createAttachmentMessage)], shouldQuery: row.shouldQuery !== false, allowedTools: row.allowedTools ?? [] })
  const original = new Function('processUserInputBase', 'queryCheckpoint', 'getContentText', 'executeUserPromptSubmitHooks', 'getUserPromptSubmitHookBlockingMessage', 'createSystemMessage', 'createUserMessage', 'createAttachmentMessage', 'randomUUID', 'MAX_HOOK_OUTPUT_LENGTH', processing.extract(['processUserInput', 'applyTruncation']) + '\nreturn processUserInput')(base, noop, factory.getContentText, wrapper.executeUserPromptSubmitHooks, wrapper.getUserPromptSubmitHookBlockingMessage, factory.createSystemMessage, factory.createUserMessage, factory.createAttachmentMessage, uuid, 10000)
  const result = await original({ input: row.content, mode: 'prompt', setToolJSX: noop, context: { getAppState: () => ({ toolPermissionContext: { mode: row.permissionMode } }), abortController: new AbortController() } })
  rows.push({ input: captured[0], result, allocations, promptCapture })
}
if (capabilities?.blocked.length) throw new Error('Blocked original prompt Hook: ' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({ rows }))
