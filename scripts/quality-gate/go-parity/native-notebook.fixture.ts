import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
import { join, resolve } from 'node:path'
import { writeFileSync, readFileSync, statSync, utimesSync } from 'node:fs'

type Step = { name: string; operation: 'parse' | 'validate' | 'call' | 'read'; inputSource: string; file?: string; contentBase64?: string; readBefore?: boolean; stale?: boolean; random?: number; skipValidation?: boolean }
const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as { homeDir: string; steps: Step[]; randomValues: number[] }
assertDisposableFixtureHome(input.homeDir, 'Native Notebook')
if (process.env.CC_HAHA_NATIVE_NOTEBOOK_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Native Notebook requires isolated production profile')
for (const step of input.steps) if (step.file && (step.file.includes('/') || step.file.includes('\\') || step.file.includes('\0') || step.file === '..')) throw new Error('Notebook fixture must remain inside disposable HOME')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
;(globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }).MACRO = { BUILD_TIME: '' }
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(input.homeDir)
bootstrap.setOriginalCwd(input.homeDir)
bootstrap.setProjectRoot(input.homeDir)
bootstrap.setIsInteractive(false)
bootstrap.setSdkBetas([])
bootstrap.switchSession('00000000-0000-4000-8000-000000000000' as Parameters<typeof bootstrap.switchSession>[0])
bootstrap.setInitialMainLoopModel('deepseek-flash')
const config = await import('../../../src/utils/config')
config.enableConfigs()
config.saveGlobalConfig(previous => ({ ...previous, userID: 'fixture-device' }))
const { NotebookEditTool } = await import('../../../src/tools/NotebookEditTool/NotebookEditTool')
const { FileReadTool } = await import('../../../src/tools/FileReadTool/FileReadTool')
const { toolToAPISchema } = await import('../../../src/utils/api')
const { getEmptyToolPermissionContext } = await import('../../../src/Tool')
const schema = await toolToAPISchema(NotebookEditTool, { tools: [NotebookEditTool, FileReadTool], agents: [], model: 'deepseek-flash', getToolPermissionContext: async () => getEmptyToolPermissionContext() })
const observations: unknown[] = []
for (const step of input.steps) {
  const tool = step.operation === 'read' ? FileReadTool : NotebookEditTool
  const readFileState = new Map<string, any>()
  const path = step.file && join(input.homeDir, step.file)
  if (path && step.contentBase64 !== undefined) writeFileSync(path, Buffer.from(step.contentBase64, 'base64'), { mode: 0o640 })
  if (path && step.readBefore) readFileState.set(path, { content: 'original read', timestamp: Math.floor(statSync(path).mtimeMs), offset: 1 })
  if (path && step.stale) {
    const at = new Date(Date.now() + 2000)
    utimesSync(path, at, at)
  }
  const beforeBase64 = path ? readFileSync(path).toString('base64') : undefined
  const parsed = tool.inputSchema.safeParse(JSON.parse(step.inputSource))
  if (!parsed.success) {
    observations.push({ step, observation: { success: false, issuesSource: JSON.stringify(parsed.error.issues), beforeBase64 } })
    continue
  }
  if (step.operation === 'parse') {
    observations.push({ step, observation: { success: true, dataSource: JSON.stringify(parsed.data), beforeBase64 } })
    continue
  }
  const context: any = { readFileState, abortController: new AbortController(), getAppState: () => ({ toolPermissionContext: getEmptyToolPermissionContext() }) }
  const originalRandom = Math.random
  let randomCalls = 0
  if (step.random !== undefined) Math.random = () => { randomCalls++; return step.random! }
  try {
    const validation = step.operation === 'read' || step.skipValidation ? undefined : await NotebookEditTool.validateInput(parsed.data as any, context)
    if (step.operation === 'validate' || validation?.result === false) {
      observations.push({ step, observation: { success: true, dataSource: JSON.stringify(parsed.data), validationSource: JSON.stringify(validation), beforeBase64, afterBase64: path ? readFileSync(path).toString('base64') : undefined, randomCalls } })
      continue
    }
    const result = await (tool.call as any)(parsed.data, context, undefined, { uuid: 'original-notebook-parent', message: { id: 'original-parent-response' } })
    const mapped = (tool.mapToolResultToToolResultBlockParam as any)(result.data, 'actual-notebook-tool-use-id')
    const actualPath = path ?? resolve(input.homeDir, (parsed.data as any).notebook_path ?? (parsed.data as any).file_path)
    const cached = readFileState.get(actualPath)
    const cache = cached && { content: cached.content, offset: cached.offset, limit: cached.limit, partialView: cached.partialView, timestampMatchesFile: cached.timestamp === Math.floor(statSync(actualPath).mtimeMs) }
    observations.push({ step, observation: { success: true, dataSource: JSON.stringify(parsed.data), validationSource: validation && JSON.stringify(validation), resultSource: JSON.stringify(result.data), mappedSource: JSON.stringify(mapped), cacheSource: JSON.stringify(cache) ?? null, beforeBase64, afterBase64: path ? readFileSync(path).toString('base64') : undefined, randomCalls } })
  } catch (error) {
    observations.push({ step, observation: { success: true, dataSource: JSON.stringify(parsed.data), error: (error as Error).message, beforeBase64, afterBase64: path ? readFileSync(path).toString('base64') : undefined, randomCalls } })
  } finally { Math.random = originalRandom }
}
process.stdout.write(JSON.stringify({ schemaSource: JSON.stringify(schema), enabled: NotebookEditTool.isEnabled(), concurrencySafe: NotebookEditTool.isConcurrencySafe({} as any), randoms: input.randomValues.map(value => ({ value, cellID: value.toString(36).substring(2, 15) })), observations, sourceOnly: { consumer: 'actual NotebookEditTool inputSchema/validateInput/call/map/toolToAPISchema + FileReadTool notebook call + notebook.ts', runtime: process.version, capabilities } }))
