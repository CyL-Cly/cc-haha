import fs from 'node:fs'
import * as files from 'node:fs/promises'
import { dirname, join, resolve } from 'node:path'
import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Current native Agent discovery')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_CONFIG_DIR_AGENTS_SOURCE !== '1' || !['between', 'held-after-sampling', 'relative'].includes(input.profile)) throw new Error('Pinned declared Agent discovery profile required')
let project = join(home, 'project')
const initial = join(home, '.claude')
const next = input.profile === 'relative' ? 'owned-next' : join(home, 'owned-next')
const nextPhysical = resolve(project, next)
const content = (name: string) => `---\nname: ${name}\ndescription: Actual current directory ${name}\nmodel: inherit\ncolor: blue\n---\nACTUAL_AGENT_BODY_${name}\n`
for (const [directory, name] of [[initial, 'old-worker'], [nextPhysical, 'next-worker']]) {
  fs.mkdirSync(join(directory!, 'agents'), { recursive: true })
  fs.writeFileSync(join(directory!, 'settings.json'), '{}')
  fs.writeFileSync(join(directory!, 'agents', 'worker.md'), content(name!))
}
fs.mkdirSync(join(project, '.git'), { recursive: true })
fs.mkdirSync(join(project, '.claude', 'agents'), { recursive: true })
fs.writeFileSync(join(project, '.claude', 'settings.json'), '{}')
fs.writeFileSync(join(project, '.claude', 'agents', 'project.md'), content('project-worker'))
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
;(globalThis as any).MACRO = { VERSION: '999.0.0-agent-directory', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
process.chdir(project)
project = process.cwd()
const state = await import('../../../src/bootstrap/state')
state.setCwdState(project)
state.setOriginalCwd(project)
state.setProjectRoot(project)
state.setIsInteractive(false)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const reads: string[] = []
const readFile = files.readFile
let startedResolve: (() => void) | undefined, releaseResolve: (() => void) | undefined
const started = new Promise<void>(resolve => { startedResolve = resolve })
const release = new Promise<void>(resolve => { releaseResolve = resolve })
let hold = false
mock.module('fs/promises', () => ({ ...files, readFile: async (path: any, ...args: any[]) => {
  const data = await (readFile as any)(path, ...args)
  if (String(path).endsWith('.md')) reads.push(String(path))
  if (hold && String(path) === join(initial, 'agents', 'worker.md')) { hold = false; startedResolve!(); await release }
  return data
} }))
const { getAgentDefinitionsWithOverrides, clearAgentDefinitionsCache } = await import('../../../src/tools/AgentTool/loadAgentsDir')
const { isAutoMemoryEnabled } = await import('../../../src/memdir/paths')
const { loadPluginAgents } = await import('../../../src/utils/plugins/loadPluginAgents')
const { getCachedSettingsForSource } = await import('../../../src/utils/settings/settingsCache')
const sources = ['userSettings', 'projectSettings', 'localSettings', 'flagSettings', 'policySettings'] as const
const projectRows = (value: any) => value.allAgents.filter((agent: any) => agent.source === 'userSettings' || agent.source === 'projectSettings').map((agent: any) => {
  const { getSystemPrompt, ...fields } = agent
  return { fields, prompt: getSystemPrompt() }
})
process.env.CLAUDE_CONFIG_DIR = initial
// The held profile isolates discovery's capture from the separate cold source
// settings boundary: complete the original loader once, then clear its own
// caches. This leaves actual per-source settings/plugin caches warmed.
let primed: any
if (input.profile === 'held-after-sampling') {
  primed = await getAgentDefinitionsWithOverrides(project)
  clearAgentDefinitionsCache()
  hold = true
}
const beforePromise = getAgentDefinitionsWithOverrides(project)
if (input.profile === 'held-after-sampling') { await started; process.env.CLAUDE_CONFIG_DIR = next; releaseResolve!() }
const before = await beforePromise
process.env.CLAUDE_CONFIG_DIR = next
const warm = await getAgentDefinitionsWithOverrides(project)
const sourceSettings = sources.map(source => getCachedSettingsForSource(source))
clearAgentDefinitionsCache()
const cleared = await getAgentDefinitionsWithOverrides(project)
const plugins = await loadPluginAgents()
if (caps.blocked.length || caps.secureReads.length || before.failedFiles?.length || cleared.failedFiles?.length) throw new Error('Agent Source failed actual private discovery ' + JSON.stringify({ caps, before: before.failedFiles, cleared: cleared.failedFiles }))
process.stdout.write(JSON.stringify({ initial, next, nextPhysical, project, before: projectRows(before), warm: projectRows(warm), cleared: projectRows(cleared), sameWarmIdentity: before === warm, differentClearedIdentity: before !== cleared, warmSourcesBeforeHold: primed !== undefined && primed !== before, allSourcesWarm: sourceSettings.every(value => value !== undefined), sameSourceCacheIdentity: sources.every((source, index) => getCachedSettingsForSource(source) === sourceSettings[index]), reads, memoryDisabled: !isAutoMemoryEnabled(), pluginCount: plugins.length, caps, scope: 'original whole Agent loader and actual native walk/FS; no model/Main entry; full clear preserves separate Source settings caches; held profile primes those sources through a completed original load' }))
process.exit(0)
