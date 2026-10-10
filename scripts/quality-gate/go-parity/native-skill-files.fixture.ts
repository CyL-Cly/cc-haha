import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
import { relative, isAbsolute, sep, dirname, join } from 'node:path'
import * as childProcess from 'node:child_process'
import { spyOn } from 'bun:test'
import { realpathSync } from 'node:fs'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.homeDir, 'Native file Skill consumer')
if (process.env.CC_HAHA_NATIVE_SKILL_FILES_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Unowned Skill files fixture')
const inside = relative(home, input.cwd)
if (inside === '..' || inside.startsWith(`..${sep}`) || isAbsolute(inside)) throw new Error('Unowned work directory')
const spawnOriginal = childProcess.spawn
const bunSpawnOriginal = Bun.spawn
const ownedCwd = realpathSync(input.cwd)
const checkGit = (args: string[], options: any) => {
  if (args.length !== 3 || args[0] !== 'git' || args[1] !== 'check-ignore' || realpathSync(options.cwd) !== ownedCwd || !realpathSync(args[2]).startsWith(ownedCwd + sep) || options.shell) throw new Error('Undeclared native subprocess')
}
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: [...absentManagedSettingsFixturePaths(), join(dirname(process.execPath), 'rg'), '/usr/bin/rg', '/bin/rg'] })
const spawnGit: typeof childProcess.spawn = ((file: string, args: string[], options: any) => {
  checkGit([file, ...args], options)
  return spawnOriginal(file, args, { ...options, env: { ...options.env, HOME: home, USERPROFILE: home, GIT_CONFIG_NOSYSTEM: '1', XDG_CONFIG_HOME: join(home, '.config') } })
}) as typeof childProcess.spawn
require('node:child_process').spawn = spawnGit
spyOn(childProcess, 'spawn').mockImplementation(spawnGit)
;(Bun as any).spawn = (command: any, nativeOptions: any = {}) => {
  const options = Array.isArray(command) ? nativeOptions : command
  const args = Array.isArray(command) ? command : command.cmd
  if (!Array.isArray(args)) throw new Error('Undeclared Bun native subprocess')
  checkGit(args, options)
  return bunSpawnOriginal(args, { ...options, env: { ...options.env, HOME: home, USERPROFILE: home, GIT_CONFIG_NOSYSTEM: '1', XDG_CONFIG_HOME: join(home, '.config') } })
}
;(globalThis as any).MACRO = { BUILD_TIME: '' }
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = `${home}/managed`
const { getManagedFilePath } = await import('../../../src/utils/settings/managedPath')
if (getManagedFilePath() !== `${home}/managed`) throw new Error('Unowned managed path')
process.env.USER_TYPE = 'external'
const state = await import('../../../src/bootstrap/state')
// Real CLI startup observes process.cwd() after chdir, including macOS aliases.
// Keep the supplied file paths literal; expandPath does not resolve symlinks.
process.chdir(input.cwd)
const actualCwd = process.cwd()
state.setCwdState(actualCwd)
state.setOriginalCwd(actualCwd)
state.setProjectRoot(actualCwd)
state.setAllowedSettingSources(['userSettings', 'projectSettings', 'localSettings', 'policySettings'])
const { enableConfigs } = await import('../../../src/utils/config')
enableConfigs()
const skills = await import('../../../src/skills/loadSkillsDir')
await skills.getSkillDirCommands(actualCwd)
const { FileReadTool } = await import('../../../src/tools/FileReadTool/FileReadTool')
const { FileEditTool } = await import('../../../src/tools/FileEditTool/FileEditTool')
const { FileWriteTool } = await import('../../../src/tools/FileWriteTool/FileWriteTool')
const { getEmptyToolPermissionContext } = await import('../../../src/Tool')
const tools = [FileReadTool, FileEditTool, FileWriteTool]
let app = { toolPermissionContext: { ...getEmptyToolPermissionContext(), mode: 'bypassPermissions' } }
const context: any = { getAppState: () => app, setAppState: (update: any) => { app = update(app) }, readFileState: new Map(), dynamicSkillDirTriggers: new Set(), abortController: new AbortController(), options: { tools, agents: [], mainLoopModel: 'deepseek-flash' }, messages: [] }
const observations = []
for (const step of input.steps) {
  const tool = tools.find(tool => tool.name === step.tool)!
  const parsed = tool.inputSchema.parse(JSON.parse(step.input))
  const result = await tool.call(parsed as any, context, async () => ({ behavior: 'allow' as const, updatedInput: parsed }), { uuid: 'fixture-parent', message: { id: 'fixture-parent-message' } } as any)
  observations.push({ data: result.data, pending: skills.getConditionalSkillCount(), triggers: [...context.dynamicSkillDirTriggers] })
}
// Wait only for actual detached directory loading; no business callback or
// result is replaced to manufacture the final command inventory.
const deadline = Date.now() + 5000
while (!skills.getDynamicSkills().some(command => command.name === input.waitFor) && Date.now() < deadline) await new Promise(resolve => setTimeout(resolve, 10))
const dynamic = skills.getDynamicSkills().map(command => command.name)
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ observations, dynamic, capabilities }), error => error ? reject(error) : resolve()))
