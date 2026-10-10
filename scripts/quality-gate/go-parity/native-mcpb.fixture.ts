import { assertDisposableFixtureHome } from './fixture-guard'
import { mock } from 'bun:test'
import { readFileSync, mkdirSync, writeFileSync, utimesSync, rmSync, readdirSync, statSync, existsSync } from 'node:fs'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'

const home = assertDisposableFixtureHome(process.env.HOME, 'Native MCPB Source comparison')
if (process.env.CC_HAHA_MCPB_FIXTURE !== '1') throw new Error('Missing MCPB fixture marker')
const sdkPath = process.env.CC_HAHA_MCPB_SOURCE_SDK
if (!sdkPath || JSON.parse(readFileSync(join(sdkPath, 'package.json'), 'utf8')).version !== '1.2.0'
  || JSON.parse(readFileSync(join(sdkPath, 'node_modules/zod/package.json'), 'utf8')).version !== '3.25.67') {
  throw new Error('MCPB Source fixture requires the verified compatible 1.2.0 SDK and Zod 3.25.67')
}
// The original repository omits its MCPB dependency. Resolve that missing
// module to untouched compatible published SDK exports; no parser/config
// business is mocked. This is an explicit reference baseline, not evidence
// that the original checkout had this SDK installed.
const schemas = await import(pathToFileURL(join(sdkPath, 'dist/schemas/latest.js')).href)
const compiler = await import(pathToFileURL(join(sdkPath, 'dist/shared/config.js')).href)
mock.module('@anthropic-ai/mcpb', () => ({ ...schemas, ...compiler }))
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
if (input.loadFixture) {
  const config = join(home, '.claude')
  mkdirSync(config, { recursive: true })
  mkdirSync(join(home, 'managed'), { recursive: true })
  writeFileSync(join(config, 'settings.json'), input.loadFixture.settings)
  writeFileSync(join(config, '.credentials.json'), input.loadFixture.credentials)
  process.env.USER_TYPE = 'ant'
  process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH = join(home, 'managed')
  const { getManagedFilePath } = await import('../../../src/utils/settings/managedPath')
  if (getManagedFilePath() !== join(home, 'managed')) throw new Error('Unowned managed path')
  process.env.USER_TYPE = 'external'
  const portable = await import('../../../src/utils/execFileNoThrowPortable')
  const execa = await import('execa')
  mock.module('../../../src/utils/execFileNoThrowPortable', () => ({ ...portable, execSyncWithDefaults_DEPRECATED() { throw new Error('isolated unavailable Keychain') } }))
  mock.module('execa', () => ({ ...execa, execaSync() { return { exitCode: 1 } } }))
  const state = await import('../../../src/bootstrap/state')
  state.setOriginalCwd(home); state.setProjectRoot(home); state.setCwdState(home)
  state.setAllowedSettingSources(['userSettings', 'projectSettings', 'localSettings', 'policySettings'])
  const { enableConfigs } = await import('../../../src/utils/config')
  enableConfigs()
  const OriginalDate = Date
  const clock = input.loadFixture.clock
  globalThis.Date = class extends OriginalDate {
    constructor(...args: any[]) { super(...(args.length ? args : [clock]) as [any]) }
    static now() { return clock }
  } as DateConstructor
}
const helpers = await import('../../../src/utils/dxt/helpers')
const handler = await import('../../../src/utils/plugins/mcpbHandler')
const directories = await import('../../../src/utils/systemDirectories')
const observations = []
for (const row of input.cases) {
  try {
    let value: unknown
    const warnings: unknown[] = []
    if (row.action === 'manifest') value = await helpers.parseAndValidateManifestFromBytes(new TextEncoder().encode(row.raw))
    else if (row.action === 'validate') value = handler.validateUserConfig(row.values, row.schema)
    else if (row.action === 'dirs') value = directories.getSystemDirectories({ env: row.env, homedir: home, platform: row.platform })
    else if (row.action === 'load' || row.action === 'changed') {
      for (const [path, data] of Object.entries(row.files ?? {})) {
        mkdirSync(join(path, '..'), { recursive: true })
        writeFileSync(path, Buffer.from(data as string, 'base64'))
        utimesSync(path, 1, row.mtime ?? 1)
      }
      for (const path of row.remove ?? []) rmSync(path, { recursive: true, force: true })
      const progress: string[] = []
      let failure: string | null = null
      try {
        value = row.action === 'load' ? await handler.loadMcpbFile(row.source, row.pluginPath, row.pluginID, s => progress.push(s), row.provided, row.force) : await handler.checkMcpbChanged(row.source, row.pluginPath)
      } catch (error) { failure = (error as Error).message }
      const files: Record<string, unknown> = {}
      const visit = (path: string, relative: string) => {
        if (!existsSync(path)) return
        for (const name of readdirSync(path).sort()) {
          const full = join(path, name), key = relative ? `${relative}/${name}` : name
          if (statSync(full).isDirectory()) visit(full, key)
          else files[key] = { bytes: readFileSync(full).toString('base64'), executable: Boolean(statSync(full).mode & 0o111) }
        }
      }
      visit(join(row.pluginPath, '.mcpb-cache'), '')
      const config = join(home, '.claude')
      observations.push({ value: value ?? null, error: failure, progress, files, settings: JSON.parse(readFileSync(join(config, 'settings.json'), 'utf8')), credentials: JSON.parse(readFileSync(join(config, '.credentials.json'), 'utf8')) })
      continue
    }
    else if (row.action === 'config') {
      const original = Object.getOwnPropertyDescriptor(process, 'platform')!
      const originalWarn = console.warn
      Object.defineProperty(process, 'platform', { ...original, value: row.platform })
      console.warn = (_message, detail) => warnings.push({ key: detail.key, replacement: detail.replacement })
      try { value = await compiler.getMcpConfigForManifest({ manifest: row.manifest, extensionPath: row.extensionPath, systemDirs: row.systemDirs, userConfig: row.userConfig, pathSeparator: row.pathSeparator }) }
      finally { Object.defineProperty(process, 'platform', original); console.warn = originalWarn }
    } else throw new Error('Undeclared MCPB fixture operation')
    observations.push({ value: value ?? null, warnings, error: null })
  } catch (error) { observations.push({ value: null, warnings: [], error: (error as Error).message }) }
}
process.stdout.write(JSON.stringify(observations))
