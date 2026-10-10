import { spyOn } from 'bun:test'
import * as crypto from 'node:crypto'
import { readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import type { Tool, ToolUseContext } from '../../../src/Tool'
import type { ToolIdentityInput, ToolIdentityObservation } from './query-loop-identity-fixtures'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as ToolIdentityInput
const scalarString = (value: unknown): value is string => typeof value === 'string' && [...value].every(character => { const point = character.codePointAt(0)!; return point < 0xd800 || point > 0xdfff })
assertDisposableFixtureHome(input.home, 'Tool identity')
if (process.env.CC_HAHA_TOOL_IDENTITY_FIXTURE !== '1' || process.env.NODE_ENV !== 'production' || !scalarString(input.registry?.name) || !Array.isArray(input.registry.aliases) || input.registry.aliases.some(value => !scalarString(value)) || !Array.isArray(input.usesSources) || !input.usesSources.length || input.usesSources.some(value => typeof value !== 'string') || Object.keys(input).some(key => !['name', 'registry', 'usesSources', 'deprecatedCatalog', 'aborted', 'home'].includes(key))) throw new Error('Tool identity requires explicit offline registry/model inputs')
const capabilities = await installClientFixtureCapabilities(input.home, { absentPaths: absentManagedSettingsFixturePaths() })
;(globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }).MACRO = { BUILD_TIME: '' }
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(input.home)
bootstrap.setOriginalCwd(input.home)
bootstrap.setProjectRoot(input.home)
bootstrap.switchSession('00000000-0000-4000-8000-000000000000' as Parameters<typeof bootstrap.switchSession>[0])
bootstrap.setIsInteractive(false)
const { enableConfigs } = await import('../../../src/utils/config')
enableConfigs()
const { findToolByName } = await import('../../../src/Tool')
const { runTools } = await import('../../../src/services/tools/toolOrchestration')
const { getAllBaseTools } = await import('../../../src/tools')
if (JSON.stringify(getAllBaseTools().map(tool => ({name:tool.name,aliases:tool.aliases ?? []}))) !== JSON.stringify(input.deprecatedCatalog)) throw new Error('Unresolved Source deprecated registry snapshot')
const { getDefaultAppState } = await import('../../../src/state/AppStateStore')
const { z } = await import('zod')
let uuid = 0
spyOn(crypto, 'randomUUID').mockImplementation(() => `identity-${++uuid}` as ReturnType<typeof crypto.randomUUID>)
const OriginalDate = Date
globalThis.Date = class extends OriginalDate {
  constructor(...args: unknown[]) { super(...(args.length ? args : [1_700_000_000_000]) as [number]) }
  static now() { return 1_700_000_000_000 }
} as DateConstructor
const observation: ToolIdentityObservation = { lookupNames: [], calls: [], mapped: [], file: null, unsupported: false }
const feedbackSources: string[] = [], progress: string[] = []
let backfills = 0
const tool = {
  name: input.registry.name, aliases: input.registry.aliases,
  inputSchema: z.object({ file_path: z.string(), content: z.string() }),
  prompt: async () => 'Original identity test tool', description: async () => 'Original identity test tool',
  maxResultSizeChars: 1000, isConcurrencySafe: () => false, isReadOnly: () => false, isEnabled: () => true, userFacingName: () => 'identity',
  backfillObservableInput() { backfills++ },
  call: async (_value: unknown, context: ToolUseContext & { toolUseId: string }) => {
    observation.calls.push(JSON.stringify(context.toolUseId))
    await writeFile(join(input.home, 'identity.txt'), JSON.stringify(observation.calls))
    return { data: 'written' }
  },
  mapToolResultToToolResultBlockParam: (data: string, id: string) => { observation.mapped.push(JSON.stringify(id)); return { type: 'tool_result', tool_use_id: id, content: data } },
} as unknown as Tool
const uses = input.usesSources.map(source => JSON.parse(source))
observation.lookupNames = uses.map(use => findToolByName([tool], use.name)?.name ?? null)
let state = getDefaultAppState(), inProgress = new Set<string>()
const context = {
  options: { commands: [], debug: false, mainLoopModel: 'fixture-model', tools: [tool], verbose: false, thinkingConfig: { type: 'disabled' }, mcpClients: [], mcpResources: {}, isNonInteractiveSession: true, agentDefinitions: { activeAgents: [], allAgents: [] } },
  abortController: new AbortController(), readFileState: new Map(),
  getAppState: () => state, setAppState: (update: (value: typeof state) => typeof state) => { state = update(state) },
  setInProgressToolUseIDs: (update: (ids: Set<string>) => Set<string>) => { inProgress = update(inProgress); progress.push(JSON.stringify([...inProgress])) },
  setResponseLength: () => {}, updateFileHistoryState: () => {}, updateAttributionState: () => {}, messages: [],
} as unknown as ToolUseContext
const assistant = { type: 'assistant', uuid: 'original-assistant', timestamp: '2023-11-14T22:13:20.000Z', message: { id: 'original-model', type: 'message', role: 'assistant', model: 'fixture-model', content: uses, usage: { input_tokens: 10, output_tokens: 1 }, stop_reason: 'end_turn', stop_sequence: null } }
if (input.aborted) context.abortController.abort()
try {
  for await (const update of runTools(uses, [assistant as Parameters<typeof runTools>[1][number]], async () => ({ behavior: 'allow' }), context)) if (update.message) feedbackSources.push(JSON.stringify(update.message))
} finally { context.abortController.abort() }
try { observation.file = await readFile(join(input.home, 'identity.txt'), 'utf8') } catch (failure) { if ((failure as NodeJS.ErrnoException).code !== 'ENOENT') throw failure }
process.stdout.write(JSON.stringify({ observation, sourceOnly: { feedbackSources, progress, backfills, deprecatedCatalog: getAllBaseTools().map(tool => ({ name: tool.name, aliases: tool.aliases ?? [] })) }, capabilities }))
