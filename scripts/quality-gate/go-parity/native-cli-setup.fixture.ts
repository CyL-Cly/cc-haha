import { parse } from 'acorn'
import { assertDisposableFixtureHome } from './fixture-guard'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Original Setup Hook stages')
if (process.env.CC_HAHA_CLI_SETUP_FIXTURE !== '1') throw new Error('Unowned Setup fixture')
await import('../../../preload')
const { hookJSONOutputSchema } = await import('../../../src/types/hooks')
const read = async (path: string) => {
  const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL(path, import.meta.url)).text())
  const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
  const nodes = ast.body.map((node: any) => node.type === 'ExportNamedDeclaration' ? node.declaration : node).filter((node: any) => node?.type === 'FunctionDeclaration')
  const extract = (names: string[]) => names.map(name => { const node = nodes.find((n: any) => n.id.name === name); if (!node) throw new Error('Missing Setup Source ' + name); return source.slice(node.start, node.end) }).join('\n')
  return { source, nodes, extract }
}
const hooks = await read('../../../src/utils/hooks.ts')
const starts = await read('../../../src/utils/sessionStart.ts')
const attachments = await read('../../../src/utils/attachments.ts')
const executor = hooks.nodes.find((n: any) => n.id.name === 'executeHooks')
const promises = executor?.body.body.find((n: any) => n.type === 'VariableDeclaration' && n.declarations.some((d: any) => d.id.name === 'hookPromises'))?.declarations.find((d: any) => d.id.name === 'hookPromises')?.init?.arguments?.[0]
const loop = executor?.body.body.find((n: any) => n.type === 'ForOfStatement' && n.await)
if (!promises || !loop) throw new Error('Missing actual startup executor')
const noop = () => {}
const produce = new Function('dependencies', `with(dependencies){return (${hooks.source.slice(promises.start, promises.end)})}`)
const rows = []
for (const row of input.rows) {
  let count = 0
  const uuid = () => 'startup-allocation-' + (++count)
  const allocation = new Function('randomUUID', attachments.extract(['createAttachmentMessage']) + '\nreturn createAttachmentMessage')(uuid)
  const outputs = new Function('hookJSONOutputSchema', 'jsonParse', 'jsonStringify', 'logForDebugging', 'createAttachmentMessage', hooks.extract(['validateHookJson', 'parseHookOutput', 'processHookJSONOutput']) + '\nreturn {parseHookOutput,processHookJSONOutput}')(hookJSONOutputSchema, JSON.parse, JSON.stringify, noop, allocation)
  const normalizeLegacyToolName = (name: string) => ({ Task: 'Agent', KillShell: 'TaskStop', AgentOutputTool: 'TaskOutput', BashOutputTool: 'TaskOutput' } as any)[name] || name
  const matcher = new Function('getHooksConfig', 'logForDebugging', 'normalizeLegacyToolName', 'getLegacyToolNames', 'DEFAULT_HOOK_SHELL', hooks.extract(['matchesPattern', 'hookDedupKey', 'prepareIfConditionMatcher', 'getMatchingHooks']) + '\nreturn getMatchingHooks')(() => row.settings.hooks?.Setup ?? [], noop, normalizeLegacyToolName, () => [], 'bash')
  const aggregate = new Function('all', 'createAttachmentMessage', 'logForDebugging', 'getHookDisplayText', 'getSessionId', 'getSessionHookCallback', 'logError', `return async function* (hookPromises,hookEvent,hookName,toolUseID){let permissionBehavior;const outcomes={success:0,blocking:0,non_blocking_error:0,cancelled:0};const matchingHooks=hookPromises.map(result=>({hook:result.hook,hookSource:'settings'}));const appState=undefined,matchQuery=undefined;${hooks.source.slice(loop.start, loop.end)}}`)(async function* (values: any[]) { for (const v of values) yield v }, allocation, noop, (h: any) => h.statusMessage || h.command || h.prompt || h.url, () => row.base.session_id, () => undefined, noop)
  const captured: any[] = []
  const execute = async function* (options: any) {
    captured.push(options.hookInput)
    if (row.settings.disableAllHooks) return
    const matching = await matcher(undefined, row.base.session_id, 'Setup', options.hookInput)
    for (const { hook } of matching) yield { message: { type: 'progress', data: { type: 'hook_progress', hookEvent: 'Setup', hookName: 'Setup:' + row.source, command: hook.statusMessage || hook.command, ...(hook.statusMessage != null ? { statusMessage: hook.statusMessage } : {}) }, parentToolUseID: options.toolUseID, toolUseID: options.toolUseID, timestamp: 'fixture', uuid: uuid() } }
    const values = []
    for (const { hook } of matching) {
      const output = row.outputs[hook.command]
      if (!output) throw new Error('Source command output primitive missing ' + hook.command)
      const producer = produce({ Date: { now: () => 0 }, signal: undefined, timeoutMs: 600000, messages: undefined, toolUseContext: undefined, hookName: 'Setup:' + row.source, hookEvent: 'Setup', toolUseID: options.toolUseID, createCombinedAbortSignal: () => ({ signal: new AbortController().signal, cleanup: noop }), randomUUID: uuid, getHookDisplayText: (h: any) => h.statusMessage || h.command, getJsonInput: () => ({ ok: true, value: JSON.stringify(options.hookInput) }), boundRequestPrompt: undefined, forceSyncExecution: options.forceSyncExecution, emitHookStarted: noop, emitHookResponse: noop, execCommandHook: async () => ({ stdout: output.stdout, stderr: output.stderr ?? '', output: output.stdout + (output.stderr ?? ''), status: output.code ?? 0, aborted: output.aborted ?? false }), createAttachmentMessage: allocation, parseHookOutput: outputs.parseHookOutput, processHookJSONOutput: outputs.processHookJSONOutput, isAsyncHookJSONOutput: (v: any) => v.async === true, isSyncHookJSONOutput: (v: any) => v.async !== true, chalk: { bold: (v: any) => v }, errorMessage: (e: any) => e.message })
      for await (const value of producer({ hook }, 0)) values.push(value)
    }
    yield* aggregate(values, 'Setup', 'Setup:' + row.source, options.toolUseID)
  }
  const wrapper = new Function('getSessionId', 'getTranscriptPathForSession', 'getCwd', 'getMainThreadAgentType', 'randomUUID', 'executeHooks', 'TOOL_HOOK_EXECUTION_TIMEOUT_MS', hooks.extract(['createBaseHookInput', 'executeSetupHooks']) + '\nreturn executeSetupHooks')(() => row.base.session_id, () => row.base.transcript_path, () => row.base.cwd, () => row.base.agent_type, uuid, execute, 600000)
  const original = new Function('isBareMode', 'shouldAllowManagedHooksOnly', 'logForDebugging', 'loadPluginHooks', 'executeSetupHooks', 'createAttachmentMessage', starts.extract(['processSetupHooks']) + '\nreturn processSetupHooks')(() => row.simple, () => false, noop, async () => {}, wrapper, allocation)
  const messages = await original(row.source, { forceSyncExecution: row.forceSyncExecution })
  const strip = (message: any) => { const { uuid, timestamp, ...record } = message; return record }
  rows.push({ input: captured[0], messages: messages.map(strip), allocations: count })
}
process.stdout.write(JSON.stringify({ rows }))
