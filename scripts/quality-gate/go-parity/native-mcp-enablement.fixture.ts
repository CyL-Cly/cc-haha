import { assertDisposableFixtureHome } from './fixture-guard'
import { mkdirSync, unlinkSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'

const home = assertDisposableFixtureHome(process.env.HOME, 'MCP enablement Source comparison')
if (process.env.CC_HAHA_MCP_ENABLEMENT_FIXTURE !== '1') throw new Error('Missing MCP enablement fixture marker')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
mkdirSync(join(home, '.claude'), { recursive: true })
mkdirSync(join(home, 'managed'), { recursive: true })
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = join(home, 'managed')
const { getManagedFilePath } = await import('../../../src/utils/settings/managedPath')
if (getManagedFilePath() !== join(home, 'managed')) throw new Error('Unowned policy path')
process.env.USER_TYPE = 'external'
if (input.defaultDirectory) delete process.env.CLAUDE_CONFIG_DIR
const expectedPath = input.defaultDirectory ? join(home, '.claude.json') : join(home, '.claude', '.claude.json')
writeFileSync(expectedPath, '{}')
const state = await import('../../../src/bootstrap/state')
state.setOriginalCwd(home)
state.setProjectRoot(home)
state.setCwdState(home)
const { enableConfigs } = await import('../../../src/utils/config')
enableConfigs()
const { getGlobalClaudeFile } = await import('../../../src/utils/env')
const { isMcpServerDisabledForExecution } = await import('../../../src/services/mcp/config')
const path = getGlobalClaudeFile()
if (path !== expectedPath) throw new Error('Unowned global config path')
const observations = []
for (const step of input.steps) {
  if (step.file === null) {
    try { unlinkSync(path) } catch (error) { if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error }
  } else writeFileSync(path, step.file)
  try { observations.push({ enabled: !isMcpServerDisabledForExecution(step.name) }) }
  catch (error) { observations.push({ error: (error as Error).message }) }
}
process.stdout.write(JSON.stringify({ path, observations }))
