import { dirname, join } from 'node:path'
import { mkdir, writeFile } from 'node:fs/promises'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'AutoDream runtime authority')
if (process.env.CC_HAHA_AUTO_DREAM_RUNTIME_FIXTURE !== '1') throw new Error('Unowned AutoDream runtime fixture')
const capabilities = await installClientFixtureCapabilities(home, {
  absentPaths: [...absentManagedSettingsFixturePaths(), '/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token', join(dirname(process.execPath), 'rg'), '/usr/bin/rg', '/bin/rg'],
})
await mkdir(join(home, '.claude'), { recursive: true })
await writeFile(join(home, '.claude', 'settings.json'), '{}')
process.env.CLAUDE_COWORK_MEMORY_PATH_OVERRIDE = join(home, 'memory')
process.env.ANTHROPIC_API_KEY = 'fake-no-inference'
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home)
state.setOriginalCwd(home)
state.setProjectRoot(home)
state.setIsInteractive(true)
const { getAutoMemPath } = await import('../../../src/memdir/paths')
const { createAutoMemCanUseTool } = await import('../../../src/services/extractMemories/extractMemories')
const { BashTool } = await import('../../../src/tools/BashTool/BashTool')
const memory = getAutoMemPath()
const canUse = createAutoMemCanUseTool(memory)
const results = []
for (const row of input.permissionRows ?? []) {
  const actualInput = JSON.parse(JSON.stringify(row.input).replaceAll('$HOME', home))
  const tool = row.name === 'Bash' ? BashTool : { name: row.name }
  const decision = await canUse(tool as any, actualInput)
  results.push(JSON.parse(JSON.stringify(decision).replaceAll(home, '$HOME')))
}
if (capabilities.blocked.length) throw new Error('AutoDream authority accessed blocked capability: ' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({ memory: memory.replaceAll(home, '$HOME'), results, capabilities }))
