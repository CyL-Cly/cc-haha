import fs from 'node:fs'
import * as exports from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual Agent memory directory')
const profiles = ['user-zero', 'user-cache-on', 'user-cache-off', 'user-disabled', 'relative-zero', 'empty-zero', 'project-zero', 'local-zero', 'remote-zero']
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_CONFIG_AGENT_MEMORY_SOURCE !== '1' || !profiles.includes(input.profile)) throw new Error('Pinned declared Agent memory profile required')
let project = join(home, 'project')
const initial = join(home, '.claude')
const next = input.profile === 'relative-zero' ? 'owned-next' : input.profile === 'empty-zero' ? '' : join(home, 'owned-next')
const nextPhysical = resolve(project, next)
const scope = input.profile === 'project-zero' ? 'project' : input.profile === 'local-zero' ? 'local' : 'user'
const initialSettings = { autoMemoryEnabled: input.profile !== 'user-cache-off', futureMemory: { initial: true } }
const nextSettings = { autoMemoryEnabled: input.profile === 'user-cache-off', futureMemory: { next: true } }
const disabled = input.profile === 'user-disabled' ? '1' : input.profile.includes('cache') ? undefined : '0'
const remote = input.profile === 'remote-zero' ? join(home, 'actual-remote-memory') : undefined
const body = 'ACTUAL_CACHED_MEMORY_AGENT_BODY'
const markdown = `---\nname: memory-worker\ndescription: Actual persistent memory worker\nmemory: ${scope}\n---\n${body}\n`
for (const [directory, settings] of [[initial, initialSettings], [nextPhysical, nextSettings]] as const) {
  fs.mkdirSync(join(directory, 'agents'), { recursive: true })
  fs.writeFileSync(join(directory, 'agents', 'worker.md'), markdown)
  fs.writeFileSync(join(directory, 'settings.json'), JSON.stringify(settings))
}
fs.mkdirSync(join(project, '.git'), { recursive: true })
fs.mkdirSync(join(project, '.claude'), { recursive: true })
fs.writeFileSync(join(project, '.claude', 'settings.json'), '{}')
const oldDirectory = scope === 'project' ? join(project, '.claude', 'agent-memory', 'memory-worker') : scope === 'local' ? join(project, '.claude', 'agent-memory-local', 'memory-worker') : join(remote ?? initial, 'agent-memory', 'memory-worker')
const newDirectory = scope === 'user' && !remote ? join(nextPhysical, 'agent-memory', 'memory-worker') : oldDirectory
const initialText = 'ACTUAL_MEMORY_CONTENT_OLD\n'
const nextText = 'ACTUAL_MEMORY_CONTENT_NEXT\n'
const rewrittenText = 'ACTUAL_MEMORY_CONTENT_REWRITTEN\n'
for (const [directory, text] of [[oldDirectory, initialText], ...(newDirectory === oldDirectory ? [] : [[newDirectory, nextText]])] as const) {
  fs.mkdirSync(directory, { recursive: true })
  fs.writeFileSync(join(directory, 'MEMORY.md'), text)
}
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
;(globalThis as any).MACRO = { VERSION: '999.0.0-agent-memory-directory', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
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
process.env.CLAUDE_CONFIG_DIR = initial
if (disabled === undefined) delete process.env.CLAUDE_CODE_DISABLE_AUTO_MEMORY
else process.env.CLAUDE_CODE_DISABLE_AUTO_MEMORY = disabled
if (remote) process.env.CLAUDE_CODE_REMOTE_MEMORY_DIR = remote
else delete process.env.CLAUDE_CODE_REMOTE_MEMORY_DIR
const { getInitialSettings } = await import('../../../src/utils/settings/settings')
const { resetSettingsCache } = await import('../../../src/utils/settings/settingsCache')
const cachedSettings = getInitialSettings()
const { getAgentDefinitionsWithOverrides, clearAgentDefinitionsCache } = await import('../../../src/tools/AgentTool/loadAgentsDir')
const { getAgentMemoryDir } = await import('../../../src/tools/AgentTool/agentMemory')
const reads: string[] = []
const readFile = fs.readFileSync
const read = (...args: any[]) => {
  if (String(args[0]).endsWith('MEMORY.md')) reads.push(String(args[0]))
  return (readFile as any)(...args)
}
fs.readFileSync = read as any
spyOn(exports, 'readFileSync').mockImplementation(read as any)
const before = await getAgentDefinitionsWithOverrides(project)
const agent = (result: any) => result.allAgents.find((definition: any) => definition.agentType === 'memory-worker')
if (!agent(before)) throw new Error('Actual memory agent missing')
const stages: any[] = []
const capture = (phase: string, result: any) => {
  const definition = agent(result)
  const start = reads.length
  stages.push({ phase, directory: getAgentMemoryDir('memory-worker', scope), prompt: definition.getSystemPrompt(), sourceFilePath: definition.sourceFilePath, reads: reads.slice(start), sameDefinition: definition === agent(before), sameMergedSettings: cachedSettings === getInitialSettings() })
}
capture('initial', before)
process.env.CLAUDE_CONFIG_DIR = next
const warm = await getAgentDefinitionsWithOverrides(project)
capture('changed-cached-definition', warm)
fs.writeFileSync(join(newDirectory, 'MEMORY.md'), rewrittenText)
capture('same-definition-physical-rewrite', warm)
clearAgentDefinitionsCache()
const cleared = await getAgentDefinitionsWithOverrides(project)
capture('agent-clear', cleared)
resetSettingsCache()
capture('settings-reset-same-definition', cleared)
if (caps.blocked.length || caps.secureReads.length || before.failedFiles?.length || cleared.failedFiles?.length || !stages.slice(0, 4).every(stage => stage.sameMergedSettings) || stages[4].sameMergedSettings) throw new Error('Actual Agent memory consumer escaped ' + JSON.stringify({ caps, stages }))
const output = JSON.stringify({ initial, next, nextPhysical, project, scope, disabled: disabled ?? null, remote: remote ?? null, initialSettings, nextSettings, body, oldDirectory, newDirectory, initialText, nextText, rewrittenText, stages, sameWarmResult: before === warm, differentClearedResult: before !== cleared, caps, proof: 'whole frozen loader/cache/getSystemPrompt/isAutoMemoryEnabled/buildMemoryPrompt; actual private file contents, no business replacement' })
await new Promise<void>((resolve, reject) => process.stdout.write(output, error => error ? reject(error) : resolve()))
process.exit(0)
