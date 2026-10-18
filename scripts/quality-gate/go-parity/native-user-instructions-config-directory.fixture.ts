import fs from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual user instructions configuration directory')
const profiles = ['absent', 'absolute', 'relative', 'empty', 'relative-parent', 'decomposed', 'user-disabled', 'managed-only', 'bare', 'held-managed-read', 'held-user-read']
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_USER_INSTRUCTIONS_DIRECTORY_SOURCE !== '1' || !profiles.includes(input.profile)) throw new Error('Pinned declared user-instructions Source profile required')
const project = join(home, 'owned-instructions-project')
fs.mkdirSync(join(project, '.git'), { recursive: true })
const requested = input.profile === 'absent' ? undefined
  : input.profile === 'absolute' ? join(home, 'owned-instructions-config')
  : input.profile === 'decomposed' ? join(home, 'owned-instructions-e\u0301')
  : input.profile === 'relative-parent' ? '../owned-instructions-parent'
  : input.profile === 'empty' ? '' : 'owned-instructions-relative'
const nextRequested = 'owned-instructions-next'
const initialPhysical = resolve(project, (requested ?? join(home, '.claude')).normalize('NFC'))
const nextPhysical = resolve(project, nextRequested)
const settings = input.profile === 'managed-only'
  ? { pluginConfigs: { 'agents-md@builtin': { options: { instructionFiles: 'managed-only' } } } }
  : {}
