import fs from 'node:fs'
import path from 'node:path'
import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual installed plugin LSP configuration')
if (Object.keys(input).some(key => !['home', 'cwd'].includes(key))) throw new Error('Undeclared LSP configuration input')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_LSP_CONFIGURATION_SOURCE !== '1') throw new Error('Pinned original LSP configuration guard required')
const cwd = fs.realpathSync(input.cwd)
const owned = path.relative(fs.realpathSync(home), cwd)
if (owned === '..' || owned.startsWith('..' + path.sep) || path.isAbsolute(owned)) throw new Error('Unowned LSP project')
const absentPaths = absentManagedSettingsFixturePaths()
const managedSkills = path.join(path.dirname(absentPaths[0]!), '.claude', 'skills')
if (!fs.existsSync(managedSkills)) absentPaths.push(managedSkills)
for (const spelling of new Set([home, fs.realpathSync(home)])) for (let parent = path.dirname(spelling);; parent = path.dirname(parent)) {
  for (const name of ['.git', '.claude/settings.json', '.claude/settings.local.json', '.claude/agents', '.agents/agents', '.claude/skills', '.claude/commands', '.claude/workflows', '.agents/skills', '.mcp.json']) {
    const target = path.join(parent, name)
    if (!fs.existsSync(target)) absentPaths.push(target)
  }
  if (path.dirname(parent) === parent) break
}
const rg = path.join(path.dirname(process.execPath), process.platform === 'win32' ? 'rg.exe' : 'rg')
if (!fs.existsSync(rg)) absentPaths.push(rg)
const capabilities = await installClientFixtureCapabilities(home, { absentPaths })
;(globalThis as any).MACRO = { VERSION: '999.0.0-lsp-configuration', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setCwdState(cwd)
state.setOriginalCwd(cwd)
state.setProjectRoot(cwd)
state.setIsInteractive(false)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const errors: string[] = []
const log = await import('../../../src/utils/log')
const originalLogError = log.logError
spyOn(log, 'logError').mockImplementation((error, ...args) => {
  errors.push(error instanceof Error ? error.message : String(error))
  return originalLogError(error, ...args)
})
const debug = await import('../../../src/utils/debug')
const originalDebug = debug.logForDebugging
const debugMessages: Array<{ message: string, level: string }> = []
spyOn(debug, 'logForDebugging').mockImplementation((message, options) => {
  debugMessages.push({ message, level: options?.level ?? 'debug' })
  return originalDebug(message, options)
})
const { getAllLspServers } = await import('../../../src/services/lsp/config')
const result = await getAllLspServers()
if (capabilities.blocked.length) throw new Error('Original LSP configuration escaped fixture: ' + JSON.stringify(capabilities))
process.stdout.write(JSON.stringify({ ...result, errors, debugMessages, capabilities }))
