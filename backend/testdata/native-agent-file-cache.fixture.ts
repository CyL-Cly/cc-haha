import { mkdirSync, writeFileSync, readFileSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../scripts/quality-gate/go-parity/client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual Agent fork cache')
if (process.env.CC_HAHA_AGENT_FILE_CACHE_FIXTURE !== '1') throw new Error('private fixture only')
const root = resolve(import.meta.dir, '../..')
const work = join(home, 'work'); mkdirSync(work, { recursive: true, mode: 0o700 })
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import(`${root}/preload.ts`)
globalThis.MACRO = { VERSION: 'fixture', BUILD_TIME: '' } as any
const state = await import(`${root}/src/bootstrap/state.ts`)
process.chdir(work); state.setCwdState(process.cwd()); state.setOriginalCwd(process.cwd()); state.setProjectRoot(process.cwd()); state.setIsInteractive(false)
;(await import(`${root}/src/utils/config.ts`)).enableConfigs()
const { getDefaultAppState } = await import(`${root}/src/state/AppStateStore.ts`)
const { createFileStateCacheWithSizeLimit } = await import(`${root}/src/utils/fileStateCache.ts`)
const { asSystemPrompt } = await import(`${root}/src/utils/systemPromptType.ts`)
const { runAgent } = await import(`${root}/src/tools/AgentTool/runAgent.ts`)
const read = (await import(`${root}/src/tools/FileReadTool/FileReadTool.ts`)).FileReadTool
const write = (await import(`${root}/src/tools/FileWriteTool/FileWriteTool.ts`)).FileWriteTool
const path = join(work, 'parent-read-shared.txt'); writeFileSync(path, 'PARENT_READ_BEFORE\n')
let parent = getDefaultAppState(); parent.toolPermissionContext.mode = 'bypassPermissions'
const definition: any = { agentType: 'general-purpose', whenToUse: 'private actual fixture', getSystemPrompt: () => 'fixture body', tools: ['*'], source: 'built-in' }
const context: any = { options: { commands: [], debug: false, mainLoopModel: 'fixture-model', tools: [read, write], verbose: false, thinkingConfig: { type: 'disabled' }, mcpClients: [], mcpResources: {}, isNonInteractiveSession: true, agentDefinitions: { activeAgents: [definition], allAgents: [definition] } }, abortController: new AbortController(), readFileState: createFileStateCacheWithSizeLimit(100), dynamicSkillDirTriggers: new Set(), getAppState: () => parent, setAppState: (update: any) => { parent = update(parent) }, setResponseLength: () => {}, messages: [] }
const readInput = read.inputSchema.parse({ file_path: path })
await read.call(readInput, context, async () => ({ behavior: 'allow', updatedInput: readInput }), { uuid: 'actual-parent', message: { id: 'parent-msg' } } as any)
const rows = []
for (const hasContext of [false, true]) {
  let captured: any
  const stop = new Error('actual runAgent context ready, before model')
  const generator = runAgent({ agentDefinition: definition, promptMessages: [], toolUseContext: context, canUseTool: (async () => ({ behavior: 'allow' })) as any, isAsync: true, querySource: 'agent:custom', forkContextMessages: hasContext ? [] : undefined, override: { userContext: {}, systemContext: {}, systemPrompt: asSystemPrompt([]), agentId: 'fixture-cache' as any }, availableTools: [read, write], onCacheSafeParams: (params: any) => { captured = params.toolUseContext; throw stop } })
  try { await generator.next(); throw new Error('actual context was not reached') } catch (error) { if (error !== stop) throw error }
  const childCache = captured.readFileState
  const writeInput = write.inputSchema.parse({ file_path: path, content: 'SECOND_PARENT_CACHE_WRITE_OK\n' })
  const validation = await write.validateInput(writeInput, captured)
  if (validation.result) await write.call(writeInput, captured, async () => ({ behavior: 'allow', updatedInput: writeInput }), { uuid: 'actual-child', message: { id: 'child-msg' } } as any)
  rows.push({ forkContextDefined: hasContext, sameCacheObject: childCache === context.readFileState, validation, bytes: readFileSync(path, 'utf8'), parentContent: context.readFileState.get(path)?.content })
}
console.log(JSON.stringify({ rows, capabilities }))
