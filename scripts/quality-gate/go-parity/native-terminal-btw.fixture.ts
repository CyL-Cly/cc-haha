import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { PassThrough } from 'node:stream'
import { createRequire } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual BTW command, forked query, mounted keyboard and cleanup')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_BTW_SOURCE !== '1') throw new Error('Pinned BTW Source required')
let mode = 'answer', activeResponse = input.response ?? 'ACTUAL_SIDE_RESPONSE', calls = 0, bodies: any[] = [], release: (() => void) | undefined
const wire = (text: string) => [
  { type: 'message_start', message: { id: 'actual-side', type: 'message', role: 'assistant', model: 'fixture-model', content: [], stop_reason: null, stop_sequence: null, usage: { input_tokens: 7, output_tokens: 0 } } },
  { type: 'content_block_start', index: 0, content_block: { type: 'text', text: '' } },
  { type: 'content_block_delta', index: 0, delta: { type: 'text_delta', text } },
  { type: 'content_block_stop', index: 0 }, { type: 'message_delta', delta: { stop_reason: 'end_turn', stop_sequence: null }, usage: { output_tokens: 5 } }, { type: 'message_stop' },
].map(data => `event: ${data.type}\ndata: ${JSON.stringify(data)}\n\n`).join('')
const peer = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) {
  calls++; bodies.push(await request.json())
  if (mode === 'hold' || mode === 'usage') await new Promise<void>(resolve => release = resolve)
  return new Response(wire(mode === 'empty' ? '' : activeResponse), { headers: { 'content-type': 'text/event-stream' } })
} })
const origin = `http://127.0.0.1:${peer.port}`
process.env.ANTHROPIC_BASE_URL = origin
process.env.ANTHROPIC_API_KEY = 'sk-ant-btw-disposable'
const absentPaths = absentManagedSettingsFixturePaths()
for (const path of ['/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token', join(dirname(process.execPath), 'rg')]) if (!fs.existsSync(path)) absentPaths.push(path)
for (let parent = home; ; parent = dirname(parent)) { const candidate = join(parent, '.git'); if (!fs.existsSync(candidate)) absentPaths.push(candidate); if (dirname(parent) === parent) break }
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
const replSource = ts.createSourceFile('REPL.tsx', fs.readFileSync(new URL('../../../src/screens/REPL.tsx', import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
let immediateNode: any
const visit = (node: any) => { if (ts.isVariableDeclaration(node) && node.name.getText(replSource) === 'executeImmediateCommand') immediateNode = node; ts.forEachChild(node, visit) }; visit(replSource)
if (!immediateNode) throw new Error('Actual immediate caller missing')
const immediateCode = new Bun.Transpiler({ loader: 'tsx' }).transformSync('const ' + immediateNode.getText(replSource) + ';')
const caps = await installClientFixtureCapabilities(home, { absentPaths, loopbackFetchOrigin: origin })
;(globalThis as any).MACRO = { VERSION: '999.0.0-btw-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home); state.setOriginalCwd(home); state.setProjectRoot(home); state.setAllowedSettingSources(['userSettings']); state.setIsInteractive(true)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const { normalizeApiKeyForConfig } = await import('../../../src/utils/authPortable')
config.saveGlobalConfig(current => ({ ...current, customApiKeyResponses: { approved: [normalizeApiKeyForConfig(process.env.ANTHROPIC_API_KEY!)], rejected: [] } }))
await Bun.write(join(home, '.claude', 'settings.json'), JSON.stringify({ syntaxHighlightingDisabled: true }))
const React = await import('react')
const { render, useInput } = await import('../../../src/ink')
const instances = (await import('../../../src/ink/instances')).default
const { charInCellAt } = await import('../../../src/ink/screen')
const { AppStateProvider, getDefaultAppState } = await import('../../../src/state/AppState')
const { createUserMessage } = await import('../../../src/utils/messages')
const { createFileStateCacheWithSizeLimit } = await import('../../../src/utils/fileStateCache')
const { saveCacheSafeParams } = await import('../../../src/utils/forkedAgent')
const { asSystemPrompt } = await import('../../../src/utils/systemPromptType')
const { call } = await import('../../../src/commands/btw/btw')
const declaration = (await import('../../../src/commands/btw/index')).default
const { processSlashCommand } = await import('../../../src/utils/processUserInput/processSlashCommand')
const { query } = await import('../../../src/query')
const messageFactories = await import('../../../src/utils/messages')
const { getCommandName } = await import('../../../src/commands')
const { escapeXml } = await import('../../../src/utils/xml')
const { createAbortController } = await import('../../../src/utils/abortController')
const { isFullscreenEnvEnabled } = await import('../../../src/utils/fullscreen')
function ReplInputPump({ children }: any) { useInput(() => {}); return children }
const settle = async () => { for (let i = 0; i < 10; i++) await new Promise(resolve => setImmediate(resolve)); await new Promise(resolve => setTimeout(resolve, 25)) }
const results = []
for (const row of input.rows) {
  mode = row.mode ?? 'answer'; calls = 0; bodies = []; release = undefined
  activeResponse = row.response ?? input.response ?? 'ACTUAL_SIDE_RESPONSE'
  process.env.CLAUDE_CODE_NO_FLICKER = row.fullscreen ? '1' : '0'
  config.saveGlobalConfig(current => ({ ...current, btwUseCount: Object.hasOwn(row, 'count') ? row.count : 0, futureBtw: { keep: true } }))
  let appState = getDefaultAppState()
  const parentController = new AbortController()
  const messages = [createUserMessage({ content: 'UNCHANGED_PARENT_CONTEXT' })]
  const before = JSON.stringify(messages)
  const context = { options: { commands: [declaration], debug: false, mainLoopModel: 'fixture-model', tools: [], verbose: false, thinkingConfig: { type: 'disabled' }, mcpClients: [], mcpResources: {}, isNonInteractiveSession: false, agentDefinitions: { activeAgents: [], allAgents: [] } }, abortController: parentController, readFileState: createFileStateCacheWithSizeLimit(100), getAppState: () => appState, setAppState: (update: any) => appState = update(appState), setInProgressToolUseIDs: () => {}, setResponseLength: () => {}, updateFileHistoryState: () => {}, updateAttributionState: () => {}, messages } as any
  saveCacheSafeParams({ systemPrompt: asSystemPrompt(['cached BTW system prefix']), userContext: {}, systemContext: {}, toolUseContext: context, forkContextMessages: messages })
  const events: any[] = []
  if (row.mode === 'usage-idle') {
    const result = await processSlashCommand('/btw' + (row.args ? ' ' + row.args : ''), [], [], [], context, () => {})
    results.push({ result, calls, count: config.getGlobalConfig().btwUseCount, parentUnchanged: before === JSON.stringify(messages), parentAborted: parentController.signal.aborted })
    continue
  }
  if (row.mode === 'usage') {
    let parentSettled = false
    const parent = (async () => { for await (const _ of query({ messages, systemPrompt: asSystemPrompt(['cached BTW system prefix']), userContext: {}, systemContext: {}, canUseTool: async () => ({ behavior: 'deny', message: 'fixture' }), toolUseContext: context, querySource: 'repl_main_thread', maxTurns: 1 })) {} parentSettled = true })()
    while (!calls) await settle()
    const notifications: any[] = [], immediateMessages: any[] = []
    const dependencies = { setToolJSX: () => {}, addNotification: (value: any) => notifications.push(value), isFullscreenEnvEnabled, createCommandInputMessage: messageFactories.createCommandInputMessage, formatCommandInputTags: messageFactories.formatCommandInputTags, getCommandName, commandArgs: row.args ?? '', LOCAL_COMMAND_STDOUT_TAG: 'local-command-stdout', escapeXml, createUserMessage, setMessages: (update: any) => { immediateMessages.splice(0, immediateMessages.length, ...update(immediateMessages)) }, stashedPrompt: undefined, helpers: {}, setInputValue: () => {}, setPastedContents: () => {}, setStashedPrompt: () => {}, getToolUseContext: () => context, messagesRef: { current: messages }, createAbortController, mainLoopModel: 'fixture-model', matchingCommand: declaration }
    const execute = new Function(...Object.keys(dependencies), immediateCode + '\nreturn executeImmediateCommand')(...Object.values(dependencies))
    await execute()
    const result = { messages: immediateMessages, notifications }
    const parentPendingAtCompletion = !parentSettled
    release?.(); await parent
    results.push({ result, calls, parentPendingAtCompletion, count: config.getGlobalConfig().btwUseCount, parentUnchanged: before === JSON.stringify(messages), parentAborted: parentController.signal.aborted })
    continue
  }
  const stdin = new PassThrough() as any
  stdin.isTTY = true; stdin.setRawMode = () => {}; stdin.ref = () => stdin; stdin.unref = () => stdin
  const stdout = new PassThrough() as any
  stdout.isTTY = false; stdout.columns = row.width ?? 100; stdout.rows = row.height ?? 35
  let output = ''
  stdout.on('data', chunk => output += chunk.toString())
  const node = await call((...values: any[]) => events.push(values), context, row.args ?? 'owned side question')
  const app = await render(React.createElement(AppStateProvider, { initialState: appState }, React.createElement(ReplInputPump, null, node)), { stdin, stdout, stderr: stdout, patchConsole: false, exitOnCtrlC: false })
  const deadline = Date.now() + 5000
  while (!calls) { if (Date.now() > deadline) throw new Error('Actual side model did not start: ' + output + JSON.stringify(caps)); await settle() }
  await settle()
  if (mode !== 'hold') while (!output.includes(mode === 'empty' ? 'No response received' : 'ACTUAL_SIDE_RESPONSE') && !(output.includes('↑/↓ to scroll') && output.includes('dismiss'))) { if (Date.now() > deadline) throw new Error('Actual BTW output did not finish ' + output); await settle() }
  const screenText = () => { const screen = (instances.get(stdout) as any).frontFrame.screen; const lines = []; for (let y = 0; y < screen.height; y++) { let line = ''; for (let x = 0; x < screen.width; x++) line += charInCellAt(screen, x, y) ?? ' '; lines.push(line.trimEnd()) } return lines.join('\n') }
  let layout
  if (input.layout) {
    const visit = (n: any): any => ({ name: n.nodeName, text: n.nodeValue, scrollTop: n.scrollTop, scrollHeight: n.scrollHeight, viewportHeight: n.viewportHeight, layout: n.yogaNode ? { width: n.yogaNode.getComputedWidth(), height: n.yogaNode.getComputedHeight(), left: n.yogaNode.getComputedLeft(), top: n.yogaNode.getComputedTop() } : undefined, children: n.childNodes?.map(visit) })
    layout = visit(instances.get(stdout)!.rootNode)
  }
  const frames = [{ key: 'mount', text: screenText(), events: structuredClone(events), layout }]
  for (const key of row.keys ?? []) { stdin.write(key); if (key === '\x1b') await new Promise(resolve => setTimeout(resolve, 120)); await settle(); frames.push({ key, text: screenText(), events: structuredClone(events) }) }
  app.unmount(); release?.(); await settle()
  results.push({ frames, events, calls, bodies, count: config.getGlobalConfig().btwUseCount, future: config.getGlobalConfig().futureBtw, parentUnchanged: before === JSON.stringify(messages), parentAborted: parentController.signal.aborted })
}
peer.stop(true)
if (caps.blocked.length || caps.secureReads.some(value => value !== 'read')) throw new Error('BTW Source escaped private capabilities ' + JSON.stringify(caps))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ declaration: { name: declaration.name, type: declaration.type, immediate: declaration.immediate, description: declaration.description, argumentHint: declaration.argumentHint }, rows: results, blocked: caps.blocked, emptyCredentialReads: caps.secureReads }), error => error ? reject(error) : resolve()))
process.exit(0)
