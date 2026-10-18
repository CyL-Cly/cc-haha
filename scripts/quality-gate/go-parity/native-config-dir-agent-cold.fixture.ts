import fs from 'node:fs'
import * as files from 'node:fs/promises'
import { dirname, join, resolve } from 'node:path'
import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'First-cold actual Agent settings')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_CONFIG_DIR_AGENT_COLD_SOURCE !== '1' || !['valid', 'schema', 'relative', 'flag', 'inline'].includes(input.profile)) throw new Error('Pinned declared first-cold Agent profile required')
let project = join(home, 'project')
const initial = join(home, '.claude')
const next = input.profile === 'relative' ? 'owned-next' : join(home, 'owned-next')
const nextPhysical = resolve(project, next)
const flagPath = input.profile === 'flag' || input.profile === 'inline' ? join(home, 'actual-flag.json') : undefined
const initialSettings = { builtInAgentOverrides: { 'general-purpose': { model: 'old-general', effort: 'low' } }, futureInitial: { keep: true } }
const nextSettings = {
  builtInAgentOverrides: {
    'general-purpose': { model: ' \ufeffnext-general\ufeff ', effort: 'high', future: { keep: 'actual next' } },
    Explore: { model: 'next-explore', effort: input.profile === 'schema' ? 'INVALID' : 'max', unknown: [true, 4] },
    'statusline-setup': input.profile === 'schema' ? { model: 42, effort: 1.5, future: 'kept after per-field catch' } : { model: 'next-statusline' },
  },
  futureNext: { string: 'actual \ud800 future', array: [1, null, false] },
}
const projectSettings = { builtInAgentOverrides: { Explore: { model: 'project-explore' } }, futureProject: true }
const localSettings = { builtInAgentOverrides: { Plan: { model: 'local-plan', effort: 'medium' } }, futureLocal: 7 }
const flagSettings = { builtInAgentOverrides: { Plan: { model: 'flag-plan', effort: 'max' } }, futureFlag: { preserve: true } }
const flagInline = input.profile === 'inline' ? { builtInAgentOverrides: { Plan: { model: 'inline-plan' } }, futureInline: [true, 'actual inline'] } : undefined
const content = (name: string) => `---\nname: ${name}\ndescription: Actual first-cold ${name}\nmodel: inherit\n---\nACTUAL_FIRST_COLD_BODY_${name}\n`
for (const [directory, name, settings] of [[initial, 'old-worker', initialSettings], [nextPhysical, 'next-worker', nextSettings]] as const) {
  fs.mkdirSync(join(directory, 'agents'), { recursive: true })
  fs.writeFileSync(join(directory, 'settings.json'), JSON.stringify(settings))
  fs.writeFileSync(join(directory, 'agents', 'worker.md'), content(name))
}
fs.mkdirSync(join(project, '.git'), { recursive: true })
fs.mkdirSync(join(project, '.claude', 'agents'), { recursive: true })
fs.writeFileSync(join(project, '.claude', 'agents', 'project.md'), content('project-worker'))
fs.writeFileSync(join(project, '.claude', 'settings.json'), JSON.stringify(projectSettings))
fs.writeFileSync(join(project, '.claude', 'settings.local.json'), JSON.stringify(localSettings))
if (flagPath) fs.writeFileSync(flagPath, JSON.stringify(flagSettings))
const absent = absentManagedSettingsFixturePaths()
for (const subdir of ['agents', 'skills', 'commands', 'workflows']) {
  const path = join(dirname(absent[0]!), '.claude', subdir)
  if (!fs.existsSync(path)) absent.push(path)
}
for (const spelling of new Set([home, fs.realpathSync(home)])) {
  for (let path = spelling; ; path = dirname(path)) {
    for (const suffix of ['.git', '.claude/agents', '.claude/settings.json', '.claude/settings.local.json', '.agents/skills']) {
      const candidate = join(path, suffix)
      if (!fs.existsSync(candidate)) absent.push(candidate)
    }
    if (dirname(path) === path) break
  }
}
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent })
;(globalThis as any).MACRO = { VERSION: '999.0.0-cold-agent-directory', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
process.chdir(project)
project = process.cwd()
const state = await import('../../../src/bootstrap/state')
state.setCwdState(project)
state.setOriginalCwd(project)
state.setProjectRoot(project)
state.setIsInteractive(false)
state.setFlagSettingsPath(flagPath)
if (flagInline) state.setFlagSettingsInline(flagInline)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const { getInitialSettings, getSettingsFilePathForSource, parseSettingsFile } = await import('../../../src/utils/settings/settings')
const { getCachedSettingsForSource } = await import('../../../src/utils/settings/settingsCache')
process.env.CLAUDE_CONFIG_DIR = initial
// The actual launch's merged parser cache is warm, but per-source getters are
// deliberately not called. That is the real native factory's starting state.
getInitialSettings()
const sources = ['userSettings', 'projectSettings', 'localSettings', 'flagSettings', 'policySettings'] as const
const reads: string[] = []
const readFile = files.readFile
let startedResolve!: () => void, releaseResolve!: () => void
const started = new Promise<void>(resolve => { startedResolve = resolve })
const release = new Promise<void>(resolve => { releaseResolve = resolve })
let hold = true
mock.module('fs/promises', () => ({ ...files, readFile: async (path: any, ...args: any[]) => {
  const data = await (readFile as any)(path, ...args)
  if (String(path).endsWith('.md')) reads.push(String(path))
  if (hold && String(path) === join(initial, 'agents', 'worker.md')) { hold = false; startedResolve(); await release }
  return data
} }))
const { getAgentDefinitionsWithOverrides, clearAgentDefinitionsCache } = await import('../../../src/tools/AgentTool/loadAgentsDir')
const customRows = (result: any) => result.allAgents.filter((agent: any) => agent.source === 'userSettings' || agent.source === 'projectSettings').map((agent: any) => { const { getSystemPrompt, ...fields } = agent; return { fields, prompt: getSystemPrompt() } })
const builtinRows = (result: any) => result.allAgents.filter((agent: any) => agent.source === 'built-in').map((agent: any) => ({ name: agent.agentType, model: agent.model ?? null, effort: agent.effort ?? null }))
const pending = getAgentDefinitionsWithOverrides(project)
await started
const coldAtHold = Object.fromEntries(sources.map(source => [source, getCachedSettingsForSource(source) === undefined]))
process.env.CLAUDE_CONFIG_DIR = next
const nextSettingsPath = getSettingsFilePathForSource('userSettings')
releaseResolve()
const before = await pending
const cached = sources.map(source => getCachedSettingsForSource(source))
const sourceSettings = Object.fromEntries(sources.map((source, index) => [source, cached[index] ?? null]))
const mutatedSettings = { builtInAgentOverrides: { 'general-purpose': { model: 'physical-change-must-stay-cached' } }, futureMutated: true }
fs.writeFileSync(join(nextPhysical, 'settings.json'), JSON.stringify(mutatedSettings))
const warm = await getAgentDefinitionsWithOverrides(project)
clearAgentDefinitionsCache()
const cleared = await getAgentDefinitionsWithOverrides(project)
const parsed = parseSettingsFile(nextSettingsPath!)
if (caps.blocked.length || caps.secureReads.length || before.failedFiles?.length || cleared.failedFiles?.length || cached.some(value => value === undefined) || !coldAtHold.userSettings) throw new Error('First-cold actual Agent failed ' + JSON.stringify({ caps, coldAtHold, failed: [before.failedFiles, cleared.failedFiles] }))
process.stdout.write(JSON.stringify({ initial, next, nextPhysical, project, flagPath: flagPath ?? null, flagInline: flagInline ?? null, initialSettings, nextSettings, projectSettings, localSettings, flagSettings: flagPath ? flagSettings : null, mutatedSettings, coldAtHold, nextSettingsPath, before: customRows(before), warm: customRows(warm), cleared: customRows(cleared), builtinBefore: builtinRows(before), builtinWarm: builtinRows(warm), builtinCleared: builtinRows(cleared), sourceSettings, parsedNext: parsed.settings, nextParseErrors: parsed.errors, sameWarmIdentity: before === warm, differentClearedIdentity: before !== cleared, sameSourceCacheIdentity: sources.every((source, index) => getCachedSettingsForSource(source) === cached[index]), reads, caps, scope: 'whole frozen Agent/builtin override/source getter/schema actual FS; first-cold source cache, no priming or injected business handlers' }))
process.exit(0)
