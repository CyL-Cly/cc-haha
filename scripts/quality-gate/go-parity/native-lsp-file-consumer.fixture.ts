import fs from 'node:fs'
import path from 'node:path'
import { mock, spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Original file-tool LSP notification consumer')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_LSP_FILE_SOURCE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Missing original LSP file consumer guard')
const work = path.join(home, 'work'), config = path.join(home, '.claude')
fs.mkdirSync(work, { recursive: true }); fs.mkdirSync(config, { recursive: true })
fs.writeFileSync(path.join(config, '.claude.json'), JSON.stringify({ hasCompletedOnboarding: true, fileCheckpointingEnabled: false }))
fs.writeFileSync(path.join(config, 'settings.json'), '{}')
const absentPaths = absentManagedSettingsFixturePaths()
for (let parent = path.dirname(home);; parent = path.dirname(parent)) {
  for (const name of ['.git', '.claude/skills', '.claude/commands', '.claude/workflows', '.agents/skills']) {
    const target = path.join(parent, name)
    if (fs.existsSync(target)) throw new Error('Source ancestor is not verified absent')
    absentPaths.push(target)
  }
  if (path.dirname(parent) === parent) break
}
const rg = path.join(path.dirname(process.execPath), process.platform === 'win32' ? 'rg.exe' : 'rg')
if (!fs.existsSync(rg)) absentPaths.push(rg)
const capabilities = await installClientFixtureCapabilities(home, { absentPaths })
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
process.chdir(work); state.setCwdState(work); state.setOriginalCwd(work); state.setProjectRoot(work); state.setIsInteractive(false)
state.setAllowedSettingSources(['userSettings'])
const configModule = await import('../../../src/utils/config'); configModule.enableConfigs()
const logging = await import('../../../src/utils/log')
const logged: string[] = []
const errorCalls: string[] = []
const originalLogError = logging.logError
spyOn(logging, 'logError').mockImplementation(error => {
  errorCalls.push(error instanceof Error ? error.message : String(error))
  originalLogError(error)
})
logging.attachErrorLogSink({ logError: error => logged.push(error.message), logMCPError: () => {}, logMCPDebug: () => {}, getErrorsPath: () => path.join(home, 'errors'), getMCPLogsPath: () => path.join(home, 'mcp-errors') })
const managerModule = await import('../../../src/services/lsp/manager')
let manager: any
mock.module('../../../src/services/lsp/manager', () => ({ ...managerModule, getLspServerManager: () => manager }))
const registry = await import('../../../src/services/lsp/LSPDiagnosticRegistry')
const originalClear = registry.clearDeliveredDiagnosticsForFile
let active: { calls: unknown[], context: any, file: string } | undefined
spyOn(registry, 'clearDeliveredDiagnosticsForFile').mockImplementation(uri => {
  active?.calls.push({ kind: 'clear', uri })
  originalClear(uri)
})
const { FileReadTool } = await import('../../../src/tools/FileReadTool/FileReadTool')
const { FileEditTool } = await import('../../../src/tools/FileEditTool/FileEditTool')
const { FileWriteTool } = await import('../../../src/tools/FileWriteTool/FileWriteTool')
const { getEmptyToolPermissionContext } = await import('../../../src/Tool')
const results = []
for (const row of input.rows) {
  if (!['Write', 'Edit'].includes(row.tool) || !['held', 'reject', 'missing'].includes(row.mode) || !/^[a-z-]+$/.test(row.name)) throw new Error('Undeclared original LSP file profile')
  const file = path.join(work, row.name + '.txt')
  fs.writeFileSync(file, row.old)
  let app = { toolPermissionContext: { ...getEmptyToolPermissionContext(), mode: 'default' } }
  const context: any = { getAppState: () => app, setAppState: (update: any) => { app = update(app) }, readFileState: new Map(), dynamicSkillDirTriggers: new Set(), abortController: new AbortController(), options: { tools: [FileReadTool, FileEditTool, FileWriteTool], agents: [], mainLoopModel: 'deepseek-flash' }, messages: [], userModified: true, nestedMemoryAttachmentTriggers: new Set() }
  const parent: any = { uuid: 'private-lsp-file-parent', message: { id: 'private-lsp-file-message' } }
  await FileReadTool.call(FileReadTool.inputSchema.parse({ file_path: file }), context, undefined, parent)
  const calls: unknown[] = [], releases: (() => void)[] = []
  const beforeLogs = logged.length
  const beforeErrorCalls = errorCalls.length
  active = { calls, context, file }
  const notification = (kind: string, changed?: string) => {
    calls.push({ kind, path: file, ...(changed !== undefined ? { content: changed } : {}), disk: fs.readFileSync(file, 'utf8'), cached: context.readFileState.get(file)?.content })
    return row.mode === 'reject' ? Promise.reject(new Error('private-' + kind + '-failure')) : new Promise<void>(resolve => releases.push(resolve))
  }
  manager = row.mode === 'missing' ? undefined : { changeFile: (_file: string, content: string) => notification('change', content), saveFile: (_file: string) => notification('save') }
  const tool = row.tool === 'Write' ? FileWriteTool : FileEditTool
  const args = row.tool === 'Write' ? { file_path: file, content: row.content } : { file_path: file, old_string: row.oldString, new_string: row.newString, replace_all: false }
  const result = await tool.call(tool.inputSchema.parse(args), context, undefined, parent)
  const returnedBeforeNotificationCompletion = releases.length === (row.mode === 'held' ? 2 : 0)
  const bytes = fs.readFileSync(file, 'utf8'), cached = context.readFileState.get(file)?.content
  for (const release of releases) release()
  await Promise.resolve()
  results.push({ calls, returnedBeforeNotificationCompletion, bytes, cached, errorCalls: errorCalls.slice(beforeErrorCalls), deliveredErrors: logged.slice(beforeLogs), result: result.data })
  active = undefined
}
if (capabilities.blocked.length || capabilities.secureReads.length) throw new Error('Source LSP file consumer escaped private primitives: ' + JSON.stringify(capabilities))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ results, capabilities }), error => error ? reject(error) : resolve()))
