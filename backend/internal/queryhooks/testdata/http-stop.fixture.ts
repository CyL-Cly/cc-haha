import fs from 'node:fs'
import { fileURLToPath } from 'node:url'
import { mock } from 'bun:test'
import { parse } from 'acorn'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'
// Bind the immutable pinned source through its physical path before the capability boundary.
const hooksSourcePath = fs.realpathSync(fileURLToPath(new URL('../../../../src/utils/hooks.ts', import.meta.url)))
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'HTTP Stop aggregation')
if (process.env.CC_HAHA_HTTP_STOP_FIXTURE !== '1') throw new Error('Unowned HTTP Stop fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../../preload')
const state = await import('../../../../src/bootstrap/state')
state.setIsInteractive(false)
state.switchSession('http-source-session')
const config = await import('../../../../src/utils/config')
config.enableConfigs()
const originalHooks = await import('../../../../src/utils/hooks')
const attachments = await import('../../../../src/utils/attachments')
const { hookJSONOutputSchema } = await import('../../../../src/types/hooks')
const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(hooksSourcePath).text())
const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
const functions = ast.body.map((node: any) => node.type === 'ExportNamedDeclaration' ? node.declaration : node).filter((node: any) => node?.type === 'FunctionDeclaration')
const extract = (names: string[]) => names.map(name => { const node = functions.find((n: any) => n.id.name === name); if (!node) throw new Error('Missing original HTTP Stop function ' + name); return source.slice(node.start, node.end) }).join('\n')
const executor = functions.find((n: any) => n.id.name === 'executeHooks')
const loop = executor?.body.body.find((n: any) => n.type === 'ForOfStatement' && n.await)
const promises = executor?.body.body.find((n: any) => n.type === 'VariableDeclaration' && n.declarations.some((d: any) => d.id.name === 'hookPromises'))?.declarations.find((d: any) => d.id.name === 'hookPromises')?.init?.arguments?.[0]
if (!loop || !promises) throw new Error('Missing actual HTTP executor')
const noop = () => {}
const outputs = new Function('hookJSONOutputSchema', 'jsonParse', 'jsonStringify', 'logForDebugging', 'createAttachmentMessage', extract(['validateHookJson', 'parseHttpHookOutput', 'processHookJSONOutput']) + '\nreturn {parseHttpHookOutput,processHookJSONOutput}')(hookJSONOutputSchema, JSON.parse, JSON.stringify, noop, attachments.createAttachmentMessage)
const produce = new Function('dependencies', `with(dependencies){return (${source.slice(promises.start, promises.end)})}`)
const aggregate = new Function('all', 'createAttachmentMessage', 'logForDebugging', 'getHookDisplayText', 'getSessionId', 'getSessionHookCallback', 'logError', `return async function* (hookPromises,hookEvent,hookName,toolUseID){let permissionBehavior;const outcomes={success:0,blocking:0,non_blocking_error:0,cancelled:0};const matchingHooks=hookPromises.map(result=>({hook:result.hook,hookSource:'settings'}));const appState=undefined,matchQuery=undefined;${source.slice(loop.start, loop.end)}}`)(async function* (values: any[]) { for (const v of values) yield v }, attachments.createAttachmentMessage, noop, (hook: any) => hook.statusMessage || hook.url, () => state.getSessionId(), () => undefined, noop)
let current: any
mock.module('../../../../src/utils/hooks', () => ({ ...originalHooks, executeStopHooks: async function* (_mode: any, signal: any) {
  const hook = current.hook
  yield { message: { type: 'progress', data: { type: 'hook_progress', hookEvent: 'Stop', hookName: 'Stop', command: hook.statusMessage || hook.url, ...(hook.statusMessage != null ? { statusMessage: hook.statusMessage } : {}) }, parentToolUseID: 'http-stop-id', toolUseID: 'http-stop-id', timestamp: 'source', uuid: 'source' } }
  const producer = produce({ Date: { now: () => 0 }, signal, timeoutMs: 600000, messages: [], toolUseContext: undefined, hookName: 'Stop', hookEvent: 'Stop', toolUseID: 'http-stop-id', createCombinedAbortSignal: () => ({ signal: new AbortController().signal, cleanup: noop }), randomUUID: () => 'http-hook-id', getHookDisplayText: (h: any) => h.statusMessage || h.url, getJsonInput: () => ({ ok: true, value: '{"session_id":"http-source-session"}' }), emitHookStarted: noop, emitHookResponse: noop, execHttpHook: async () => current.response, parseHttpHookOutput: outputs.parseHttpHookOutput, processHookJSONOutput: outputs.processHookJSONOutput, isAsyncHookJSONOutput: (v: any) => v.async === true, createAttachmentMessage: attachments.createAttachmentMessage })
  const values = []
  for await (const value of producer({ hook }, 0)) values.push(value)
  yield* aggregate(values, 'Stop', 'Stop', 'http-stop-id')
} }))
const { handleStopHooks } = await import('../../../../src/query/stopHooks')
const rows = []
for (const row of input.rows) {
  current = row
  const app: any = { toolPermissionContext: { mode: 'default' }, sessionHooks: new Map(), mcp: { clients: [] } }
  const context: any = { options: { tools: [] }, getAppState: () => app, setAppState: (updater: any) => Object.assign(app, updater(app)), setResponseLength: noop, abortController: new AbortController() }
  const generator = handleStopHooks([], [], [], {}, {}, context, 'sdk')
  const messages = []
  let result: any
  for (;;) { const step = await generator.next(); if (step.done) { result = step.value; break }; const { uuid, timestamp, ...record } = step.value as any; messages.push(record) }
  const strip = (m: any) => { const { uuid, timestamp, ...record } = m; return record }
  rows.push({ messages, result: { ...result, blockingErrors: result.blockingErrors.map(strip) } })
}
if (capabilities.blocked.length) throw new Error('Blocked original HTTP Stop: ' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({ rows }))
