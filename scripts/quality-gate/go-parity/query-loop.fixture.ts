import { spyOn } from 'bun:test'
import * as crypto from 'node:crypto'
import { readFile, writeFile } from 'node:fs/promises'
import { existsSync, readFileSync, realpathSync } from 'node:fs'
import { dirname, join } from 'node:path'
import type { QueryLoopInput, QueryLoopObservation } from './query-loop-fixtures'
import type { Tool, ToolUseContext } from '../../../src/Tool'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as QueryLoopInput
assertDisposableFixtureHome(input.home, 'Query loop')
const allowed = new Set(['name', 'nonInteractive', 'backfill', 'permission', 'resultContentSource', 'wires', 'historySources', 'systemSources', 'definition', 'maxOutputTokens', 'home', 'origin', 'runtime', 'deviceID', 'sessionID', 'identityTrace', 'deprecatedCatalog', 'refreshTools'])
if (Object.keys(input).some(key => !allowed.has(key)) || process.env.CC_HAHA_QUERY_LOOP_FIXTURE !== '1' || process.env.NODE_ENV !== 'production' || typeof input.origin !== 'string' || !input.origin || input.origin !== process.env.ANTHROPIC_BASE_URL || typeof input.nonInteractive !== 'boolean' || !Array.isArray(input.wires) || input.wires.length !== 2 || !Array.isArray(input.historySources) || !Array.isArray(input.systemSources) || !['add', 'overwrite', 'none'].includes(input.backfill) || !['allow', 'replace-path', 'replace-content'].includes(input.permission) || input.maxOutputTokens !== 32000 || input.deviceID !== 'fixture-query-device' || input.sessionID !== '00000000-0000-4000-8000-000000000000') throw new Error('Query loop requires explicit offline production profile')
if (input.refreshTools !== undefined && (input.refreshTools !== 'replace-after-tools' || input.nonInteractive)) throw new Error('Query tool refresh requires the original interactive caller')
if (input.runtime.platform !== process.platform || input.runtime.arch !== process.arch || input.runtime.version !== process.version) throw new Error('Query loop requires pinned Source runtime identity')
if (typeof input.resultContentSource !== 'string') throw new Error('Query loop requires original tool result mapping')
const resultContent = JSON.parse(input.resultContentSource)
const definition = input.definition
if (definition?.kind !== 'registered' || !['FixtureWrite','�'].includes(definition.name) || definition.inputSchema?.kind !== 'object' || definition.inputSchema.unknownKeys !== 'strip' || definition.concurrencySafe !== false || definition.readOnly !== false || typeof definition.prompt !== 'string' || typeof definition.description !== 'string' || !Array.isArray(definition.inputSchema.fields) || definition.inputSchema.fields.length !== 2 || definition.inputSchema.fields.some((field, index) => field.name !== ['file_path', 'content'][index] || field.schema?.kind !== 'string')) throw new Error('Query loop requires an original registered string/object tool definition')
const absent = absentManagedSettingsFixturePaths()
if (input.refreshTools) {
  for (const path of ['/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token']) {
    if (existsSync(path)) throw new Error('Actual query refresh remote credential path is not absent')
    absent.push(path)
  }
  for (const spelling of new Set([input.home, realpathSync(input.home)])) {
    for (let directory = spelling; ; directory = dirname(directory)) {
      const path = join(directory, '.git')
      if (existsSync(path)) throw new Error('Actual query refresh ancestor is not absent')
      absent.push(path)
      if (directory === dirname(directory)) break
    }
  }
}
const capabilities = await installClientFixtureCapabilities(input.home, { absentPaths: absent, loopbackFetchOrigin: input.origin })
;(globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }).MACRO = { BUILD_TIME: '' }
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(input.home)
bootstrap.setOriginalCwd(input.home)
bootstrap.setProjectRoot(input.home)
bootstrap.switchSession(input.sessionID as Parameters<typeof bootstrap.switchSession>[0])
bootstrap.setIsInteractive(!input.nonInteractive)
bootstrap.setSdkBetas([])
bootstrap.setPromptCache1hEligible(false)
bootstrap.setPromptCache1hAllowlist([])
const { enableConfigs } = await import('../../../src/utils/config')
enableConfigs()
const { query } = await import('../../../src/query')
const { productionDeps } = await import('../../../src/query/deps')
const { buildQueryConfig } = await import('../../../src/query/config')
const { getDefaultAppState } = await import('../../../src/state/AppStateStore')
const { asSystemPrompt } = await import('../../../src/utils/systemPromptType')
const { z } = await import('zod')
const { getAllBaseTools } = await import('../../../src/tools')
if (JSON.stringify(getAllBaseTools().map(tool => ({name:tool.name,aliases:tool.aliases ?? []}))) !== JSON.stringify(input.deprecatedCatalog)) throw new Error('Unresolved Source deprecated registry snapshot: '+JSON.stringify(getAllBaseTools().map(tool => ({name:tool.name,aliases:tool.aliases ?? []}))))
const querySource = input.nonInteractive ? 'sdk' : 'repl_main_thread'
const config = buildQueryConfig()
if (config.gates.streamingToolExecution) throw new Error('Query loop oracle requires observed nonstreaming tool execution snapshot')
const observation: QueryLoopObservation = { profile: { nonInteractive: input.nonInteractive, querySource, streamingToolExecution: config.gates.streamingToolExecution }, modelHistory: [], history: [], requests: [], yielded: [], yieldedAfter: [], effects: [], allocations: [], terminal: null, error: null, executions: 0, file: null }
let uuidCount = 0
spyOn(crypto, 'randomUUID').mockImplementation(() => {
  const value = `fixture-id-${++uuidCount}`
  observation.allocations.push({ kind: 'uuid', value })
  return value as ReturnType<typeof crypto.randomUUID>
})
const OriginalDate = Date
globalThis.Date = class extends OriginalDate {
  constructor(...args: unknown[]) {
    super(...(args.length ? args : [1_700_000_000_000]) as [number])
    if (!args.length) observation.allocations.push({ kind: 'time', value: this.toISOString() })
  }
  static now() { return 1_700_000_000_000 }
} as DateConstructor
const tool = {
  name: definition.name, aliases: definition.aliases, inputSchema: z.object(Object.fromEntries(definition.inputSchema.fields.map(field => [field.name, z.string()]))),
  prompt: async () => { observation.effects.push({ kind: 'prompt', value: JSON.stringify(definition.prompt) }); return definition.prompt }, description: async () => definition.description,
  maxResultSizeChars: 1000, isConcurrencySafe: () => false, isReadOnly: () => false, isEnabled: () => true, userFacingName: () => 'fixture write',
  ...(input.backfill === 'none' ? {} : { backfillObservableInput(value: Record<string, unknown>) {
    observation.effects.push({ kind: 'backfill', value: JSON.stringify(value) })
    if (input.backfill === 'add') value.legacy_view = 'shown'
    else value.file_path = 'effect-overwritten.txt'
  } }),
  call: async (value: { file_path: string; content: string }, context: ToolUseContext & {toolUseId:string}) => {
    if (input.identityTrace) observation.effects.push({kind:'context-ID',value:JSON.stringify(context.toolUseId)})
    observation.executions++
    observation.effects.push({ kind: 'tool', value: JSON.stringify(value) })
    if (value.file_path !== (input.permission === 'replace-path' ? 'effect-permission.txt' : 'effect.txt') || !input.identityTrace && observation.executions !== 1) throw new Error('Fixture write received unexpected path or duplicate execution')
    await writeFile(join(input.home, value.file_path), input.identityTrace ? JSON.stringify(observation.effects.filter(effect => effect.kind === 'context-ID').map(effect => JSON.parse(effect.value))) : value.content)
    return { data: 'written' }
  },
  mapToolResultToToolResultBlockParam: (_data: string, id: string) => { if(input.identityTrace) observation.effects.push({kind:'mapped-ID',value:JSON.stringify(id)}); return { type: 'tool_result', tool_use_id: id, content: resultContent } },
} as unknown as Tool
let state = getDefaultAppState()
const context = {
  options: { ...(input.refreshTools ? { refreshTools: () => { observation.effects.push({ kind: 'refresh-tools', value: readFileSync(join(input.home, 'effect.txt'), 'utf8') }); return [{ ...tool, name: 'FixtureAfterRefresh', aliases: [] }] } } : {}), commands: [], debug: false, mainLoopModel: 'fixture-model', tools: [tool], verbose: false, thinkingConfig: { type: 'disabled' }, mcpClients: [], mcpResources: {}, isNonInteractiveSession: input.nonInteractive, agentDefinitions: { activeAgents: [], allAgents: [] } },
  abortController: new AbortController(), readFileState: new Map(),
  getAppState: () => state, setAppState: (update: (value: typeof state) => typeof state) => { state = update(state) },
  setInProgressToolUseIDs: (update: (ids: Set<string>) => Set<string>) => { inProgress = update(inProgress); observation.effects.push({ kind: 'in-progress', value: JSON.stringify([...inProgress]) }) },
  setResponseLength: () => {}, updateFileHistoryState: () => {}, updateAttributionState: () => {}, messages: [],
} as unknown as ToolUseContext
let inProgress = new Set<string>()
const deps = productionDeps()
const originalModel = deps.callModel
let resolvedHistory: unknown[] = []
let assistants: unknown[] = []
// Instrument the existing dependency seam; execute its real production body.
// Compaction, tools, hooks, permissions and query() itself are not mocked.
deps.callModel = async function* (request) {
  resolvedHistory = request.messages
  assistants = []
  observation.modelHistory.push(request.messages.map(message => JSON.stringify(message)))
  for await (const message of originalModel({ ...request, options: { ...request.options, fetchOverride: async (url, init) => {
    const parsed = new URL(String(url))
    if (parsed.origin !== input.origin || init?.method !== 'POST') throw new Error('Query loop escaped exact loopback POST')
    observation.requests.push({ path: parsed.pathname + parsed.search, method: init.method, headers: [...new Headers(init.headers)], body: String(init.body) })
    return fetch(url, init)
  } } })) {
    if (message.type === 'assistant') assistants.push(message)
    yield message
  }
}
const yielded: unknown[] = []
try {
  const generator = query({ messages: input.historySources.map(source => JSON.parse(source)), systemPrompt: asSystemPrompt(input.systemSources.map(source => JSON.parse(source))), userContext: {}, systemContext: {},
    canUseTool: async (_tool, value) => {
      observation.effects.push({ kind: 'permission', value: JSON.stringify(value) })
      return { behavior: 'allow', ...(input.permission === 'replace-path' ? { updatedInput: { ...value, file_path: 'effect-permission.txt' } } : input.permission === 'replace-content' ? { updatedInput: { ...value, content: 'permission content' } } : {}) }
    },
    toolUseContext: context, querySource, maxTurns: 2, maxOutputTokensOverride: input.maxOutputTokens, deps,
  })
  while (true) {
    const next = await generator.next()
    if (next.done) { observation.terminal = next.value; break }
    yielded.push(next.value)
    observation.yielded.push(JSON.stringify(next.value))
  }
} catch (failure) {
  if (!(failure instanceof Error)) throw failure
  observation.error = { name: failure.name, message: failure.message }
} finally { context.abortController.abort() }
observation.yieldedAfter = yielded.map(message => JSON.stringify(message))
observation.history = [...resolvedHistory, ...assistants].map(message => JSON.stringify(message))
try { observation.file = await readFile(join(input.home, input.permission === 'replace-path' ? 'effect-permission.txt' : 'effect.txt'), 'utf8') } catch (failure) { if ((failure as NodeJS.ErrnoException).code !== 'ENOENT') throw failure }
process.stdout.write(JSON.stringify({ observationSource: JSON.stringify(observation), capabilities }))
