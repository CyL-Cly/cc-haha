import { spyOn } from 'bun:test'
import type { ToolSchemaCase, SchemaDefinition, ToolDefinition } from './tool-schema-fixtures'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from './client-options-capabilities'
import { requestContextWire } from './request-context-fixtures'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as ToolSchemaCase & { homeDir: string; probe?: string }
assertDisposableFixtureHome(input.homeDir, 'Tool schema')
if (process.env.CC_HAHA_TOOL_SCHEMA_FIXTURE !== '1' || process.env.NODE_ENV !== 'production' || process.env.CLAUDE_CODE_SIMPLE !== '1') throw new Error('Tool schema fixture requires private production profile')
if (Object.hasOwn(input, 'env')) throw new Error('Tool schema fixture cannot override sandbox environment')
const server = Bun.serve({ hostname: '127.0.0.1', port: 0, fetch() { return new Response(requestContextWire, { headers: { 'content-type': 'text/event-stream', 'request-id': 'schema-request', connection: 'keep-alive' } }) } })
const capabilities = await installClientFixtureCapabilities(input.homeDir, { loopbackFetchOrigin: server.url.origin, absentPaths: absentManagedSettingsFixturePaths() })
try {
  if (input.probe === 'network') await fetch('https://fixture.invalid')
  if (input.probe === 'native') Bun.spawn(['security'])
  process.env.ANTHROPIC_BASE_URL = server.url.origin
  ;(globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }).MACRO = { BUILD_TIME: '' }
  const bootstrap = await import('../../../src/bootstrap/state')
  bootstrap.setCwdState(input.homeDir)
  bootstrap.setOriginalCwd(input.homeDir)
  bootstrap.setProjectRoot(input.homeDir)
  bootstrap.switchSession('00000000-0000-4000-8000-000000000000' as Parameters<typeof bootstrap.switchSession>[0])
  bootstrap.setInitialMainLoopModel('deepseek-flash')
  const config = await import('../../../src/utils/config')
  config.enableConfigs()
  config.saveGlobalConfig(previous => ({ ...previous, userID: 'fixture-device' }))
  const growthbook = await import('../../../src/services/analytics/growthbook')
  let host: NonNullable<ToolSchemaCase['steps'][number]['host']> = {}
  const getFeature = growthbook.getFeatureValue_CACHED_MAY_BE_STALE
  spyOn(growthbook, 'getFeatureValue_CACHED_MAY_BE_STALE').mockImplementation(((name: string, fallback: unknown) => name === 'tengu_compact_line_prefix_killswitch' ? host.compactLinePrefixKilled ?? false : name === 'tengu_amber_wren' ? host.readLimits ?? {} : name === 'tengu_fgts' ? host.fgtsEnabled ?? false : getFeature(name, fallback)) as typeof getFeature)
  const checkGate = growthbook.checkStatsigFeatureGate_CACHED_MAY_BE_STALE
  spyOn(growthbook, 'checkStatsigFeatureGate_CACHED_MAY_BE_STALE').mockImplementation(name => name === 'tengu_tool_pear' ? host.strictEnabled ?? false : checkGate(name))
  const { z } = await import('zod/v4')
  const { FileReadTool } = await import('../../../src/tools/FileReadTool/FileReadTool')
  const { FileEditTool } = await import('../../../src/tools/FileEditTool/FileEditTool')
  const { FileWriteTool } = await import('../../../src/tools/FileWriteTool/FileWriteTool')
  const { GlobTool } = await import('../../../src/tools/GlobTool/GlobTool')
  const { GrepTool } = await import('../../../src/tools/GrepTool/GrepTool')
  const { toolToAPISchema } = await import('../../../src/utils/api')
  const { clearToolSchemaCache, getToolSchemaCache } = await import('../../../src/utils/toolSchemaCache')
  const { getEmptyToolPermissionContext } = await import('../../../src/Tool')
  const { VERSION: sdkVersion } = await import('@anthropic-ai/sdk/version')
  const native = { Read: FileReadTool, Edit: FileEditTool, Write: FileWriteTool, Glob: GlobTool, Grep: GrepTool }
  const promptCalls: Record<string, number> = {}
  const wrapped = new WeakSet<object>()
  const schema = (declaration: SchemaDefinition): any => {
    let value: any
    if (declaration.kind === 'object') {
      const fields = Object.fromEntries((declaration.fields ?? []).map(field => [field.name, schema(field.schema)]))
      value = declaration.unknownKeys === 'strict' ? z.strictObject(fields) : declaration.unknownKeys === 'passthrough' ? z.looseObject(fields) : z.object(fields)
    } else if (declaration.kind === 'string') value = z.string()
    else if (declaration.kind === 'boolean') value = z.boolean()
    else if (declaration.kind === 'number') { value = z.number(); if (declaration.integer) value = value.int(); if (declaration.minimum !== undefined) value = value.min(declaration.minimum); if (declaration.exclusiveMinimum !== undefined) value = value.gt(declaration.exclusiveMinimum) }
    else if (declaration.kind === 'enum') value = z.enum(declaration.values)
    else throw new Error('Unsupported original schema declaration')
    if (Object.hasOwn(declaration, 'default')) value = value.default(declaration.default)
    if (declaration.optional) value = value.optional()
    if (declaration.description !== undefined) value = value.describe(declaration.description)
    return value
  }
  const instantiate = (definition: ToolDefinition): any => {
    const tool = definition.kind === 'builtin' ? native[definition.name as keyof typeof native] : { name: definition.name, aliases: definition.aliases, strict: definition.strict, inputSchema: schema(definition.inputSchema ?? { kind: 'object', fields: [], unknownKeys: 'strip' }), ...(definition.inputJSONSchema !== undefined ? { inputJSONSchema: definition.inputJSONSchema } : {}), prompt: async () => definition.prompt, description: async () => definition.description, isConcurrencySafe: () => definition.concurrencySafe ?? false, isReadOnly: () => definition.readOnly ?? false }
    if (!tool) throw new Error('Unknown actual Source tool')
    if (!wrapped.has(tool)) { wrapped.add(tool); const original = tool.prompt; spyOn(tool, 'prompt').mockImplementation(async (options: unknown) => { promptCalls[tool.name] = (promptCalls[tool.name] ?? 0) + 1; return original.call(tool, options) }) }
    return tool
  }
  const records: unknown[] = []
  const fetches: unknown[] = []
  for (const current of input.steps) {
    host = current.host ?? {}
    if (input.mode === 'request' && (host.firstParty || host.baseURL !== undefined)) throw new Error('first-party/explicit URL schema profile cannot send requests')
    process.env.ANTHROPIC_BASE_URL = host.baseURL ?? (host.firstParty ? 'https://api.anthropic.com' : server.url.origin + (input.proxy ? '/proxy/providers/fixture' : ''))
    if (host.killSwitch) process.env.CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS = 'true'; else delete process.env.CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS
    bootstrap.setInitialMainLoopModel(host.mainLoopModel ?? 'deepseek-flash')
    bootstrap.setMainLoopModelOverride(host.mainLoopModel ?? 'deepseek-flash')
    if (current.clearCache) clearToolSchemaCache()
    const tools = current.tools.map(instantiate)
    if (input.mode === 'request') {
      const { queryModelWithStreaming } = await import('../../../src/services/api/claude')
      const { asSystemPrompt } = await import('../../../src/utils/systemPromptType')
      for await (const _ of queryModelWithStreaming({ messages: (input.historySources ?? ['{"type":"user","uuid":"original","message":{"role":"user","content":"question"}}']).map(source => JSON.parse(source)), systemPrompt: asSystemPrompt([]), thinkingConfig: { type: 'disabled' }, tools, signal: new AbortController().signal, options: { model: current.model ?? 'deepseek-flash', querySource: 'sdk', isNonInteractiveSession: true, agents: [], mcpTools: [], extraToolSchemas: input.extraToolSchemas as any, getToolPermissionContext: async () => getEmptyToolPermissionContext(), fetchOverride: async (url, init) => { const parsed = new URL(String(url)); fetches.push({ path: parsed.pathname + parsed.search, headers: [...new Headers(init?.headers)], body: init?.body }); return fetch(url, init) } } })) {}
    } else {
      const schemas = await Promise.all(tools.map(tool => toolToAPISchema(tool, { tools, agents: [], model: current.model, deferLoading: current.deferLoading, cacheControl: current.cacheControl as any, getToolPermissionContext: async () => getEmptyToolPermissionContext() })))
      const parsed = (current.parseInputs ?? []).map(value => { const result = tools.find(tool => tool.name === value.name).inputSchema.safeParse(value.input); return result.success ? { success: true, dataSource: JSON.stringify(result.data) } : { success: false, issuesSource: JSON.stringify(result.error.issues) } })
      records.push({ schemas: schemas.map(value => JSON.stringify(value)), promptCalls: { ...promptCalls }, cacheSize: getToolSchemaCache().size, parsed })
    }
  }
  process.stdout.write(JSON.stringify({ observation: { records, fetches }, sourceOnly: { capabilities, sdk: sdkVersion, runtime: { platform: process.platform, arch: process.arch, version: process.version }, profile: 'actual-native-tool-definitions' } }))
} finally { server.stop(true) }
