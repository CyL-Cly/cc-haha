import { dirname, join } from 'node:path'
import { mkdir, realpath, writeFile } from 'node:fs/promises'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'Native context analyser')
if (process.env.CC_HAHA_CONTEXT_ANALYSER_FIXTURE !== '1') throw new Error('Unowned context analyser fixture')
const absent = [...absentManagedSettingsFixturePaths(), join(dirname(process.execPath), 'rg'), '/usr/bin/rg', '/bin/rg']
for (const base of [home, await realpath(home)]) {
  for (let parent = dirname(base); ; parent = dirname(parent)) {
    for (const relative of ['.git', '.claude/skills', '.claude/commands', '.agents/skills', '.agents/commands']) absent.push(join(parent, relative))
    if (parent === dirname(parent)) break
  }
}
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absent, ...(input.countURL ? { loopbackFetchOrigin: new URL(input.countURL).origin } : {}) })
await mkdir(join(home, '.claude'), { recursive: true })
await writeFile(join(home, '.claude', 'settings.json'), '{}')
await import('../../../preload')
process.chdir(home)
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = join(home, 'policy')
const settings = await import('../../../src/utils/settings/settings')
settings.getInitialSettings()
process.env.USER_TYPE = 'external'
for (const [key, value] of Object.entries(input.environment)) process.env[key] = String(value)
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home)
state.setOriginalCwd(home)
state.setProjectRoot(home)
state.setIsInteractive(false)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const { analyzeContextUsage } = await import('../../../src/utils/analyzeContext')
const { getMessagesAfterCompactBoundary } = await import('../../../src/utils/messages')
const { getEmptyToolPermissionContext } = await import('../../../src/Tool')
const { getTools } = await import('../../../src/tools')
const { roughTokenCountEstimationForAPIRequest } = await import('../../../src/services/tokenEstimation')
const results = []
const markdown = []
const rendered = []
for (const row of input.rows) {
	const tools = getTools(getEmptyToolPermissionContext()).filter(tool => (row.tools ?? []).includes(tool.name))
  const data = await analyzeContextUsage(
    getMessagesAfterCompactBoundary(row.messages), row.model,
    async () => getEmptyToolPermissionContext(), tools,
    { allAgents: row.agents ?? [], activeAgents: row.agents ?? [] } as any,
    row.width,
    { options: { customSystemPrompt: row.prompt, appendSystemPrompt: row.append } } as any,
    undefined, getMessagesAfterCompactBoundary(row.messages),
    { estimateOnly: row.estimateOnly !== false, messagesAlreadyMicrocompacted: true },
  )
  results.push(data)
  if (input.renderGrid) {
    const React = await import('react')
    const { ContextVisualization } = await import('../../../src/components/ContextVisualization')
    const { renderToAnsiString } = await import('../../../src/utils/staticRender')
    rendered.push(await renderToAnsiString(React.createElement(ContextVisualization, { data }), row.width || 80))
  }
  if (input.markdown) {
    const { call } = await import('../../../src/commands/context/context-noninteractive')
    const text = await call('', {
      messages: row.messages,
      getAppState: () => ({ toolPermissionContext: getEmptyToolPermissionContext() }),
      options: { mainLoopModel: row.model, tools, agentDefinitions: { allAgents: row.agents ?? [], activeAgents: row.agents ?? [] }, customSystemPrompt: row.prompt, appendSystemPrompt: row.append, estimateOnly: row.estimateOnly !== false },
    } as any)
    markdown.push(text.value)
  }
}
const estimates = input.estimates.map((row: any) => roughTokenCountEstimationForAPIRequest(row.messages, row.schemas))
if (capabilities.blocked.length) throw new Error('Blocked analyser capability: ' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({ results, estimates, markdown, rendered }))
