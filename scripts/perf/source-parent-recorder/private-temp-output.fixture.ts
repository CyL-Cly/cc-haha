import fs from 'node:fs'
import { dirname, isAbsolute, join, relative, resolve, sep } from 'node:path'

const repository = process.env.CC_HAHA_PRIVATE_TEMP_REPOSITORY_ROOT ?? resolve(import.meta.dir, '../../..')
if (!isAbsolute(repository) || fs.realpathSync(repository) !== repository || process.env.CC_HAHA_PRIVATE_TEMP_OUTPUT_FIXTURE !== '1' || Bun.version !== '1.3.14') throw new Error('Pinned private task-output fixture required')
const input = await Bun.stdin.json()
const { assertDisposableFixtureHome } = await import(join(repository, 'scripts/quality-gate/go-parity/fixture-guard.ts'))
const { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } = await import(join(repository, 'scripts/quality-gate/go-parity/client-options-capabilities.ts'))
const home = assertDisposableFixtureHome(input.home, 'Benchmark actual Bash task output')
if (!['compute', 'write'].includes(input.action)) throw new Error('Explicit output action required')
const absent = absentManagedSettingsFixturePaths()
for (let path = home; ; path = dirname(path)) {
  const git = join(path, '.git')
  if (!fs.existsSync(git)) absent.push(git)
  if (dirname(path) === path) break
}
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent })
;(globalThis as any).MACRO = { VERSION: '2.1.288', BUILD_TIME: '', PACKAGE_URL: '' }
await import(join(repository, 'preload.ts'))
const bootstrap = await import(join(repository, 'src/bootstrap/state.ts'))
bootstrap.setCwdState(home)
bootstrap.setOriginalCwd(home)
bootstrap.setProjectRoot(home)
bootstrap.setAllowedSettingSources(['userSettings'])
bootstrap.setIsInteractive(false)
bootstrap.switchSession('79797979-7979-4797-8797-797979797979')
process.env.USER_TYPE = 'ant'
const settings = await import(join(repository, 'src/utils/settings/settings.ts'))
settings.getInitialSettings()
process.env.USER_TYPE = 'external'
const config = await import(join(repository, 'src/utils/config.ts'))
config.enableConfigs()
const disk = await import(join(repository, 'src/utils/task/diskOutput.ts'))
const outputDirectory = disk.getTaskOutputDir()
const below = relative(join(home, 'tmp'), outputDirectory)
const privateDirectory = !!below && below !== '..' && !below.startsWith('..' + sep) && !isAbsolute(below)
let outputPath: string | null = null, bytes: string | null = null
// Negative cases only compute the real consumer path. Never mkdir outside HOME.
if (input.action === 'write') {
  if (!privateDirectory) throw new Error('Actual task output resolved outside private temporary root; no output write attempted: ' + outputDirectory)
  outputPath = await disk.initTaskOutput('benchmark-private-temp')
  disk.appendTaskOutput('benchmark-private-temp', 'BENCHMARK_PRIVATE_TASK_OUTPUT\n')
  await disk.flushTaskOutput('benchmark-private-temp')
  bytes = fs.readFileSync(outputPath, 'utf8')
}
await disk._clearOutputsForTest()
const result = { action: input.action, outputDirectory, privateDirectory, outputPath, bytes, blocked: caps.blocked, secureReads: caps.secureReads, taskOutputWriteAttempted: input.action === 'write', globalTaskOutputWriteAttempted: false }
await new Promise<void>(resolve => process.stdout.write(JSON.stringify(result), () => resolve()))
process.exit(0)
