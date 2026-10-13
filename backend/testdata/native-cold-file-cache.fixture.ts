import { readFileSync, mkdirSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../scripts/quality-gate/go-parity/client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Cold restored native file cache')
if (process.env.CC_HAHA_COLD_FILE_CACHE_FIXTURE !== '1') throw new Error('private fixture only')
const root = resolve(import.meta.dir, '../..')
const work = join(home, 'work')
mkdirSync(work, { recursive: true, mode: 0o700 })
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
globalThis.MACRO = { VERSION: 'fixture', BUILD_TIME: '' } as any
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = join(home, 'managed')
await import(`${root}/src/utils/settings/managedPath.ts`)
process.env.USER_TYPE = 'external'
const state = await import(`${root}/src/bootstrap/state.ts`)
process.chdir(work)
state.setOriginalCwd(process.cwd()); state.setCwdState(process.cwd()); state.setProjectRoot(process.cwd())
state.setAllowedSettingSources(['userSettings'])
;(await import(`${root}/src/utils/config.ts`)).enableConfigs()
const read = (await import(`${root}/src/tools/FileReadTool/FileReadTool.ts`)).FileReadTool
const write = (await import(`${root}/src/tools/FileWriteTool/FileWriteTool.ts`)).FileWriteTool
const cache = await import(`${root}/src/utils/fileStateCache.ts`)
const query = await import(`${root}/src/utils/queryHelpers.ts`)
const tools = [read, write]
let app: any = { toolPermissionContext: { ...(await import(`${root}/src/Tool.ts`)).getEmptyToolPermissionContext(), mode: 'bypassPermissions' } }
const context: any = { abortController: new AbortController(), options: { tools, commands: [], agents: [], mainLoopModel: 'fixture-model', isNonInteractiveSession: true }, getAppState: () => app, setAppState: (update: any) => { app = update(app) }, readFileState: cache.createFileStateCacheWithSizeLimit(100), dynamicSkillDirTriggers: new Set(), messages: [] }
const path = join(work, 'cold-cache.txt')
const { writeFileSync } = await import('node:fs')
writeFileSync(path, 'before\n')
const readInput = read.inputSchema.parse({ file_path: path })
const readResult = await read.call(readInput, context, async () => ({ behavior: 'allow', updatedInput: readInput }), { uuid: 'actual-warm', message: { id: 'actual-warm-message' } } as any)
const block = read.mapToolResultToToolResultBlockParam(readResult.data, 'actual-warm-call')
const timestamp = new Date().toISOString()
const history: any[] = [
  { type: 'assistant', uuid: 'actual-warm', message: { role: 'assistant', content: [{ type: 'tool_use', id: 'actual-warm-call', name: 'Read', input: readInput }] } },
  { type: 'user', uuid: 'actual-result', timestamp, message: { role: 'user', content: [block] } },
]
// print.ts performs this extraction at cold startup. The real Write consumer
// receives its result as readFileState; do not return a fabricated tool result.
context.readFileState = query.extractReadFilesFromMessages(history, work, 100)
const restored = context.readFileState.get(path)
const writeInput = write.inputSchema.parse({ file_path: path, content: 'RESTORED_CACHE_WRITE_OK\n' })
const validation = await write.validateInput(writeInput, context)
const writeResult = await write.call(writeInput, context, async () => ({ behavior: 'allow', updatedInput: writeInput }), { uuid: 'actual-cold', message: { id: 'actual-cold-message' } } as any)
console.log(JSON.stringify({ restored: { content: restored?.content, validTimestamp: Number.isFinite(restored?.timestamp) }, validation, resultType: writeResult.data.type, physical: readFileSync(path, 'utf8'), capabilities, actualReadCalls: 1, actualWriteCalls: 1 }))
