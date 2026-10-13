import { parse } from 'acorn'
import { assertDisposableFixtureHome } from './fixture-guard'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Original Doctor context')
function extract(source: string, names?: string[]) {
  const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
  return ast.body.flatMap((n: any) => n.type === 'ExportNamedDeclaration' ? [n.declaration] : [n]).filter((n: any) => n && ((n.type === 'FunctionDeclaration' && (!names || names.includes(n.id.name))) || (n.type === 'VariableDeclaration' && n.declarations.some((d: any) => !names || names.includes(d.id.name))))).map((n: any) => source.slice(n.start, n.end)).join('\n')
}
async function source(url: string) { return new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL(url, import.meta.url)).text()) }
const warning = extract(await source('../../../src/utils/doctorContextWarnings.ts'))
const shadow = extract(await source('../../../src/utils/permissions/shadowedRuleDetection.ts'))
const permissions = extract(await source('../../../src/utils/permissions/permissions.ts'), ['getAllowRules', 'getDenyRules', 'getAskRules', 'permissionRuleSourceDisplayString'])
const parser = extract(await source('../../../src/utils/permissions/permissionRuleParser.ts'), ['permissionRuleValueFromString', 'permissionRuleValueToString', 'normalizeLegacyToolName', 'escapeRuleContent', 'unescapeRuleContent', 'findFirstUnescapedChar', 'findLastUnescapedChar'])
const names = extract(await source('../../../src/utils/settings/constants.ts'), ['SETTING_SOURCES', 'getSettingSourceDisplayNameLowercase'])
const agents = extract(await source('../../../src/utils/statusNoticeHelpers.ts'))
const memory = extract(await source('../../../src/utils/claudemd.ts'), ['getLargeMemoryFiles'])
const rough = extract(await source('../../../src/services/tokenEstimation.ts'), ['roughTokenCountEstimation'])
const plural = extract(await source('../../../src/utils/stringUtils.ts'), ['plural'])
const mcpCount = extract(await source('../../../src/utils/analyzeContext.ts'), ['countMcpToolTokens']).replace(/await import\(['"]\.\/toolSearch\.js['"]\)/, '__nativeToolSearch').replace(/await import\(['"]\.\.\/tools\/ToolSearchTool\/prompt\.js['"]\)/, '__nativeDeferredModule')
const deferred = extract(await source('../../../src/tools/ToolSearchTool/prompt.ts'), ['isDeferredTool'])
const results = []
for (const row of input.cases) {
  const tokenCalls: any[] = []
  const tools = (row.tools ?? []).map((t: any) => ({ ...t, isMcp: true, prompt: async () => t.description, inputJSONSchema: t.schema ?? {} }))
  const deps: Record<string, any> = { getMemoryFiles: async () => row.memory ?? [], MAX_MEMORY_CHARACTER_COUNT: 40000, BASH_TOOL_NAME: 'Bash', LEGACY_TOOL_NAME_ALIASES: { Task: 'Agent', KillShell: 'TaskStop' }, SandboxManager: { isSandboxingEnabled: () => row.sandbox ?? false, isAutoAllowBashIfSandboxedEnabled: () => row.sandboxAutoAllow ?? true }, getMainLoopModel: () => 'deepseek-flash', TOOL_TOKEN_COUNT_OVERHEAD: 500, __nativeToolSearch: { isToolSearchEnabled: async () => row.deferred ?? false }, __nativeDeferredModule: new Function(deferred + ';return {isDeferredTool}')(), jsonStringify: JSON.stringify, countToolDefinitionTokens: async (all: any[]) => { tokenCalls.push(all.map((t: any) => t.name)); if (row.countError) throw new Error('actual native token counter failure'); return row.tokens ?? 0 } }
  const fn = new Function(...Object.keys(deps), names + ';const PERMISSION_RULE_SOURCES=[...SETTING_SOURCES,"cliArg","command","session"];' + parser + permissions + shadow + agents + memory + rough + plural + mcpCount + warning + ';return checkContextWarnings')(...Object.values(deps))
  const result = await fn(tools, row.agents ? { activeAgents: row.agents } : null, async () => row.permission ?? { alwaysAllowRules: {}, alwaysAskRules: {}, alwaysDenyRules: {} })
  results.push({ result, tokenCalls })
}
process.stdout.write(JSON.stringify(results))
