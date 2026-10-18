import fs from 'node:fs'
import fsp from 'node:fs/promises'
import { createRequire } from 'node:module'
import { dirname, join } from 'node:path'
import { pathToFileURL } from 'node:url'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual terminal setup command')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TERMINAL_SETUP_SOURCE !== '1') throw new Error('Pinned terminal setup Source required')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
const appleExecutables = input.apple ? ['defaults', 'killall', 'PlistBuddy'].map(name => join(home, 'privatebin', name)) : []
const caps = await installClientFixtureCapabilities(home, {
  absentPaths: absentManagedSettingsFixturePaths(), privateExecutableFiles: appleExecutables,
  ...(input.apple ? { approvedSpawn: (request: any) => appleExecutables.includes(request.command) && request.args.every((value: unknown) => typeof value === 'string') } : {}),
})
;(globalThis as any).MACRO = { VERSION: '999.0.0-terminal-setup', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(home)
bootstrap.setOriginalCwd(home)
bootstrap.setProjectRoot(home)
bootstrap.setAllowedSettingSources(['userSettings'])
bootstrap.setIsInteractive(true)
const config = await import('../../../src/utils/config')
config.enableConfigs()
config.saveGlobalConfig(current => ({ ...current, retainedSetupFixture: { value: 'unknown old field' }, ...input.global }))
const { addItemToJSONCArray, safeParseJSONC } = await import('../../../src/utils/json')
function original(path: string, dependencies: Record<string, unknown>, returned: string) {
  const source = ts.createSourceFile(path, fs.readFileSync(new URL(path, import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
  const body = source.statements.filter((node: any) => !ts.isImportDeclaration(node) && !ts.isExportAssignment(node)).map((node: any) => node.getText(source).replace(/^export /, '')).join('\n')
  const js = ts.transpileModule(body, { compilerOptions: { target: ts.ScriptTarget.ESNext, module: ts.ModuleKind.ESNext, jsx: ts.JsxEmit.React } }).outputText
  return new Function(...Object.keys(dependencies), js + '\nreturn ' + returned)(...Object.values(dependencies))
}
function sourceColors(row: any, Chalk: any, getTheme: any) {
  const observedProcess = { env: row.env, platform: row.platform, argv: row.args }
  const dependencies = { process: observedProcess, os: { release: () => row.release }, tty: { isatty: () => row.tty } }
  const chalkLibrary = original('../../../node_modules/chalk/source/vendor/supports-color/index.js', dependencies, 'createSupportsColor')
  const hyperlinkLibrary = original('../../../node_modules/supports-color/index.js', dependencies, 'createSupportsColor')
  const chalkSupport = chalkLibrary({ isTTY: row.tty })
  const hyperlinkSupport = hyperlinkLibrary({ isTTY: row.tty })
  const chalk = new Chalk({ level: 0 })
  chalk.level = chalkSupport ? chalkSupport.level : 0
  const colorize = original('../../../src/ink/colorize.ts', { process: observedProcess, chalk }, 'colorize')
  const color = original('../../../src/components/design-system/color.ts', { colorize, getTheme }, 'color')
  return { chalkSupport, hyperlinkSupport, inkLevel: chalk.level, chalk, color, observedProcess }
}
if (input.jsonc) {
  const values = input.jsonc.map((content: string) => ({ content, parsed: safeParseJSONC(content), modified: addItemToJSONCArray(content, input.item) }))
  await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ values, capabilities: caps }), error => error ? reject(error) : resolve()))
  process.exit(0)
}
if (input.colorRows) {
  const { Chalk } = await import('chalk')
  const { getTheme } = await import('../../../src/utils/theme')
  const values = input.colorRows.map((row: any) => {
    const { chalkSupport, hyperlinkSupport, inkLevel, chalk, color } = sourceColors(row, Chalk, getTheme)
    return {
      chalkSupport, hyperlinkSupport, inkLevel,
      roles: row.roles.map((role: string) => color(role, row.theme)(row.text)),
      dim: chalk.dim(row.text),
    }
  })
  if (caps.blocked.length || caps.secureReads.length) throw new Error('Color Source escaped private fixture')
  await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ values, capabilities: caps }), error => error ? reject(error) : resolve()))
  process.exit(0)
}
if (input.hyperlinkRows) {
  const { default: hasFlag } = await import('has-flag')
  const values = input.hyperlinkRows.map((row: any) => {
    const observedProcess = { env: row.env, platform: row.platform }
    const library = original('../../../node_modules/supports-hyperlinks/index.js', { process: observedProcess, hasFlag: (flag: string) => hasFlag(flag, row.args), createSupportsColor: () => row.color ? { level: 1 } : false }, 'createSupportsHyperlinks')
    const librarySupported = library({ isTTY: row.tty })
    const wrapper = original('../../../src/ink/supports-hyperlinks.ts', { process: observedProcess, supportsHyperlinksLib: { stdout: librarySupported } }, 'supportsHyperlinks')
    return { library: librarySupported, extended: wrapper() }
  })
  if (caps.blocked.length || caps.secureReads.length) throw new Error('Hyperlink Source escaped private fixture')
  await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ values, capabilities: caps }), error => error ? reject(error) : resolve()))
  process.exit(0)
}
if (input.terminal === 'Apple_Terminal' && !input.apple) throw new Error('Apple setup requires explicit private OS command fixture before imports')
for (const key of ['XDG_CONFIG_HOME', 'APPDATA']) {
  if (input.environment?.[key]) process.env[key] = join(home, input.environment[key])
}
for (const key of ['PATH', 'VSCODE_GIT_ASKPASS_MAIN']) {
  if (input.environment?.[key] !== undefined) process.env[key] = input.environment[key]
}
for (const [relative, value] of Object.entries(input.files ?? {})) {
  if (relative.split('/').some(part => part === '..') || relative.startsWith('/')) throw new Error('Invalid private terminal setup path')
  await fsp.mkdir(dirname(join(home, relative)), { recursive: true })
  await fsp.writeFile(join(home, relative), value as string)
}
const environment = { terminal: input.terminal || null }
let paintChalk = { dim: (value: string) => value }
let paintColor = () => (value: string) => value
let hyperlinkSupport = () => input.hyperlinks === true
if (input.paint) {
  const { Chalk } = await import('chalk')
  const { getTheme } = await import('../../../src/utils/theme')
  const { default: hasFlag } = await import('has-flag')
  const paint = sourceColors(input.paint, Chalk, getTheme)
  paintChalk = paint.chalk
  paintColor = paint.color
  const library = original('../../../node_modules/supports-hyperlinks/index.js', { process: paint.observedProcess, hasFlag: (flag: string) => hasFlag(flag, input.paint.args), createSupportsColor: () => paint.hyperlinkSupport }, 'createSupportsHyperlinks')
  const librarySupported = library({ isTTY: input.paint.tty })
  hyperlinkSupport = original('../../../src/ink/supports-hyperlinks.ts', { process: paint.observedProcess, supportsHyperlinksLib: { stdout: librarySupported } }, 'supportsHyperlinks')
}
let onboarding = 0
const errors: string[] = []
const executions: unknown[] = []
const logError = (error: Error) => { errors.push(error.message) }
let apple: Record<string, unknown> = {}
if (input.apple) {
  await fsp.mkdir(join(home, 'tmp'), { recursive: true })
  const { execFileNoThrow } = await import('../../../src/utils/execFileNoThrow')
  const execute = async (name: string, args: string[]) => {
    executions.push({ name, args })
    const command = join(home, 'privatebin', name === '/usr/libexec/PlistBuddy' ? 'PlistBuddy' : name)
    if (!appleExecutables.includes(command)) throw new Error('Unknown private Apple fixture process')
    return await execFileNoThrow(command, args, { env: { ...process.env, TMPDIR: join(home, 'tmp'), CC_HAHA_APPLE_CASE: JSON.stringify(input.apple), CC_HAHA_APPLE_SEED: join(home, 'apple-seed.plist') }, useCwd: true })
  }
  apple = original('../../../src/utils/appleTerminalBackup.ts', { stat: fsp.stat, homedir: () => home, join, getGlobalConfig: config.getGlobalConfig, saveGlobalConfig: config.saveGlobalConfig, execFileNoThrow: execute, logError }, '{ backupTerminalPreferences, checkAndRestoreTerminalBackup, getTerminalPlistPath, markTerminalSetupComplete }')
  apple.execFileNoThrow = execute
}
const call = original('../../../src/commands/terminalSetup/terminalSetup.tsx', {
  chalk: paintChalk, color: paintColor,
  randomBytes: () => Buffer.from('10203040', 'hex'), ...fsp, dirname, join, pathToFileURL,
  homedir: () => home, platform: () => input.platform,
  supportsHyperlinks: hyperlinkSupport,
  maybeMarkProjectOnboardingComplete: () => { onboarding++ },
  getGlobalConfig: config.getGlobalConfig, saveGlobalConfig: config.saveGlobalConfig,
  env: environment, getPlatform: () => input.platform === 'darwin' ? 'macos' : input.platform === 'win32' ? 'windows' : input.platform,
  isFsInaccessible: (error: NodeJS.ErrnoException) => error.code === 'ENOENT' || error.code === 'EACCES',
  addItemToJSONCArray, safeParseJSONC, jsonParse: JSON.parse, jsonStringify: JSON.stringify,
  logError, ...apple,
}, '{ call, shouldOfferTerminalSetup }')
const declaration = original('../../../src/commands/terminalSetup/index.ts', { env: environment }, 'terminalSetup')
const done: unknown[] = []
let error: string | null = null
try {
  await call.call((...values: unknown[]) => done.push(values), { options: { theme: input.theme || 'dark' } }, input.args ?? '')
} catch (caught) { error = (caught as Error).message }
const files: Record<string, string> = {}
async function collect(directory: string, relative = '') {
  // Bun's transpilation cache belongs to the oracle runtime. The physical
  // terminal configuration and backups remain in the byte comparison.
  if (relative === 'Library/Caches/bun' || relative === 'privatebin') return
  for (const entry of await fsp.readdir(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name)
    const name = relative ? relative + '/' + entry.name : entry.name
    if (entry.isDirectory()) await collect(path, name)
    else if (entry.isFile() && !name.startsWith('.claude') && name !== 'apple-seed.plist') files[name] = await fsp.readFile(path, 'utf8')
  }
}
await collect(home)
const global = config.getGlobalConfig()
if (caps.blocked.length || caps.secureReads.length) throw new Error('Terminal setup escaped private fixture ' + JSON.stringify(caps))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ declaration, offered: call.shouldOfferTerminalSetup(), done, error, files, onboarding, executions, errors, global: { shiftEnterKeyBindingInstalled: global.shiftEnterKeyBindingInstalled, optionAsMetaKeyInstalled: global.optionAsMetaKeyInstalled, appleTerminalSetupInProgress: global.appleTerminalSetupInProgress, appleTerminalBackupPath: global.appleTerminalBackupPath, retainedSetupFixture: (global as any).retainedSetupFixture }, capabilities: caps }), caught => caught ? reject(caught) : resolve()))
process.exit(0)
