import fs from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual auto-memory configuration directory')
const profiles = ['absent', 'absolute', 'relative', 'empty', 'decomposed', 'relative-parent']
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_AUTO_MEMORY_DIRECTORY_SOURCE !== '1' || !profiles.includes(input.profile)) throw new Error('Pinned declared auto-memory Source profile required')
const project = join(home, 'owned-memory-project')
fs.mkdirSync(join(project, '.git'), { recursive: true })
const requested = input.profile === 'absent' ? undefined
  : input.profile === 'absolute' ? join(home, 'owned-memory-config')
  : input.profile === 'decomposed' ? join(home, 'owned-memory-e\u0301')
  : input.profile === 'relative-parent' ? '../owned-memory-parent'
  : input.profile === 'empty' ? '' : 'owned-memory-relative'
const nextRequested = 'owned-memory-next'
const initialPhysical = resolve(project, (requested ?? join(home, '.claude')).normalize('NFC'))
const nextPhysical = resolve(project, nextRequested)
for (const directory of [initialPhysical, nextPhysical]) {
  fs.mkdirSync(directory, { recursive: true })
  fs.writeFileSync(join(directory, 'settings.json'), '{}\n')
}
const absent = absentManagedSettingsFixturePaths()
const managed = dirname(absent[0]!)
absent.push(join(managed, 'CLAUDE.md'), join(managed, '.claude/rules'))
for (const spelling of new Set([home, fs.realpathSync(home)])) {
  for (let path = dirname(spelling); ; path = dirname(path)) {
    for (const leaf of ['.git', 'CLAUDE.md', '.claude/CLAUDE.md', '.claude/rules', 'CLAUDE.local.md', 'AGENTS.md', '.claude/AGENTS.md']) absent.push(join(path, leaf))
    if (dirname(path) === path) break
  }
}
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent })
;(globalThis as any).MACRO = { VERSION: '999.0.0-auto-memory-directory', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
process.chdir(project)
const state = await import('../../../src/bootstrap/state')
state.setCwdState(process.cwd())
state.setOriginalCwd(process.cwd())
state.setProjectRoot(process.cwd())
state.setIsInteractive(false)
state.setAllowedSettingSources(['userSettings'])
if (requested === undefined) delete process.env.CLAUDE_CONFIG_DIR
else process.env.CLAUDE_CONFIG_DIR = requested
process.env.CLAUDE_CODE_DISABLE_AUTO_MEMORY = '0'
const config = await import('../../../src/utils/config')
config.enableConfigs()
const paths = await import('../../../src/memdir/paths')
const memory = await import('../../../src/memdir/memdir')
const instructions = await import('../../../src/utils/claudemd')
const settings = await import('../../../src/utils/settings/settings')
const initialSettings = settings.getInitialSettings()
const initialPrompt = await memory.loadMemoryPrompt()
const initialDirectory = paths.getAutoMemPath()
const initialEntrypoint = paths.getAutoMemEntrypoint()
const initialText = 'ACTUAL_AUTO_MEMORY_INITIAL_CONTENT\n'
fs.writeFileSync(initialEntrypoint, initialText)
const initialFiles = await instructions.getMemoryFiles()
const stages = []
const capture = async (phase: string) => {
  const prompt = await memory.loadMemoryPrompt()
  const files = await instructions.getMemoryFiles()
  const directory = paths.getAutoMemPath()
  stages.push({ phase, directory, physical: resolve(directory), prompt, files, context: instructions.getClaudeMds(files), sameFiles: files === initialFiles, sameSettings: settings.getInitialSettings() === initialSettings, directoryExists: fs.statSync(directory).isDirectory() })
}
await capture('initial')
process.env.CLAUDE_CONFIG_DIR = nextRequested
await capture('changed-warm')
paths.getAutoMemPath.cache.clear()
const nextPrompt = await memory.loadMemoryPrompt()
const nextDirectory = paths.getAutoMemPath()
const nextText = 'ACTUAL_AUTO_MEMORY_NEXT_CONTENT\n'
fs.writeFileSync(paths.getAutoMemEntrypoint(), nextText)
await capture('path-cache-clear')
instructions.clearMemoryFileCaches()
await capture('instruction-cache-clear')
if (caps.blocked.length || caps.secureReads.length || initialPrompt !== stages[0]!.prompt || nextPrompt !== stages[2]!.prompt || !stages.every(stage => stage.directoryExists && stage.sameSettings) || !stages.slice(0, 3).every(stage => stage.sameFiles) || stages[3]!.sameFiles) throw new Error('Original auto-memory consumer escaped or lost cache identity: ' + JSON.stringify({ caps, stages }))
process.stdout.write(JSON.stringify({ profile: input.profile, requested: requested ?? null, project: process.cwd(), initialPhysical, nextRequested, nextPhysical, initialDirectory, initialEntrypoint, initialText, nextDirectory, nextText, stages, caps, proof: 'Whole frozen loadMemoryPrompt/getAutoMemPath/getMemoryFiles/getClaudeMds with actual mkdir/read/write; distinct path and instruction caches, no memory/settings body replacement' }) + '\n')
process.exit(0)
