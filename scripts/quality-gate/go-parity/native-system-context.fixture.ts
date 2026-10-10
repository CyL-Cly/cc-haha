import { dirname, join, sep } from 'node:path'
import * as children from 'node:child_process'
import { realpathSync } from 'node:fs'
import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'Native system context')
if (process.env.CC_HAHA_SYSTEM_CONTEXT_FIXTURE !== '1') throw new Error('Unowned system context fixture')
const cwd = realpathSync(input.cwd)
if (!cwd.startsWith(realpathSync(home) + sep)) throw new Error('Unowned Git directory')
const originalSpawn = children.spawn
const originalBunSpawn = Bun.spawn
const originals = Bun.which('git')
const absent = [...absentManagedSettingsFixturePaths(), join(dirname(process.execPath), 'rg'), '/usr/bin/rg', '/bin/rg']
for (const base of [home, realpathSync(home)]) {
  for (let parent = dirname(base); ; parent = dirname(parent)) {
    absent.push(join(parent, '.git'))
    if (parent === dirname(parent)) break
  }
}
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absent })
const check = (file: string, args: string[], options: any) => {
  if (![originals, 'git'].includes(file) || options.shell || options.cwd && realpathSync(options.cwd) !== cwd || ![
    ['--no-optional-locks', 'status', '--short'],
    ['--no-optional-locks', 'log', '--oneline', '-n', '5'],
    ['config', 'user.name'],
  ].some(allowed => JSON.stringify(args) === JSON.stringify(allowed))) throw new Error('Undeclared Git process')
}
const processEnvironment = (options: any) => ({ ...options, env: { ...(options.env ?? process.env), HOME: home, USERPROFILE: home, GIT_CONFIG_NOSYSTEM: '1', XDG_CONFIG_HOME: join(home, '.config') } })
const spawn: typeof children.spawn = ((file: string, args: string[], options: any) => {
  check(file, args, options)
  return originalSpawn(file, args, processEnvironment(options))
}) as typeof children.spawn
require('node:child_process').spawn = spawn
spyOn(children, 'spawn').mockImplementation(spawn)
;(Bun as any).spawn = (command: any, supplied: any = {}) => {
  const options = Array.isArray(command) ? supplied : command
  const argv = Array.isArray(command) ? command : command.cmd
  check(argv[0], argv.slice(1), options)
  return originalBunSpawn(argv, processEnvironment(options))
}
await import('../../../preload')
process.chdir(cwd)
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = join(home, 'managed')
const managed = await import('../../../src/utils/settings/managedPath')
if (managed.getManagedFilePath() !== join(home, 'managed')) throw new Error('Managed fixture path mismatch')
process.env.USER_TYPE = 'external'
for (const [key, value] of Object.entries(input.environment)) process.env[key] = String(value)
const state = await import('../../../src/bootstrap/state')
state.setCwdState(cwd)
state.setOriginalCwd(cwd)
state.setProjectRoot(cwd)
state.setAllowedSettingSources(['userSettings', 'projectSettings', 'localSettings', 'policySettings'])
const config = await import('../../../src/utils/config')
config.enableConfigs()
const settings = await import('../../../src/utils/settings/settings')
settings.getInitialSettings()
const context = await import('../../../src/context')
const git = await import('../../../src/utils/git')
const [first, repeated] = input.concurrent
  ? await Promise.all([context.getSystemContext(), context.getSystemContext()])
  : [await context.getSystemContext(), await context.getSystemContext()]
const main = await git.getDefaultBranch()
const branch = await git.getBranch()
const rawStatus = await context.getGitStatus()
const { resetGitFileWatcher } = await import('../../../src/utils/git/gitFilesystem')
resetGitFileWatcher()
if (capabilities.blocked.length) throw new Error('Blocked system context capability: ' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({ first, repeated, main, branch, rawStatus }))