for (const [directory, label] of [[initialPhysical, 'INITIAL'], [nextPhysical, 'NEXT']] as const) {
  fs.mkdirSync(join(directory, 'rules', 'nested'), { recursive: true })
  fs.writeFileSync(join(directory, 'settings.json'), JSON.stringify(settings) + '\n')
  fs.writeFileSync(join(directory, 'CLAUDE.md'), `ACTUAL_USER_CLAUDE_${label}\n`)
  fs.writeFileSync(join(directory, 'rules', 'plain.md'), `ACTUAL_USER_RULE_${label}\n`)
  fs.writeFileSync(join(directory, 'rules', 'nested', 'child.md'), `ACTUAL_USER_NESTED_RULE_${label}\n`)
}
const managed = join(home, 'owned-managed-instructions')
fs.mkdirSync(managed, { recursive: true })
const managedFile = join(managed, 'CLAUDE.md')
fs.writeFileSync(managedFile, 'ACTUAL_MANAGED_INSTRUCTIONS\n')
const absent = absentManagedSettingsFixturePaths()
for (const spelling of new Set([home, fs.realpathSync(home)])) {
  for (let path = dirname(spelling); ; path = dirname(path)) {
    for (const leaf of ['.git', 'CLAUDE.md', '.claude/CLAUDE.md', '.claude/rules', 'CLAUDE.local.md', 'AGENTS.md', '.claude/AGENTS.md']) absent.push(join(path, leaf))
    if (dirname(path) === path) break
  }
}
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent })
// Substitute only the absent external MDM OS store; all settings parsing,
// configuration paths, memory discovery, cache, and formatting stay original.
mock.module('../../../src/utils/settings/mdm/rawRead', () => ({
  startMdmRawRead: () => {},
  getMdmRawReadPromise: () => Promise.resolve({ plistStdouts: [], hklmStdout: null, hkcuStdout: null }),
  fireRawRead: async () => ({ plistStdouts: [], hklmStdout: null, hkcuStdout: null }),
}))
;(globalThis as any).MACRO = { VERSION: '999.0.0-user-instructions-directory', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
process.chdir(project)
const state = await import('../../../src/bootstrap/state')
state.setCwdState(process.cwd())
state.setOriginalCwd(process.cwd())
state.setProjectRoot(process.cwd())
state.setIsInteractive(false)
state.setAllowedSettingSources(input.profile === 'user-disabled' ? [] : ['userSettings'])
if (requested === undefined) delete process.env.CLAUDE_CONFIG_DIR
else process.env.CLAUDE_CONFIG_DIR = requested
delete process.env.CLAUDE_CODE_DISABLE_AUTO_MEMORY
delete process.env.CLAUDE_CODE_DISABLE_CLAUDE_MDS
if (input.profile === 'bare') process.env.CLAUDE_CODE_SIMPLE = '1'
else delete process.env.CLAUDE_CODE_SIMPLE
// Cache the original owned managed-path override before returning to the
// external profile, so the real managed reader can use private physical files.
process.env.USER_TYPE = 'ant'
process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = managed
const managedPath = await import('../../../src/utils/settings/managedPath')
if (managedPath.getManagedFilePath() !== managed) throw new Error('Original private managed-path binding failed')
process.env.USER_TYPE = 'external'
const config = await import('../../../src/utils/config')
config.enableConfigs()
const paths = await import('../../../src/memdir/paths')
const memdir = await import('../../../src/memdir/memdir')
const instructionMode = await import('../../../src/utils/instructionFiles')
const instructions = await import('../../../src/utils/claudemd')
const context = await import('../../../src/context')
const operations = (await import('../../../src/utils/fsOperations')).getFsImplementation()
const originalRead = operations.readFile.bind(operations)
const reads: string[] = []
let reached!: () => void, release!: () => void
const started = new Promise<void>(resolve => { reached = resolve })
const held = new Promise<void>(resolve => { release = resolve })
const holdPath = input.profile === 'held-managed-read' ? managedFile
  : input.profile === 'held-user-read' ? config.getMemoryPath('User') : undefined
let holdConsumed = false
operations.readFile = ((...args: any[]) => {
  const path = String(args[0])
  reads.push(path)
  const result = (originalRead as any)(...args)
  if (holdPath && path === holdPath && !holdConsumed) {
    holdConsumed = true
    return result.then(async (body: unknown) => {
      reached()
      await held
      return body
    })
  }
  return result
}) as typeof operations.readFile
const initialMode = instructionMode.getInstructionFilesMode()
const autoEnabled = paths.isAutoMemoryEnabled()
const initialPrompt = await memdir.loadMemoryPrompt()
const pending = context.getUserContext()
if (holdPath) {
  await started
  process.env.CLAUDE_CONFIG_DIR = nextRequested
  release()
}
const firstContext = await pending
const firstReads = reads.slice()
const initialFiles = firstContext.claudeMd ? await instructions.getMemoryFiles() : null
if (!holdPath) process.env.CLAUDE_CONFIG_DIR = nextRequested
const warmContext = await context.getUserContext()
const warmFiles = initialFiles ? await instructions.getMemoryFiles() : null
instructions.clearMemoryFileCaches()
const contextAfterFilesClear = await context.getUserContext()
context.getUserContext.cache.clear()
const nextContext = await context.getUserContext()
const nextFiles = nextContext.claudeMd ? await instructions.getMemoryFiles() : null
operations.readFile = originalRead
if (caps.blocked.length || caps.secureReads.length || firstContext !== warmContext || firstContext !== contextAfterFilesClear || initialFiles !== warmFiles || nextContext === firstContext) throw new Error('Original instruction consumer/cache escaped: ' + JSON.stringify({ caps, firstContext, nextContext }))
process.stdout.write(JSON.stringify({ profile: input.profile, requested: requested ?? null, nextRequested, project: process.cwd(), initialPhysical, nextPhysical, managed, initialMode, autoEnabled, initialPrompt, firstContext, initialFiles, firstReads, nextContext, nextFiles, reads, holdConsumed, sameWarmContext: firstContext === warmContext, sameContextAfterFilesClear: firstContext === contextAfterFilesClear, sameWarmFiles: initialFiles === warmFiles, nextContextDifferent: nextContext !== firstContext, caps, proof: 'Whole frozen getUserContext/getMemoryFiles/getClaudeMds/loadMemoryPrompt with default enable/mode; actual private managed/user/rules filesystem and independent caches; held profiles delay only genuine read completion' }) + '\n')
process.exit(0)
