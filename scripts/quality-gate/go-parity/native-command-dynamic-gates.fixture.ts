import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual warmed command gate projection')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_COMMAND_DYNAMIC_GATES !== '1' || typeof input.initialEnabled !== 'boolean') throw new Error('Unknown private dynamic command profile')
const cwd = join(home, 'project')
const configDir = join(home, '.claude')
const workflowPath = join(cwd, '.claude', 'workflows', 'live-workflow.js')
const dynamicDir = join(home, 'dynamic-skills')
const cuPath = join(configDir, 'cc-haha', 'computer-use-config.json')
const workflowBody = (description: string) => `export const meta={name:'live-workflow',description:${JSON.stringify(description)}};return args`
fs.mkdirSync(dirname(workflowPath), { recursive: true })
fs.mkdirSync(dirname(cuPath), { recursive: true })
fs.mkdirSync(join(dynamicDir, 'computer-use'), { recursive: true })
fs.writeFileSync(workflowPath, workflowBody('Original cached workflow'))
fs.writeFileSync(join(dynamicDir, 'computer-use', 'SKILL.md'), '---\ndescription: Dynamic computer replacement\n---\nActual dynamically loaded skill.')
const absent = absentManagedSettingsFixturePaths()
for (const spelling of new Set([home, fs.realpathSync(home)])) for (let path = dirname(spelling); ; path = dirname(path)) {
  for (const suffix of ['.git', '.claude/skills', '.claude/commands', '.claude/workflows', '.agents/skills']) {
    const candidate = join(path, suffix)
    try { fs.lstatSync(candidate) } catch (error: any) { if (error.code !== 'ENOENT') throw error; absent.push(candidate) }
  }
  if (dirname(path) === path) break
}
for (const candidate of ['/Library/Application Support/ClaudeCode/.claude/skills', '/Library/Application Support/ClaudeCode/.claude/commands', '/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token', join(dirname(process.execPath), 'rg'), '/usr/bin/rg', '/bin/rg']) {
  try { fs.lstatSync(candidate) } catch (error: any) { if (error.code !== 'ENOENT') throw error; absent.push(candidate) }
}
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent, loopbackFetchOrigin: 'http://127.0.0.1:1' })
;(globalThis as any).MACRO = { VERSION: '999.0.0-command-gates', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(cwd); bootstrap.setOriginalCwd(cwd); bootstrap.setProjectRoot(cwd); bootstrap.setIsInteractive(false)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const settings = await import('../../../src/utils/settings/settings')
const managedEnvironment = await import('../../../src/utils/managedEnv')
const cuGate = await import('../../../src/utils/computerUse/skillGate')
const imagegen = await import('../../../src/skills/bundled/imagegen')
const computerUse = await import('../../../src/skills/bundled/computerUse')
// The actual registered Command objects retain their isEnabled closures.
// Other bundled producers are outside this bounded full-getCommands profile.
imagegen.registerImagegenSkill(); computerUse.registerComputerUseSkill()
const commands = await import('../../../src/commands')
const skills = await import('../../../src/skills/loadSkillsDir')
const imageEnv = (enabled: boolean) => ({ CC_HAHA_IMAGE_PROVIDER_KIND: enabled ? 'openai_images' : '', CC_HAHA_IMAGE_PROVIDER_ID: enabled ? 'owned' : '', CC_HAHA_IMAGE_MODEL: enabled ? 'owned-model' : '', CC_HAHA_IMAGE_BASE_URL: enabled ? 'http://127.0.0.1:1' : '', CC_HAHA_IMAGE_API_KEY: enabled ? 'fixture-key' : '' })
function setGates(enabled: boolean, workflow: boolean) {
  fs.writeFileSync(cuPath, JSON.stringify({ enabled }))
  cuGate.invalidateComputerUseSkillGate()
  const result = settings.updateSettingsForSource('userSettings', { enableWorkflows: workflow, env: imageEnv(enabled) })
  if (result.error) throw result.error
  managedEnvironment.applyConfigEnvironmentVariables()
}
const select = (all: any[]) => all.filter(command => ['computer-use', 'imagegen', 'live-workflow'].includes(command.name)).map(command => ({ name: command.name, description: command.description, loadedFrom: command.loadedFrom ?? '' })).sort((a, b) => a.name.localeCompare(b.name))
setGates(input.initialEnabled, true)
const first = await commands.getCommands(cwd)
const workflow = first.find(command => command.name === 'live-workflow')!
const slash = commands.getSlashCommandToolSkills(cwd)
const slashNames = (await slash).map(command => command.name).filter(name => ['computer-use', 'imagegen'].includes(name)).sort()
const frames = [{ selected: select(first), slashNames, slashSame: true, workflowIdentity: true }]
async function capture() {
  const all = await commands.getCommands(cwd)
  const currentSlash = commands.getSlashCommandToolSkills(cwd)
  const currentWorkflow = all.find(command => command.name === 'live-workflow')
  frames.push({ selected: select(all), slashNames: (await currentSlash).map(command => command.name).filter(name => ['computer-use', 'imagegen'].includes(name)).sort(), slashSame: currentSlash === slash, workflowIdentity: !currentWorkflow || currentWorkflow === workflow })
}
// The disk body changes without clearing loadAllCommands. Fresh projection
// must re-filter the old definition, never reload it to implement the gate.
fs.writeFileSync(workflowPath, workflowBody('Changed disk workflow must stay memoized'))
setGates(!input.initialEnabled, false); await capture()
setGates(false, true); await skills.addSkillDirectories([dynamicDir]); await capture()
setGates(true, true); await capture()
if (caps.blocked.length || caps.secureReads.length) throw new Error('Dynamic command profile escaped private capabilities: ' + JSON.stringify(caps))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ frames, caps }), error => error ? reject(error) : resolve()))
