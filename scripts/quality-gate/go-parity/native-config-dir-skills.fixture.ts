import fs from 'node:fs'
import { spyOn } from 'bun:test'
import { dirname, join, resolve } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Native current skills directory')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_CONFIG_DIR_SKILLS_SOURCE !== '1') throw new Error('Pinned skills directory Source required')
const initial = join(home, '.claude'), cwd = join(home, 'owned-skill-work')
const profile = input.profile ?? 'absolute'
const directories: Record<string, string> = { absolute: join(home, 'owned-next-skills'), empty: '', relative: 'owned-relative-skills', parent: '../owned-parent-skills', unicode: join(home, 'owned-cafe\u0301-skills'), home: join(home, 'owned-next-skills'), 'settings-disabled': join(home, 'owned-next-skills') }
if (!Object.hasOwn(directories, profile)) throw new Error('Unknown skills directory profile')
const next = directories[profile]!, nextPhysical = resolve(cwd, next.normalize('NFC'))
for (const [root, name] of [[initial, 'old'], [nextPhysical, 'next']] as const) {
  for (const subdir of ['skills', 'commands']) fs.mkdirSync(join(root, subdir), { recursive: true })
  fs.mkdirSync(join(root, 'skills', `${name}-skill`), { recursive: true })
  fs.writeFileSync(join(root, 'skills', `${name}-skill`, 'SKILL.md'), `---\ndescription: ${name} user skill\n---\n${name} skill body.\n`)
  fs.writeFileSync(join(root, 'commands', `${name}-legacy.md`), `---\ndescription: ${name} legacy command\n---\n${name} legacy body.\n`)
  fs.writeFileSync(join(root, 'settings.json'), profile === 'settings-disabled' && name === 'old' ? '{"disableAgentSkillsDirectory":true,"futurePrivate":{"keep":true}}\n' : '{}\n')
}
fs.mkdirSync(cwd, { recursive: true })
fs.mkdirSync(join(cwd, '.git'), { recursive: true })
fs.mkdirSync(join(home, '.agents', 'skills'), { recursive: true })
const nextHome = profile === 'home' ? join(home, 'owned-next-agent-home') : undefined
if (nextHome) {
  for (const [root, name] of [[home, 'old'], [nextHome, 'next']] as const) {
    const dir = join(root, '.agents', 'skills', `${name}-agent`)
    fs.mkdirSync(dir, { recursive: true })
    fs.writeFileSync(join(dir, 'SKILL.md'), `---\ndescription: ${name} shared agent skill\n---\n${name} shared body.\n`)
  }
}
const absent = absentManagedSettingsFixturePaths()
for (let parent = home; ; parent = dirname(parent)) {
  const path = join(parent, '.git')
  if (!fs.existsSync(path)) absent.push(path)
  if (dirname(parent) === parent) break
}
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent })
;(globalThis as any).MACRO = { VERSION: '999.0.0-skills-dir-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = join(home, 'managed')
const { getManagedFilePath } = await import('../../../src/utils/settings/managedPath')
if (getManagedFilePath() !== join(home, 'managed')) throw new Error('Unowned managed path')
process.env.USER_TYPE = 'external'
process.chdir(cwd)
const state = await import('../../../src/bootstrap/state')
state.setOriginalCwd(process.cwd())
state.setProjectRoot(process.cwd())
state.setCwdState(process.cwd())
state.setAllowedSettingSources(['userSettings', 'projectSettings', 'localSettings', 'policySettings'])
const { enableConfigs } = await import('../../../src/utils/config')
enableConfigs()
const roots = await import('../../../src/skills/skillRoots')
const skills = await import('../../../src/skills/loadSkillsDir')
const watcher = await import('../../../src/utils/skills/skillChangeDetector')
const project = (command: Awaited<ReturnType<typeof skills.getSkillDirCommands>>[number]) => ({ ...JSON.parse(JSON.stringify(command)), userFacingName: command.userFacingName?.() ?? command.name })
const observe = async () => ({ roots: roots.getUserSkillRoots(), commands: (await skills.getSkillDirCommands(process.cwd())).map(project), watch: await watcher.getWatchablePaths() })
const before = await observe()
process.env.CLAUDE_CONFIG_DIR = next
if (nextHome) process.env.HOME = nextHome
const warm = await observe()
skills.clearSkillCaches()
const cleared = await observe()
let held
if (input.held) {
  process.env.CLAUDE_CONFIG_DIR = initial
  skills.clearSkillCaches()
  let entered!: () => void, release!: () => void, armed = true
  const reached = new Promise<void>(resolve => { entered = resolve })
  const released = new Promise<void>(resolve => { release = resolve })
  const original = fs.promises.readdir
  const wrapper = async (...args: any[]) => {
    const result = await (original as any)(...args)
    if (armed && resolve(String(args[0])) === join(initial, 'skills')) {
      armed = false
      entered()
      await released
    }
    return result
  }
  ;(fs.promises as any).readdir = wrapper
  spyOn(await import('node:fs/promises'), 'readdir').mockImplementation(wrapper as any)
  const pending = skills.getSkillDirCommands(process.cwd())
  await reached
  process.env.CLAUDE_CONFIG_DIR = next
  release()
  held = { commands: (await pending).map(project), watch: await watcher.getWatchablePaths() }
}
if (caps.blocked.length || caps.secureReads.length) throw new Error('Skills Source escaped private boundary ' + JSON.stringify(caps))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ initial, next, nextPhysical, nextHome, cwd: process.cwd(), before, warm, cleared, held, caps }), error => error ? reject(error) : resolve()))
process.exit(0)
