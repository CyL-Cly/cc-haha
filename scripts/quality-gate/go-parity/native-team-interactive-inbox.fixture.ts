import { parse } from 'acorn'
import { mkdir } from 'node:fs/promises'
import { dirname, join } from 'node:path'
import * as children from 'node:child_process'
import { syncBuiltinESMExports } from 'node:module'
import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
import { exposeSourceBindings } from './source-exports'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const originalSpawn = children.spawn, originalBunSpawn = Bun.spawn.bind(Bun)
const originalBunSpawnSync = Bun.spawnSync.bind(Bun)
assertDisposableFixtureHome(input.homeDir, 'Actual interactive leader inbox and attachments')
if (process.env.CC_HAHA_INTERACTIVE_INBOX_FIXTURE !== '1') throw new Error('Missing interactive inbox fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
let trustedPane = '', victimPane = ''
const paneRuns: any[] = []
const paneCommand = async (args: string[]) => {
  const child = originalBunSpawn([input.realPaneBinary, '-L', `claude-swarm-${process.pid}`, ...args], { cwd: input.homeDir, env: process.env, stdin: 'ignore', stdout: 'pipe', stderr: 'pipe' })
  const [out, error, code] = await Promise.all([new Response(child.stdout).text(), new Response(child.stderr).text(), child.exited])
  if (code !== 0) throw new Error(`Private tmux fixture failed: ${error}`)
  return out.trim()
}
if (input.realPaneBinary) {
  process.env.TMUX_TMPDIR = join(input.homeDir, 'private-tmux')
  await mkdir(process.env.TMUX_TMPDIR, { recursive: true })
  process.env.PATH = `${dirname(input.realPaneBinary)}:/usr/bin:/bin`
  process.env.DEBUG = '1'
  process.env.CLAUDE_CODE_DEBUG_LOGS_DIR = join(input.homeDir, 'debug-log.txt')
  delete process.env.TMUX
  process.on('exit', () => { originalBunSpawnSync([input.realPaneBinary, '-L', `claude-swarm-${process.pid}`, 'kill-server'], { stdin: 'ignore', stdout: 'ignore', stderr: 'ignore', env: process.env }) })
  trustedPane = await paneCommand(['-f', '/dev/null', 'new-session', '-d', '-s', 'actual-private-fixture', '-P', '-F', '#{pane_id}', 'sleep 30'])
  victimPane = await paneCommand(['split-window', '-h', '-t', trustedPane, '-P', '-F', '#{pane_id}', 'sleep 30'])
  const permitted = (file: string, args: string[], options: any) => {
    if (!['tmux', input.realPaneBinary].includes(file) || options?.shell || options?.cwd && options.cwd !== input.homeDir || JSON.stringify(args) !== JSON.stringify(['-L', `claude-swarm-${process.pid}`, 'kill-pane', '-t', trustedPane])) throw new Error('Trusted pane shutdown escaped private socket')
  }
  const spawn = ((file: string, args: string[], options: any) => {
    permitted(file, args, options)
    const child = originalSpawn(file, args, options), entry: any = { args, tmpdir: options?.env?.TMUX_TMPDIR, stderr: '' }
    paneRuns.push(entry); child.stderr?.on('data', chunk => { entry.stderr += String(chunk) }); child.on('close', code => { entry.code = code })
    return child
  }) as typeof children.spawn
  require('node:child_process').spawn = spawn
  spyOn(children, 'spawn').mockImplementation(spawn)
  Bun.spawn = ((command: any, nativeOptions: any = {}) => { const options = Array.isArray(command) ? nativeOptions : command, args = Array.isArray(command) ? command : command.cmd; permitted(args[0], args.slice(1), options); return originalBunSpawn(command, nativeOptions) }) as typeof Bun.spawn
  syncBuiltinESMExports()
}
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(input.homeDir); bootstrap.setOriginalCwd(input.homeDir)
let bindingNames: string[] = []
function moduleBindings(source: string): string[] {
  const js = new Bun.Transpiler({ loader: 'ts' }).transformSync(source)
  const ast: any = parse(js, { ecmaVersion: 'latest', sourceType: 'module' })
  const names = new Set<string>()
  const pattern = (node: any) => {
    if (!node) return
    if (node.type === 'Identifier') names.add(node.name)
    else if (node.type === 'ObjectPattern') node.properties.forEach((entry: any) => pattern(entry.value ?? entry.argument))
    else if (node.type === 'ArrayPattern') node.elements.forEach(pattern)
    else if (node.type === 'AssignmentPattern') pattern(node.left)
    else if (node.type === 'RestElement') pattern(node.argument)
  }
  for (const top of ast.body) {
    const node = top.type === 'ExportNamedDeclaration' ? top.declaration : top
    if (!node) continue
    if (node.type === 'ImportDeclaration') node.specifiers.forEach((entry: any) => pattern(entry.local))
    else if (node.type === 'FunctionDeclaration' || node.type === 'ClassDeclaration') pattern(node.id)
    else if (node.type === 'VariableDeclaration') node.declarations.forEach((entry: any) => pattern(entry.id))
  }
  return [...names]
}
Bun.plugin({ name: 'actual-interactive-inbox-private-bindings', setup(build) {
  build.onLoad({ filter: /\/useInboxPoller\.ts$/ }, async args => { const source = await Bun.file(args.path).text(); bindingNames = moduleBindings(source); return { contents: `${source}\nexport const fixtureInboxBindings = { ${bindingNames.join(', ')} }`, loader: 'ts' } })
  build.onLoad({ filter: /\/attachments\.ts$/ }, async args => ({ contents: exposeSourceBindings(await Bun.file(args.path).text(), ['getTeammateMailboxAttachments']), loader: 'ts' }))
  build.onLoad({ filter: /\/UserTeammateMessage\.tsx$/ }, async args => ({ contents: exposeSourceBindings(await Bun.file(args.path).text(), ['parseTeammateMessages', 'TEAMMATE_MSG_REGEX'], 'tsx'), loader: 'tsx' }))
} })
const hook: any = await import('../../../src/hooks/useInboxPoller')
const attachments: any = await import('../../../src/utils/attachments')
const mailbox = await import('../../../src/utils/teammateMailbox')
const helpers = await import('../../../src/utils/swarm/teamHelpers')
const tasks = await import('../../../src/utils/tasks')
const { normalizeAttachmentForAPI } = await import('../../../src/utils/messages')
const { getDefaultAppState } = await import('../../../src/state/AppStateStore')
const { createStore } = await import('../../../src/state/store')
const bridge = await import('../../../src/utils/swarm/leaderPermissionBridge')
const teammate = await import('../../../src/utils/teammate')
const permissionCallbacks = await import('../../../src/hooks/useSwarmPermissionPoller')
const teammateUI: any = await import('../../../src/components/messages/UserTeammateMessage')
const xml = (input.xml ?? []).map(teammateUI.parseTeammateMessages)
const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/hooks/useInboxPoller.ts', import.meta.url)).text())
const ast = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
let pollBody: any, pollInboxBody: any
function walk(node: any) {
  if (!node || typeof node !== 'object') return
  if (node.type === 'VariableDeclarator' && node.id?.name === 'poll') pollBody = node.init.arguments[0].body
  if (node.type === 'VariableDeclarator' && node.id?.name === 'pollInbox') pollInboxBody = node.init.arguments[0].body
  for (const value of Object.values(node)) { if (Array.isArray(value)) value.forEach(walk); else if (value && typeof value === 'object') walk(value) }
}
walk(ast)
if (!pollBody || !pollInboxBody) throw new Error('Actual useInboxPoller callbacks changed')
const AsyncFunction = Object.getPrototypeOf(async function () {}).constructor
const pollInbox = new AsyncFunction(...bindingNames, 'enabled', 'store', 'setAppState', 'isLoading', 'focusedInputDialog', 'terminal', 'onSubmitTeammateMessage', source.slice(pollInboxBody.start + 1, pollInboxBody.end - 1))
const poll = new AsyncFunction('pollInFlightRef', 'pollInbox', source.slice(pollBody.start + 1, pollBody.end - 1))
const cleanMessages = (messages: any[]) => messages.map(({ id, read, ...row }) => ({ ...row, ...(read === undefined ? {} : { read }) }))
const parsed = (input.parsers ?? []).map((text: string) => {
  const values = ['isPermissionRequest', 'isPermissionResponse', 'isSandboxPermissionRequest', 'isSandboxPermissionResponse', 'isShutdownRequest', 'isShutdownApproved', 'isTeamPermissionUpdate', 'isModeSetRequest', 'isPlanApprovalRequest', 'isPlanApprovalResponse'].map(name => ({ name, value: (mailbox as any)[name](text) })).filter(row => row.value !== null)
  return { values, structured: mailbox.isStructuredProtocolMessage(text) }
})
const rows = []
for (let index = 0; index < (input.rows ?? []).length; index++) {
  const row = input.rows[index], team = `actual-interactive-${index}`
  if (input.realPaneBinary) {
    for (const member of row.members) if (member.tmuxPaneId === 'TRUSTED_PANE') member.tmuxPaneId = trustedPane
    for (const message of row.messages) if (message.text.startsWith('{')) { const body = JSON.parse(message.text); if (body.paneId === 'VICTIM_PANE') { body.paneId = victimPane; message.text = JSON.stringify(body) } }
  }
  if (!row.missingConfig) await helpers.writeTeamFileAsync(team, { name: team, createdAt: 1, members: row.members })
  for (const task of row.tasks ?? []) await tasks.createTask(team, task)
  const polledName = row.worker?.agentName ?? 'team-lead'
  if (row.worker) teammate.setDynamicTeamContext({ ...row.worker, teamName: team })
  else teammate.setDynamicTeamContext(undefined)
  const decisions: any[] = []
  permissionCallbacks.clearAllPendingCallbacks()
  for (const requestId of row.callbacks ?? []) permissionCallbacks.registerPermissionCallback({ requestId, toolUseId: requestId, onAllow: (updatedInput, permissionUpdates) => { decisions.push({ requestId, decision: 'approved', updatedInput, permissionUpdates }) }, onReject: feedback => { decisions.push({ requestId, decision: 'rejected', feedback }) } })
  for (const message of row.messages) await mailbox.writeToMailbox(polledName, message, team)
  const state: any = getDefaultAppState()
  state.teamContext = { teamName: team, leadAgentId: 'team-lead@team', teammates: row.teammates }
  state.toolPermissionContext.mode = row.mode ?? 'default'
  const store = createStore(state)
  let permissionQueue: any[] = []
  bridge.registerLeaderToolUseConfirmQueue((update: any) => { permissionQueue = update(permissionQueue) })
  await poll({ current: false }, () => pollInbox(...bindingNames.map(name => hook.fixtureInboxBindings[name]), true, store, store.setState, true, undefined, async () => {}, () => false))
  if (row.resolvePermissions) for (const request of permissionQueue) request.onAllow(request.input, [])
  // Join the actual fire-and-forget filesystem producers through their physical
  // receipts, instead of assuming that a fixed delay settles Source writes.
  const expectedReplies = new Map<string, number>()
  for (const message of row.messages) if (mailbox.isPlanApprovalRequest(message.text)) expectedReplies.set(message.from, (expectedReplies.get(message.from) ?? 0) + 1)
  if (row.resolvePermissions) for (const request of permissionQueue) expectedReplies.set(request.workerBadge.name, (expectedReplies.get(request.workerBadge.name) ?? 0) + 1)
  const deadline = Date.now() + 5000
  while (true) {
    const incoming = await mailbox.readMailbox(polledName, team)
    const marked = incoming.every((message: any) => Boolean(message.read) === !(row.missingConfig && mailbox.isShutdownApproved(message.text)))
    const replied = (await Promise.all([...expectedReplies].map(async ([name, count]) => (await mailbox.readMailbox(name, team)).length === count))).every(Boolean)
    if (marked && replied) break
    if (Date.now() > deadline) throw new Error('Original inbox filesystem producers did not settle')
    await new Promise(resolve => setTimeout(resolve, 2))
  }
  const stateAfter: any = store.getState(), inboxes: any = {}
  for (const recipient of row.recipients ?? []) inboxes[recipient] = cleanMessages(await mailbox.readMailbox(recipient, team)).map(({ timestamp, ...message }: any) => ({ ...message, text: message.text.startsWith('{') ? JSON.stringify({ ...JSON.parse(message.text), timestamp: undefined }) : message.text }))
  const snapshot = await tasks.readTaskListSnapshot(team)
  let paneState: any
  if (input.realPaneBinary) {
    while (true) {
      const active = (await paneCommand(['list-panes', '-a', '-F', '#{pane_id}'])).split('\n')
      paneState = { trustedAlive: active.includes(trustedPane), victimAlive: active.includes(victimPane) }
      if (!paneState.trustedAlive) break
      if (Date.now() > deadline) { const debug = await import('../../../src/utils/debug'); await debug.flushDebugLogs(); throw new Error(`Actual leader poll never killed its trusted private pane: ${JSON.stringify(paneRuns)}\n${await Bun.file(process.env.CLAUDE_CODE_DEBUG_LOGS_DIR!).text()}`) }
      await new Promise(resolve => setTimeout(resolve, 2))
    }
  }
  rows.push({ regular: cleanMessages(stateAfter.inbox.messages).map(({ timestamp, ...message }) => message.from === 'system' ? message : { ...message, timestamp }), teammates: stateAfter.teamContext.teammates, members: (await helpers.readTeamFileAsync(team))?.members ?? null, tasks: snapshot.tasks, revision: snapshot.revision, mailbox: cleanMessages(await mailbox.readMailbox(polledName, team)), ...(row.worker ? { workerResult: { mode: stateAfter.toolPermissionContext.mode, rules: stateAfter.toolPermissionContext.alwaysAllowRules.session, decisions } } : {}), inboxes, permissions: permissionQueue.map(q => ({ toolName: q.tool.name, toolUseId: q.toolUseID, displayName: q.workerBadge.name, input: q.input })), ...(paneState ? { paneState } : {}) })
  bridge.unregisterLeaderToolUseConfirmQueue()
}
teammate.setDynamicTeamContext(undefined)
const inProcessContext = await import('../../../src/utils/teammateContext')
const deliveries = []
process.env.USER_TYPE = 'ant'
for (let index = 0; index < (input.deliveries ?? []).length; index++) {
  const row = input.deliveries[index], team = `actual-attachment-${index}`
  const polledName = row.inProcess?.agentName ?? row.worker?.agentName ?? 'team-lead'
  for (const message of row.messages) await mailbox.writeToMailbox(polledName, message, team)
  for (const message of row.viewedMessages ?? []) await mailbox.writeToMailbox(row.viewedTask.identity.agentName, message, team)
  let state: any = getDefaultAppState()
  state.teamContext = { teamName: team, leadAgentId: 'team-lead@team', teammates: { 'team-lead@team': { name: 'team-lead' } } }
  state.inbox.messages = row.pending
  if (row.viewedTask) { state.tasks[row.viewedTask.id] = row.viewedTask; state.viewingAgentTaskId = row.viewedTask.id }
  const context = { getAppState: () => state, setAppState: (update: any) => { state = update(state) } }
  const result = row.inProcess
    ? await inProcessContext.runWithTeammateContext(inProcessContext.createTeammateContext({ ...row.inProcess, teamName: team, abortController: new AbortController() }), () => attachments.getTeammateMailboxAttachments(context))
    : await attachments.getTeammateMailboxAttachments(context)
  const normalized = result.flatMap(normalizeAttachmentForAPI).map((message: any) => ({ content: message.message.content, isMeta: message.isMeta }))
  deliveries.push({ attachments: result, pending: state.inbox.messages, mailbox: cleanMessages(await mailbox.readMailbox(polledName, team)), normalized, ...(row.viewedTask ? { viewedMailbox: cleanMessages(await mailbox.readMailbox(row.viewedTask.identity.agentName, team)) } : {}) })
}
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ parsed, xml, regex: teammateUI.TEAMMATE_MSG_REGEX.source, rows, deliveries, capabilities }), () => resolve()))
if (input.realPaneBinary) await paneCommand(['kill-server'])
process.exit(0)
