import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.homeDir, 'Native foreground shell')
if (process.env.CC_HAHA_NATIVE_FOREGROUND_SHELL_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Unowned foreground shell fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setOriginalCwd(input.cwd); state.setCwdState(input.cwd); state.setIsInteractive(false)
state.switchSession('11111111-1111-4111-8111-111111111111')
const { TaskOutput } = await import('../../../src/utils/task/TaskOutput')
const { getTaskOutputPath } = await import('../../../src/utils/task/diskOutput')
const { wrapSpawn } = await import('../../../src/utils/ShellCommand')
const { EventEmitter } = await import('events')
const { mkdir, writeFile } = await import('fs/promises')
const { dirname } = await import('path')
const shell = await import('../../../src/tasks/LocalShellTask/LocalShellTask')
const local = await import('../../../src/tasks/LocalAgentTask/LocalAgentTask')
const sdk = await import('../../../src/utils/sdkEventQueue')
const queue = await import('../../../src/utils/messageQueueManager')
const samples = await Promise.all((input.samples ?? []).map(async (row: any) => {
 const file = getTaskOutputPath(row.id)
 await mkdir(dirname(file), { recursive: true })
 await writeFile(file, Buffer.from(row.base64, 'base64'))
 let resolve: (value: any) => void
 const observed = new Promise(r => { resolve = r })
 const output = new TaskOutput(row.id, (last, full, lines, bytes) => { TaskOutput.stopPolling(row.id); resolve({ last, full, lines, bytes }) }, true)
 TaskOutput.startPolling(row.id)
 const result = await observed
 output.clear()
 return result
}))
const strip = ({ shellCommand, abortController, unregisterCleanup, ...data }: any) => data
const originalNow = Date.now
const states: any[] = []
for (const row of input.states ?? []) {
 Date.now = () => row.now
 let current: any = { tasks: {}, speculation: { status: 'inactive' } }
 const set = (f: any) => { current = f(current) }
 sdk.drainSdkEvents(); queue.clearCommandQueue()
 // The child process is an external EventEmitter primitive; all shell
 // background/exit/listener/TaskOutput logic here is the actual Source class.
 const child: any = new EventEmitter()
 child.stdout = null; child.stderr = null
 const output = new TaskOutput(row.id, null, true)
 const path = output.path
 await mkdir(dirname(path), { recursive: true }); await writeFile(path, 'actual Source before/after output')
 const command = wrapSpawn(child, new AbortController().signal, 10000, output)
 const agent = local.registerAgentForeground({ agentId: row.agentId, description: 'original foreground Agent', prompt: 'original prompt', selectedAgent: { agentType: 'general-purpose' }, setAppState: set })
 const id = shell.registerForeground({ command: row.command, description: row.description, shellCommand: command }, set, row.toolUseId)
 const snapshot = () => ({ tasks: Object.fromEntries(Object.entries(current.tasks).map(([id, task]) => [id, strip(task)])), has: shell.hasForegroundTasks(current), events: sdk.drainSdkEvents().map(({ uuid, session_id, ...event }: any) => event) })
 const initial = snapshot()
 shell.backgroundAll(() => current, set)
 await Promise.resolve()
 const promoted = snapshot()
 const duplicate = shell.backgroundExistingForegroundTask(id, command, row.description, set, row.toolUseId)
 child.emit('exit', row.code)
 await command.result
 // Await the real background result handler's TaskOutput/flush promise chain.
 for (let i = 0; i < 40 && current.tasks[id].status === 'running'; i++) await new Promise(resolve => setTimeout(resolve, 1))
 const completed = snapshot()
 states.push({ initial, promoted, duplicate, completed, shellStatus: command.status })
 agent.cancelAutoBackground?.()
 local.unregisterAgentForeground(row.agentId, set)
}
Date.now = originalNow
const { parse } = await import('acorn')
const sourceCode = new Bun.Transpiler({ loader: 'tsx' }).transformSync(await Bun.file(new URL('../../../src/tools/BashTool/BashTool.tsx', import.meta.url)).text())
const sourceAST = parse(sourceCode, { ecmaVersion: 'latest', sourceType: 'module' })
let persistBody: string | undefined
let sizeExpression: string | undefined
function findPersistence(node: any) {
 if (!node || typeof node !== 'object') return
 if (node.type === 'IfStatement' && sourceCode.slice(node.test.start, node.test.end) === 'result.outputFilePath && result.outputTaskId') persistBody = sourceCode.slice(node.start, node.end)
 if (node.type === 'VariableDeclarator' && node.id?.name === 'MAX_PERSISTED_SIZE') sizeExpression = sourceCode.slice(node.init.start, node.init.end)
 for (const value of Object.values(node)) { if (Array.isArray(value)) value.forEach(findPersistence); else if (value && typeof value === 'object') findPersistence(value) }
}
findPersistence(sourceAST)
if (!persistBody || !sizeExpression) throw new Error('Original Bash physical persistence block missing')
const { stat, truncate, link, copyFile, open } = await import('fs/promises')
const storage = await import('../../../src/utils/toolResultStorage')
const runPersistence = new Function('result', 'fsStat', 'ensureToolResultsDir', 'getToolResultPath', 'fsTruncate', 'link', 'copyFile', `return (async () => { const MAX_PERSISTED_SIZE = ${sizeExpression}; let persistedOutputPath; let persistedOutputSize; ${persistBody}; return { persistedOutputPath, persistedOutputSize } })()`)
const persisted: any[] = []
for (const row of input.persists ?? []) {
 const path = getTaskOutputPath(row.id)
 await mkdir(dirname(path), { recursive: true })
 const handle = await open(path, 'w')
 await handle.write(Buffer.from(row.base64 ?? '', 'base64'))
 if (row.size !== undefined) await handle.truncate(row.size)
 await handle.close()
 if (row.existing) { await storage.ensureToolResultsDir(); await writeFile(storage.getToolResultPath(row.id, false), 'original prior destination') }
 const result = await runPersistence({ outputFilePath: path, outputTaskId: row.id }, stat, storage.ensureToolResultsDir, storage.getToolResultPath, truncate, link, copyFile)
 const sourceStat = await stat(path)
 const destStat = result.persistedOutputPath ? await stat(result.persistedOutputPath) : undefined
 persisted.push({ ...result, sourceSize: sourceStat.size, destSize: destStat?.size, sameFile: sourceStat.ino === destStat?.ino })
}
const { splitCommand_DEPRECATED } = await import('../../../src/utils/bash/commands')
const autoNode: any = (sourceAST as any).body.find((node: any) => node.type === 'FunctionDeclaration' && node.id?.name === 'isAutobackgroundingAllowed')
if (!autoNode) throw new Error('Original auto-background predicate unavailable')
const autoAllowed = new Function('splitCommand_DEPRECATED', 'DISALLOWED_AUTO_BACKGROUND_COMMANDS', `${sourceCode.slice(autoNode.start, autoNode.end)}; return isAutobackgroundingAllowed`)(splitCommand_DEPRECATED, ['sleep'])
const auto = (input.autoCommands ?? []).map((command: string) => autoAllowed(command))
const timeouts = await import('../../../src/utils/timeouts')
const timeoutRows = (input.timeoutRows ?? []).map((env: any) => ({ defaultMs: timeouts.getDefaultBashTimeoutMs(env), maxMs: timeouts.getMaxBashTimeoutMs(env) }))
const { splitCommandWithOperators } = await import('../../../src/utils/bash/commands')
const silentNode: any = (sourceAST as any).body.find((node: any) => node.type === 'FunctionDeclaration' && node.id?.name === 'isSilentBashCommand')
const setExpression = (name: string) => {
 const declaration: any = (sourceAST as any).body.flatMap((node: any) => node.type === 'VariableDeclaration' ? node.declarations : []).find((node: any) => node.id?.name === name)
 if (!declaration) throw new Error(`Actual Bash set ${name} missing`)
 return sourceCode.slice(declaration.init.start, declaration.init.end)
}
if (!silentNode) throw new Error('Actual Bash silent predicate missing')
const silent = new Function('splitCommandWithOperators', `const BASH_SILENT_COMMANDS = ${setExpression('BASH_SILENT_COMMANDS')}; const BASH_SEMANTIC_NEUTRAL_COMMANDS = ${setExpression('BASH_SEMANTIC_NEUTRAL_COMMANDS')}; ${sourceCode.slice(silentNode.start, silentNode.end)}; return isSilentBashCommand`)(splitCommandWithOperators)
const silentCommands = (input.silentCommands ?? []).map((command: string) => silent(command))
const { BashTool } = await import('../../../src/tools/BashTool/BashTool')
const largeOutputs = (input.largeOutputs ?? []).map((row: any) => BashTool.mapToolResultToToolResultBlockParam(row, 'original-large-bash'))
process.stdout.write(JSON.stringify({ samples, states, largeOutputs, persisted, auto, timeoutRows, silentCommands, capabilities: { blocked: capabilities.blocked } }) + '\n')
