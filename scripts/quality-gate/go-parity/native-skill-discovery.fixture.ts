import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { writeFileSync } from 'node:fs'
import { join } from 'node:path'

const home = assertDisposableFixtureHome(process.env.HOME, 'Native Skill discovery')
if (process.env.CC_HAHA_SKILLS_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') {
  throw new Error('Skills contract requires its isolated production fixture')
}
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
let plugins: unknown[] = []
const originalPluginLoader = await import('../../../src/utils/plugins/pluginLoader')
// Only the not-yet-bound plugin producer is controlled. Roots, parsing, file
// traversal, metadata, precedence and the actual REST handler are production.
mock.module('../../../src/utils/plugins/pluginLoader.ts', () => ({
  ...originalPluginLoader,
  clearPluginCache() {},
  async loadAllPluginsCacheOnly() { return { enabled: plugins, disabled: [], errors: [] } },
  async loadAllPlugins() { return { enabled: plugins, disabled: [], errors: [] } },
}))
const { setOriginalCwd, setProjectRoot, setCwdState } = await import('../../../src/bootstrap/state')
const { resetSettingsCache } = await import('../../../src/utils/settings/settingsCache')
const { getUserSkillRoots, getProjectSkillRoots, getAddDirSkillRoots, getNestedSkillDirCandidates } = await import('../../../src/skills/skillRoots')
const { handleSkillsApi, collectUserSkillNames } = await import('../../../src/server/api/skills')
const output = []
for (const scenario of input.scenarios) {
  plugins = scenario.plugins ?? []
  writeFileSync(join(home, '.claude', 'settings.json'), JSON.stringify(scenario.settings ?? {}))
  process.env.CLAUDE_CODE_DISABLE_AGENT_SKILLS_DIR = scenario.disableEnv ?? ''
  setOriginalCwd(scenario.cwd)
  setCwdState(scenario.cwd)
  setProjectRoot(scenario.projectRoot)
  resetSettingsCache()
  const requests = []
  for (const path of scenario.requests) {
    const url = new URL(path, 'http://fixture.local')
    url.searchParams.set('cwd', scenario.cwd)
    const response = await handleSkillsApi(new Request(url), url, url.pathname.split('/').filter(Boolean))
    requests.push({ status: response.status, body: await response.json() })
  }
  output.push({
    userRoots: getUserSkillRoots(), projectRoots: getProjectSkillRoots(scenario.cwd),
    addDirRoots: getAddDirSkillRoots(scenario.cwd), nestedCandidates: getNestedSkillDirCandidates(scenario.cwd),
    userNames: [...await collectUserSkillNames()], requests,
  })
}
process.stdout.write(JSON.stringify(output))
