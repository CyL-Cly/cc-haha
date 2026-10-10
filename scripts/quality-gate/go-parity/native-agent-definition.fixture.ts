import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Native Agent definitions')
if (process.env.CC_HAHA_NATIVE_AGENT_DEFINITION_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Agent definition requires disposable production configuration')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
;(globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }).MACRO = { BUILD_TIME: '' }
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(input.homeDir)
bootstrap.setOriginalCwd(input.homeDir)
bootstrap.setProjectRoot(input.homeDir)
bootstrap.setIsInteractive(false)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const { parseAgentFromMarkdown, parseAgentFromJson } = await import('../../../src/tools/AgentTool/loadAgentsDir')
const { resolveAgentTools } = await import('../../../src/tools/AgentTool/agentToolUtils')
const definitions = []
for (const item of input.cases ?? []) {
  process.env.CLAUDE_CODE_DISABLE_AUTO_MEMORY = item.memoryEnabled ? '0' : '1'
  const definition = item.json ? parseAgentFromJson(item.name, JSON.parse(item.json), item.source) : parseAgentFromMarkdown(item.filePath, item.baseDir, JSON.parse(item.frontmatter), item.body, item.source, item.hash)
  if (!definition) { definitions.push(null); continue }
  const { getSystemPrompt, ...fields } = definition
  definitions.push({ fields: JSON.stringify(fields), ...(item.prompt && { prompt: getSystemPrompt({ toolUseContext: { options: {} } } as any) }) })
}
const resolved = (input.resolve ?? []).map((item: any) => {
  const tools = item.available.map((name: string) => ({ name, aliases: [] }))
  const result = resolveAgentTools(item.definition, tools as any, item.async ?? false, item.main ?? false)
  return { ...result, resolvedTools: result.resolvedTools.map(tool => tool.name) }
})
process.stdout.write(JSON.stringify({ definitions, resolved, capabilities }))
