import fs from 'node:fs'
import path from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual plugin LSP process and file consumer')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_LSP_RUNTIME_SOURCE !== '1') throw new Error('Pinned original LSP runtime guard required')
for (const target of [input.cwd, input.peer, input.receipt, input.file]) {
  const canonical = fs.existsSync(target) ? fs.realpathSync(target) : path.join(fs.realpathSync(path.dirname(target)), path.basename(target))
  if (path.relative(fs.realpathSync(home), canonical).startsWith('..')) throw new Error('LSP runtime path escaped private home')
}
const absentPaths = absentManagedSettingsFixturePaths()
const managedSkills = path.join(path.dirname(absentPaths[0]!), '.claude', 'skills')
if (!fs.existsSync(managedSkills)) absentPaths.push(managedSkills)
for (const spelling of new Set([home, fs.realpathSync(home)])) for (let parent = path.dirname(spelling);; parent = path.dirname(parent)) {
  for (const name of ['.git', '.claude/settings.json', '.claude/settings.local.json', '.claude/agents', '.agents/agents', '.claude/skills', '.claude/commands', '.claude/workflows', '.agents/skills', '.mcp.json']) {
    const target = path.join(parent, name)
    if (!fs.existsSync(target)) absentPaths.push(target)
  }
  if (path.dirname(parent) === parent) break
}
const rg = path.join(path.dirname(process.execPath), process.platform === 'win32' ? 'rg.exe' : 'rg')
if (!fs.existsSync(rg)) absentPaths.push(rg)
const capabilities = await installClientFixtureCapabilities(home, { absentPaths, privateExecutableFiles: [input.peer, ...(input.git ? [input.git] : [])], approvedSpawn: request => (request.command === input.peer && JSON.stringify(request.args) === JSON.stringify([input.receipt, home]) && request.options.cwd === input.cwd) || ((request.command === "git" || request.command === "/usr/bin/git") && request.args[0] === "check-ignore" && request.args.slice(1).every(file => path.isAbsolute(file) && !path.relative(fs.realpathSync(home), fs.realpathSync(file)).startsWith("..")) && request.options.cwd === input.cwd) })
const nativeKills: Array<{pid: number, signal: string}> = []
if (input.observeShutdown) {
  const { ChildProcess } = await import('node:child_process')
  const kill = ChildProcess.prototype.kill
  ChildProcess.prototype.kill = function(signal?: NodeJS.Signals | number) {
    nativeKills.push({ pid: this.pid!, signal: String(signal ?? 'SIGTERM') })
    return kill.call(this, signal)
  }
}
;(globalThis as any).MACRO = { VERSION: '999.0.0-lsp-runtime', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setCwdState(input.cwd); state.setOriginalCwd(input.cwd); state.setProjectRoot(input.cwd); state.setIsInteractive(false)
const config = await import('../../../src/utils/config'); config.enableConfigs()
const module = await import('../../../src/services/lsp/manager')
module.initializeLspServerManager(); await module.waitForInitialization()
const manager = module.getLspServerManager()
if (!manager || manager.getAllServers().size !== 1) throw new Error('Actual installed plugin did not produce exactly one LSP server: ' + JSON.stringify(module.getInitializationStatus()))
const initial = [...manager.getAllServers()].map(([name, server]) => ({ name, state: server.state, config: server.config }))
if (fs.existsSync(input.receipt)) throw new Error('Original LSP server spawned during declaration loading')
if (input.hot) await manager.openFile(input.file, fs.readFileSync(input.file, 'utf8'))
const { FileReadTool } = await import('../../../src/tools/FileReadTool/FileReadTool')
const { FileWriteTool } = await import('../../../src/tools/FileWriteTool/FileWriteTool')
const { getEmptyToolPermissionContext } = await import('../../../src/Tool')
let app = { toolPermissionContext: { ...getEmptyToolPermissionContext(), mode: 'default' } }
const context: any = { getAppState: () => app, setAppState: (update: any) => { app = update(app) }, readFileState: new Map(), dynamicSkillDirTriggers: new Set(), abortController: new AbortController(), options: { tools: [FileReadTool, FileWriteTool], agents: [], mainLoopModel: 'deepseek-flash' }, messages: [], userModified: true, nestedMemoryAttachmentTriggers: new Set() }
if (input.diagnosticFiles) {
  const { BashTool } = await import('../../../src/tools/BashTool/BashTool')
  const { getAttachments } = await import('../../../src/utils/attachments')
  const { normalizeAttachmentForAPI } = await import('../../../src/utils/messages')
  const registry = await import('../../../src/services/lsp/LSPDiagnosticRegistry')
  Object.assign(app, { mcp: { commands: [] }, tasks: {} })
  Object.assign(context.options, { mcpClients: [], agentDefinitions: { activeAgents: [] } })
  for (const file of input.diagnosticFiles) await manager.openFile(file, fs.readFileSync(file, 'utf8'))
  await manager.sendRequest(input.file, '$/fixture/barrier', {})
  const collect = async (bash: boolean, agent?: string) => {
    context.agentId = agent
    context.options.tools = bash ? [BashTool] : []
    const payloads = (await getAttachments(null, context, null, [], [], 'repl_main_thread')).filter(a => a.type === 'diagnostics')
    return { payloads, normalized: payloads.flatMap(a => normalizeAttachmentForAPI(a)).map(m => ({ content: m.message.content, isMeta: m.isMeta })) }
  }
  const observations = [await collect(false), await collect(true, 'actual-child'), await collect(true)]
  await manager.changeFile(input.file, 'same diagnostic again')
  await manager.sendRequest(input.file, '$/fixture/barrier', {})
  observations.push(await collect(true))
  registry.clearDeliveredDiagnosticsForFile('file://' + input.file)
  await manager.changeFile(input.file, 'literal URI clear retains actual path-key dedup')
  await manager.sendRequest(input.file, '$/fixture/barrier', {})
  observations.push(await collect(true))
  registry.resetAllLSPDiagnosticState()
  await manager.changeFile(input.file, 'actual reset permits diagnostics')
  await manager.sendRequest(input.file, '$/fixture/barrier', {})
  observations.push(await collect(true))
  await module.shutdownLspServerManager()
  if (capabilities.blocked.length || capabilities.secureReads.some(value => value !== 'read' && value !== 'readAsync')) throw new Error('Original diagnostic consumer escaped private primitives: ' + JSON.stringify(capabilities))
  process.stdout.write(JSON.stringify({ observations, capabilities }))
  process.exit(0)
}
if (input.toolCalls) {
  const { LSPTool } = await import('../../../src/tools/LSPTool/LSPTool')
  const { getAllBaseTools, getTools } = await import('../../../src/tools')
  const { z } = await import('zod/v4')
  const catalog = { declared: getAllBaseTools().some(t => t.name === 'LSP'), enabled: getTools(app.toolPermissionContext).some(t => t.name === 'LSP') }
  const results = []
  for (const request of input.toolCalls) {
    const parsed = LSPTool.inputSchema.parse(request)
    const validation = await LSPTool.validateInput!(parsed, context)
    if (!validation.result) throw new Error('Original LSP input rejected: ' + JSON.stringify(validation))
    const permission = await LSPTool.checkPermissions(parsed, context)
    const result = await LSPTool.call(parsed, context, undefined, undefined as any)
    results.push({ data: result.data, mapped: LSPTool.mapToolResultToToolResultBlockParam(result.data, 'actual-lsp-tool-id'), permission })
  }
  const permissionResults = []
  if (input.permissionRows) {
    const { hasPermissionsToUseTool } = await import('../../../src/utils/permissions/permissions')
    for (const row of input.permissionRows) {
      app.toolPermissionContext = { ...getEmptyToolPermissionContext(), mode: row.mode, alwaysAllowRules: { session: row.allow }, alwaysDenyRules: { session: row.deny }, alwaysAskRules: { session: row.ask }, isBypassPermissionsModeAvailable: true }
      const decision = await hasPermissionsToUseTool(LSPTool, row.input, context, { message: { id: 'actual-lsp-parent' } } as any, 'actual-lsp-permission-tool')
      permissionResults.push(decision)
    }
  }
  const parses = (input.schemaInputs ?? []).map((value: unknown) => {
    const parsed = LSPTool.inputSchema.safeParse(value)
    return parsed.success ? { success: true, data: parsed.data } : { success: false, issues: parsed.error.issues }
  })
  await manager.sendRequest(input.file, '$/fixture/barrier', {})
  const frames = fs.readFileSync(input.receipt, 'utf8').trim().split('\n').map(line => JSON.parse(line))
  await module.shutdownLspServerManager()
  if (capabilities.blocked.length || JSON.stringify(capabilities.secureReads) !== JSON.stringify(["read"])) throw new Error('Original LSP tool escaped fixture: ' + JSON.stringify(capabilities))
  if (input.observeShutdown) {
    // Observe the controlled peer's real SIGTERM receipt after original stop
    // settled. This wait is outside the business call and cannot affect it.
    await new Promise<void>(resolve => {
      const observed = () => {
        const rows = fs.readFileSync(input.receipt, 'utf8').trim().split('\n').map(line => JSON.parse(line))
        if (rows.some(row => row.method === '$/fixture/terminated')) return resolve()
        setImmediate(observed)
      }
      observed()
    })
  }
  const finalFrames = input.observeShutdown ? fs.readFileSync(input.receipt, 'utf8').trim().split('\n').map(line => JSON.parse(line)) : undefined
  process.stdout.write(JSON.stringify({ results, permissionResults, parses, frames, finalFrames, nativeKills, catalog, schema: z.toJSONSchema(LSPTool.inputSchema), prompt: await LSPTool.prompt({} as any), capabilities }))
  process.exit(0)
}
const parent: any = { uuid: 'lsp-original-file-parent', message: { id: 'lsp-original-message' } }
await FileReadTool.call(FileReadTool.inputSchema.parse({ file_path: input.file }), context, undefined, parent)
await FileWriteTool.call(FileWriteTool.inputSchema.parse({ file_path: input.file, content: input.content }), context, undefined, parent)
// Observe the original state rather than replacing its first-use asynchronous
// start with a fixture callback. No timer changes the business scheduling.
await new Promise<void>((resolve, reject) => {
  const check = () => {
    const server = [...manager.getAllServers().values()][0]!
    if (server.state === 'error') return reject(server.lastError)
    if (server.state === 'running' && manager.isFileOpen(input.file)) return resolve()
    setImmediate(check)
  }
  check()
})
await manager.sendRequest(input.file, '$/fixture/barrier', {})
const frames = fs.readFileSync(input.receipt, 'utf8').trim().split('\n').map(line => JSON.parse(line))
await module.shutdownLspServerManager()
if (capabilities.blocked.length || capabilities.secureReads.length) throw new Error('Original LSP runtime escaped fixture: ' + JSON.stringify(capabilities))
process.stdout.write(JSON.stringify({ initial, frames, capabilities }))
process.exit(0)
