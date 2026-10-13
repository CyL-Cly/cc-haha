import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { isAbsolute, relative, sep, join } from 'node:path'

const home = assertDisposableFixtureHome(process.env.HOME, 'Warm market Skill')
if (process.env.CC_HAHA_WARM_MARKET_SKILL_FIXTURE !== '1') throw new Error('Undeclared warm market fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const within = relative(home, input.cwd)
if (!isAbsolute(input.cwd) || within === '..' || within.startsWith(`..${sep}`) || isAbsolute(within)) throw new Error('Unowned warm market project')
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = `${home}/managed`
const { getManagedFilePath } = await import('../../src/utils/settings/managedPath')
if (getManagedFilePath() !== `${home}/managed`) throw new Error('Unowned managed root')
process.env.USER_TYPE = 'external'
process.chdir(input.cwd)
const state = await import('../../src/bootstrap/state')
state.setOriginalCwd(input.cwd)
state.setProjectRoot(input.cwd)
state.setCwdState(input.cwd)
state.setAllowedSettingSources(['userSettings', 'projectSettings', 'localSettings', 'policySettings'])
const { enableConfigs } = await import('../../src/utils/config')
enableConfigs()
const skills = await import('../../src/skills/loadSkillsDir')
const detector = await import('../../src/utils/skills/skillChangeDetector')
await detector.resetForTesting({ stabilityThreshold: 40, pollInterval: 10, reloadDebounce: 20, chokidarInterval: 10 })
let notified = 0
const unsubscribe = detector.subscribe(() => { notified++ })
const watchableBefore = await detector.getWatchablePaths()
await detector.initialize()
const before = (await skills.getSkillDirCommands(input.cwd)).map(command => command.name)
const service = await import('../../src/server/services/market/installService')
await service.installMarketSkill('clawhub', 'warm-market-skill')
const after = (await skills.getSkillDirCommands(input.cwd)).map(command => command.name)
await new Promise(resolve => setTimeout(resolve, 250))
const watchableAfter = await detector.getWatchablePaths()
await service.uninstallMarketSkill('clawhub', 'warm-market-skill')
const removed = (await skills.getSkillDirCommands(input.cwd)).map(command => command.name)
unsubscribe()
await detector.dispose()
process.stdout.write(JSON.stringify({ before, after, removed, initialMissingRoot: watchableBefore.length === 0, laterRootExists: watchableAfter.some(path => path === join(process.env.CLAUDE_CONFIG_DIR!, 'skills')), detectorNotifications: notified }))
