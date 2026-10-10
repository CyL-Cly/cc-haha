import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Agent available tools')
if (process.env.CC_HAHA_NATIVE_AGENT_TOOLS_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Agent tools require disposable production profile')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: [...absentManagedSettingsFixturePaths(), '/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token'] })
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setCwdState(input.homeDir); state.setOriginalCwd(input.homeDir); state.setProjectRoot(input.homeDir)
state.setAllowedSettingSources(['userSettings']); state.setIsInteractive(false)
const config = await import('../../../src/utils/config'); config.enableConfigs()
const cache = await import('../../../src/utils/settings/settingsCache')
const { getAllBaseTools } = await import('../../../src/tools')
const { filterToolsForAgent } = await import('../../../src/tools/AgentTool/agentToolUtils')
const { getMainLoopModel } = await import('../../../src/utils/model/model')
const {getSettings_DEPRECATED}=await import('../../../src/utils/settings/settings')
const allowed = ['ANTHROPIC_MODEL','ANTHROPIC_BASE_URL','ANTHROPIC_DEFAULT_SONNET_MODEL','ANTHROPIC_DEFAULT_OPUS_MODEL','ANTHROPIC_DEFAULT_HAIKU_MODEL','ENABLE_TOOL_SEARCH','CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS','CLAUDE_CODE_ENABLE_TASKS','EMBEDDED_SEARCH_TOOLS','CLAUDE_CODE_ENTRYPOINT','CLAUDE_CODE_SIMPLE']
const rows = []
for (const row of input.cases) {
 for (const key of allowed) delete process.env[key]
 for (const [key,value] of Object.entries(row.env ?? {})) { if (!allowed.includes(key) || typeof value !== 'string') throw new Error('Unsafe Agent environment input');process.env[key]=value }
 mkdirSync(join(input.homeDir,'.claude'),{recursive:true})
 writeFileSync(join(input.homeDir,'.claude','settings.json'),JSON.stringify(row.settings ?? {}))
 cache.resetSettingsCache();state.resetModelStringsForTestingOnly();state.setMainLoopModelOverride(undefined)
 const enabled=getAllBaseTools().filter(tool => { try { return tool.isEnabled() } catch { return false } })
 rows.push({ model:getMainLoopModel(),settings:getSettings_DEPRECATED(),tools:filterToolsForAgent({tools:enabled,isBuiltIn:false,isAsync:true}).map(tool=>tool.name).sort((a,b)=>a.localeCompare(b)) })
}
process.stdout.write(JSON.stringify({rows,capabilities}))
