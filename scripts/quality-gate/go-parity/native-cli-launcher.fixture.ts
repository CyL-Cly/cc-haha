import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from './client-options-capabilities'
const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.homeDir, 'Native CLI Launcher')
if (process.env.CC_HAHA_NATIVE_CLI_LAUNCHER_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Unowned CLI launcher fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
;(globalThis as any).MACRO = { BUILD_TIME: '' }
const service = await import('../../../src/server/services/desktopCliLauncherService.js')
const formatting = input.formatting.map((row: any) => ({
  command: service.getDesktopCliCommandName(row.platform),
  pathPresent: service.isPathEntryPresent(row.pathValue, row.binDir, row.platform, home),
  block: service.buildManagedPathBlock(row.shell, row.binDir, home),
  upserted: service.upsertManagedPathBlock(row.existing, row.block),
  windowsWrapper: service.buildWindowsLauncherWrapper(row.source),
}))
const statuses = []
for (const row of input.ensure ?? []) {
  for (const key of ['CLAUDE_CLI_PATH', 'SHELL', 'ZDOTDIR', 'PATH']) {
    if (row.environment[key] === undefined) delete process.env[key]
    else process.env[key] = row.environment[key]
  }
  const status = await service.ensureDesktopCliLauncherInstalled()
  statuses.push(status)
}
process.stdout.write(JSON.stringify({ formatting, statuses, capabilities }) + '\n')
