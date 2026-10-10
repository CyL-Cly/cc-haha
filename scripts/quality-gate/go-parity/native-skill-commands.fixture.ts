import { assertDisposableFixtureHome } from './fixture-guard'
import { isAbsolute, relative, sep } from 'node:path'
import { realpathSync } from 'node:fs'

const home = assertDisposableFixtureHome(process.env.HOME, 'Native Skill commands')
if (process.env.CC_HAHA_NATIVE_SKILL_COMMAND_FIXTURE !== '1') throw new Error('Undeclared Skill command fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const within = relative(realpathSync(home), realpathSync(input.cwd))
if (!isAbsolute(input.cwd) || within === '..' || within.startsWith(`..${sep}`) || isAbsolute(within)) throw new Error('Unowned Skill project')
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = `${home}/managed`
// Resolve the actual memoized managed directory while its fixture override is
// available, then exercise the same external model/profile as the Go caller.
// No filesystem or business function is replaced, and policy reads stay private.
const { getManagedFilePath } = await import('../../../src/utils/settings/managedPath')
if (getManagedFilePath() !== `${home}/managed`) throw new Error('Unowned managed Skill root')
process.env.USER_TYPE = 'external'
process.chdir(input.cwd)
const state = await import('../../../src/bootstrap/state')
state.setOriginalCwd(input.cwd)
state.setProjectRoot(input.cwd)
state.setCwdState(input.cwd)
state.setAllowedSettingSources(input.sources ?? ['userSettings', 'projectSettings', 'localSettings', 'policySettings'])
state.setAdditionalDirectoriesForClaudeMd(input.additional ?? [])
const { enableConfigs } = await import('../../../src/utils/config')
enableConfigs()
const skills = await import('../../../src/skills/loadSkillsDir')
const project = (command: Awaited<ReturnType<typeof skills.getSkillDirCommands>>[number]) => {
  const result = JSON.parse(JSON.stringify(command))
  result.userFacingName = command.userFacingName?.() ?? command.name
  return result
}
const observations = []
for (const operation of input.operations) {
  if (operation.kind === 'local') observations.push({ commands: (await skills.getSkillDirCommands(input.cwd)).map(project), pending: skills.getConditionalSkillCount() })
  else if (operation.kind === 'activate') observations.push({ names: skills.activateConditionalSkillsForPaths(operation.paths, input.cwd), dynamic: skills.getDynamicSkills().map(project), pending: skills.getConditionalSkillCount() })
  else if (operation.kind === 'clear') { skills.clearSkillCaches(); observations.push({ dynamic: skills.getDynamicSkills().map(project), pending: skills.getConditionalSkillCount() }) }
  else throw new Error('Undeclared Skill operation')
}
process.stdout.write(JSON.stringify({ observations }))
