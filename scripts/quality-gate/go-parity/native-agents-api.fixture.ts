import { assertDisposableFixtureHome } from './fixture-guard'
import { existsSync, mkdirSync, readFileSync, readdirSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { mock } from 'bun:test'

const home = assertDisposableFixtureHome(process.env.HOME, 'Agents API Source comparison')
if (process.env.CC_HAHA_AGENTS_FIXTURE !== '1') throw new Error('Missing agents fixture marker')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const config = join(home, '.claude')
const workspace = join(home, 'workspace')
mkdirSync(config, { recursive: true })
mkdirSync(workspace, { recursive: true })
mkdirSync(join(home, 'managed'), { recursive: true })
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = join(home, 'managed')
// External Source builds deliberately ignore the test env override. Replace
// only the path producer so real file-based managed policy remains private.
mock.module('../../../src/utils/settings/managedPath', () => ({
  getManagedFilePath: () => join(home, 'managed'),
  getManagedSettingsDropInDir: () => join(home, 'managed', 'managed-settings.d'),
}))
writeFileSync(join(config, '.claude.json'), '{}')
writeFileSync(join(config, 'settings.json'), JSON.stringify(input.settings ?? {}))
mkdirSync(join(workspace, '.git'), { recursive: true })
process.env.CLAUDE_CODE_USE_NATIVE_FILE_SEARCH = '1'
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setCwdState(workspace)
state.setOriginalCwd(workspace)
state.setProjectRoot(workspace)
state.setIsInteractive(false)
const { getBuiltInAgentsWithoutOverrides } = await import('../../../src/tools/AgentTool/builtInAgents')
const observations = []
const expand = (value: string) => value.replaceAll('$HOME', home).replaceAll('$WORKSPACE', workspace).replaceAll('$CONFIG', config)
function snapshot() {
  const files: Record<string, string> = {}
  const walk = (directory: string) => {
    if (!existsSync(directory)) return
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      const path = join(directory, entry.name)
      if (entry.isDirectory()) walk(path)
      else if (entry.isFile() && entry.name.endsWith('.md')) files[path.replace(home, '$HOME')] = readFileSync(path, 'utf8')
    }
  }
  walk(home)
  return files
}
for (const row of input.cases) {
  if (row.action === 'stock') {
    observations.push(getBuiltInAgentsWithoutOverrides().map(agent => ({
      fields: Object.fromEntries(Object.entries(agent).filter(([, value]) => typeof value !== 'function')),
      systemPrompt: agent.getSystemPrompt.length === 0 ? agent.getSystemPrompt() : agent.getSystemPrompt({
        toolUseContext: { options: { commands: [], agentDefinitions: { activeAgents: [] }, mcpClients: [] } },
      } as any),
      promptArity: agent.getSystemPrompt.length,
    })))
  } else if (row.action === 'guide') {
    const { CLAUDE_CODE_GUIDE_AGENT } = await import('../../../src/tools/AgentTool/built-in/claudeCodeGuideAgent')
    const { getSettings_DEPRECATED } = await import('../../../src/utils/settings/settings')
    if (row.using3P) process.env.CLAUDE_CODE_USE_BEDROCK = '1'
    else delete process.env.CLAUDE_CODE_USE_BEDROCK
    Object.assign(MACRO, { ISSUES_EXPLAINER: row.issuesExplainer ?? '' })
    const prompt = CLAUDE_CODE_GUIDE_AGENT.getSystemPrompt({
      toolUseContext: { options: { commands: row.commands ?? [], agentDefinitions: { activeAgents: row.agents ?? [] }, mcpClients: row.mcpServers ?? [] } },
    } as any)
    observations.push({ prompt, settings: getSettings_DEPRECATED() })
  } else if (row.action === 'concurrentCreate') {
    const { AgentService } = await import('../../../src/server/services/agentService')
    const service = new AgentService()
    const results = await Promise.allSettled(Array.from({ length: row.count }, () => service.createAgent(row.agent, { scope: 'user' })))
    observations.push({ successes: results.filter(result => result.status === 'fulfilled').length, failures: results.filter(result => result.status === 'rejected').map(result => ({
      status: (result as PromiseRejectedResult).reason.statusCode,
      code: (result as PromiseRejectedResult).reason.code,
      message: (result as PromiseRejectedResult).reason.message,
    })), files: snapshot() })
  } else if (row.action === 'api') {
    if (row.reset) {
      rmSync(join(config, 'agents'), { recursive: true, force: true })
      rmSync(join(workspace, '.claude'), { recursive: true, force: true })
      writeFileSync(join(config, 'settings.json'), JSON.stringify(row.settings ?? {}))
    }
    for (const file of row.files ?? []) {
      const path = expand(file.path)
      if (!path.startsWith(home + '/')) throw new Error('Unsafe agents fixture path')
      mkdirSync(dirname(path), { recursive: true })
      if (file.symlink) symlinkSync(expand(file.symlink), path)
      else writeFileSync(path, expand(file.content))
    }
    const { resetSettingsCache } = await import('../../../src/utils/settings/settingsCache')
    const { clearAgentDefinitionsCache } = await import('../../../src/tools/AgentTool/loadAgentsDir')
    resetSettingsCache()
    if (row.clear) clearAgentDefinitionsCache()
    const { handleAgentsApi } = await import('../../../src/server/api/agents')
    const url = new URL(expand(row.path), 'http://127.0.0.1:3000')
    const raw = row.raw === undefined ? row.body === undefined ? undefined : JSON.stringify(row.body, (_key, value) => typeof value === 'string' ? expand(value) : value) : expand(row.raw)
    const response = await handleAgentsApi(new Request(url, { method: row.method, headers: { 'Content-Type': 'application/json' }, body: raw }), url, url.pathname.split('/').filter(Boolean))
    const body = JSON.parse(JSON.stringify(await response.json()).replaceAll(home, '$HOME'))
    observations.push({ status: response.status, body, files: snapshot(), settings: JSON.parse(readFileSync(join(config, 'settings.json'), 'utf8')) })
  } else throw new Error('Unknown agents fixture action')
}
process.stdout.write(JSON.stringify(observations))
