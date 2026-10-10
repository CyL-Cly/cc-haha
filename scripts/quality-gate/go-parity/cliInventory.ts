import { Command } from '@commander-js/extra-typings'
import { parse } from 'acorn'
import { EventEmitter } from 'node:events'
import fs from 'node:fs'
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { syncBuiltinESMExports } from 'node:module'
import { tmpdir } from 'node:os'
import { dirname, isAbsolute, join, resolve, sep } from 'node:path'
import { PassThrough } from 'node:stream'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'

const rootDir = fileURLToPath(new URL('../../../', import.meta.url))
const childKey = 'CC_HAHA_CLI_INVENTORY_CHILD'
const selfPath = fileURLToPath(import.meta.url)
export const cliBuildFeatures = ['TRANSCRIPT_CLASSIFIER'] as const

// The TypeScript CLI is an original-behavior reference, while the packaged
// sidecar is now Go. Keep its Bun feature set tied to the frozen original
// inventory, rather than requiring a TypeScript production build to survive.
// The actual Go build and no-legacy-sidecar contract have their own desktop
// build-sidecars/go-sidecar-build regression tests and compiled smoke path.
export function assertCliReferenceFeatures(features: unknown) {
  if (!Array.isArray(features) || features.some(value => typeof value !== 'string')
    || JSON.stringify(features) !== JSON.stringify(cliBuildFeatures)) {
    throw new Error('CLI reference feature configuration differs from the frozen original inventory')
  }
}

export function snapshotCommand(command: Command): unknown {
  const internal = command as any
  return {
    name: command.name(), aliases: command.aliases(), description: command.description(),
    hidden: Boolean(internal._hidden), usage: command.usage(), version: command.version() ?? null,
    positionalOptions: Boolean(internal._enablePositionalOptions),
    passThroughOptions: Boolean(internal._passThroughOptions),
    allowUnknownOption: Boolean(internal._allowUnknownOption),
    allowExcessArguments: Boolean(internal._allowExcessArguments),
    arguments: command.registeredArguments.map(argument => ({
      name: argument.name(), description: argument.description, required: argument.required,
      variadic: argument.variadic, hasDefault: argument.defaultValue !== undefined,
      default: argument.defaultValue ?? null, choices: argument.argChoices ?? null,
      parser: Boolean(argument.parseArg),
    })),
    options: command.options.map(option => ({
      flags: option.flags, name: option.name(), attribute: option.attributeName(), description: option.description,
      short: option.short ?? null, long: option.long ?? null, required: option.required,
      optional: option.optional, variadic: Boolean(option.variadic), negate: option.negate,
      mandatory: option.mandatory, hidden: Boolean(option.hidden),
      hasDefault: option.defaultValue !== undefined, default: option.defaultValue ?? null,
      preset: option.presetArg ?? null, choices: option.argChoices ?? null,
      environment: option.envVar ?? null, conflicts: option.conflictsWith ?? [],
      implies: option.implied ?? null, parser: Boolean(option.parseArg),
    })).sort((a, b) => a.flags.localeCompare(b.flags)),
    helpOption: internal._getHelpOption()?.flags ?? null,
    commands: command.commands.map(snapshotCommand).sort((a: any, b: any) => a.name.localeCompare(b.name)),
  }
}

type AstNode = any
function children(node: AstNode): AstNode[] {
  return Object.values(node).flatMap(value => Array.isArray(value) ? value : [value])
    .filter((value: any) => value && typeof value === 'object' && typeof value.type === 'string')
}

function featureValue(node: AstNode): boolean | undefined {
  if (node.type === 'Literal' && typeof node.value === 'boolean') return node.value
  if (node.type === 'CallExpression' && node.callee.name === 'inventoryBuildFeature') {
    if (node.arguments.length !== 1 || typeof node.arguments[0].value !== 'string') throw new Error('Unknown CLI build feature expression')
    return (cliBuildFeatures as readonly string[]).includes(node.arguments[0].value)
  }
  if (node.type === 'LogicalExpression') {
    const left = featureValue(node.left), right = featureValue(node.right)
    if (node.operator === '&&') return left === false || right === false ? false : left === true && right === true ? true : undefined
    if (node.operator === '||') return left === true || right === true ? true : left === false && right === false ? false : undefined
  }
  if (node.type === 'UnaryExpression' && node.operator === '!') {
    const value = featureValue(node.argument)
    return value === undefined ? undefined : !value
  }
  return undefined
}

// Bootstrap switches precede Commander. Parse the production source after Bun's
// TS transform, evaluating only build features; runtime predicates stay explicit.
export function inventoryBootstrap(source: string) {
  // Alias feature before transforming so Bun does not silently fold calls using
  // the inventory parent's feature set. Only this function evaluates the build's
  // explicit feature list; the production child uses the corresponding flags.
  const code = new Bun.Transpiler({ loader: 'tsx', target: 'bun' }).transformSync(source.replace(/\bfeature\s*\(/g, 'inventoryBuildFeature('))
  const tree = parse(code, { ecmaVersion: 'latest', sourceType: 'module' }) as AstNode
  const main = tree.body.find((node: AstNode) => node.type === 'FunctionDeclaration' && node.id.name === 'main')
  if (!main) throw new Error('CLI bootstrap main function was not found')
  const routes: Array<{ tokens: string[]; predicate: string; enabledByBuild: boolean; imports: string[] }> = []
  const tokens = (node: AstNode): string[] => {
    if (node.type === 'CallExpression' && node.callee.name === 'inventoryBuildFeature') return []
    if (node.type === 'Literal' && typeof node.value === 'string') return [node.value]
    return children(node).flatMap(tokens)
  }
  const imports = (node: AstNode): string[] => node.type === 'ImportExpression'
    ? [node.source.value] : children(node).flatMap(imports)
  const visit = (node: AstNode, enabled = true) => {
    if (node.type === 'IfStatement') {
      const values = [...new Set(tokens(node.test))].filter(value => value.startsWith('-') || /^[a-z][a-z-]+$/.test(value))
      if (values.length > 0) routes.push({
        tokens: values.sort(), predicate: code.slice(node.test.start, node.test.end).replaceAll('inventoryBuildFeature(', 'feature('),
        enabledByBuild: enabled && featureValue(node.test) !== false, imports: [...new Set(imports(node.consequent))].sort(),
      })
      visit(node.consequent, enabled && featureValue(node.test) !== false)
      if (node.alternate) visit(node.alternate, enabled && featureValue(node.test) !== true)
      return
    }
    for (const child of children(node)) visit(child, enabled)
  }
  visit(main.body)
  if (routes.length === 0) throw new Error('No CLI bootstrap routes were discovered')
  return routes
}

// Parser sources and rendered pipe help are separate from the compact inventory
// fixture: a boolean parser marker is insufficient to migrate its semantics.
export function snapshotCliRegistration(command: Command): unknown {
  const tree = snapshotCommand(command) as any
  const internal = command as any
  return {
    ...tree, hasAction: Boolean(internal._actionHandler), helpText: command.helpInformation(),
    arguments: command.registeredArguments.map((argument, index) => ({ ...tree.arguments[index], parserSource: argument.parseArg ? String(argument.parseArg) : null })),
    options: tree.options.map((option: any) => {
      const registered = command.options.find(value => value.flags === option.flags)!
      return { ...option, registrationIndex: command.options.indexOf(registered), parserSource: registered.parseArg ? String(registered.parseArg) : null }
    }),
    commands: command.commands.map((child, registrationIndex) => ({ ...(snapshotCliRegistration(child) as any), registrationIndex })).sort((a: any, b: any) => a.name.localeCompare(b.name)),
  }
}

export type CliProbe = { args: string[]; exitCode: number; stdout: string; stderr: string; tree: any; registration: any; parsed: any; blocked: string[]; macro: Record<string, unknown>; bootstrap?: any }

async function installChildGuard() {
  // Bun's source runner consumes a leading -- before the script argv. Restore
  // the probe's explicit application argv before the production entrypoint so
  // transport parsing does not become a false Commander contract.
  const transportArguments = process.argv.slice(2)
  const applicationArguments = JSON.parse(process.env.CC_HAHA_CLI_INVENTORY_ARGS!) as unknown
  if (!Array.isArray(applicationArguments) || applicationArguments.some(value => typeof value !== 'string')) throw new Error('Invalid isolated CLI application argv')
  process.argv = [process.argv[0]!, process.argv[1]!, ...applicationArguments]
  const home = resolve(process.env.HOME!)
  const repository = resolve(process.env.CC_HAHA_CLI_INVENTORY_ROOT!)
  const output = process.env.CC_HAHA_CLI_INVENTORY_OUTPUT!
  const originalWriteFile = fs.writeFileSync.bind(fs)
  const blocked: string[] = []
  let tree: unknown = null
  let registration: unknown = null, parsed: unknown = null
  let captureBootstrap: (() => unknown) | undefined
  const persist = () => originalWriteFile(output, JSON.stringify({ tree, registration, parsed, transportArguments, applicationArguments, blocked, macro: (globalThis as any).MACRO, ...(captureBootstrap ? { bootstrap: captureBootstrap() } : {}) }))
  process.on('exit', persist)
  const deny = (operation: string): never => {
    blocked.push(operation)
    throw Object.assign(new Error(`CLI inventory blocked ${operation}`), { code: 'ENOENT' })
  }
  const allowedPath = (value: any, write = false) => {
    if (typeof value === 'number') return value >= 0 && value <= 2
    if (value instanceof URL) value = fileURLToPath(value)
    if (Buffer.isBuffer(value)) value = value.toString()
    if (typeof value !== 'string') return false
    const absolute = resolve(value)
    const under = (directory: string) => absolute === directory || absolute.startsWith(`${directory}${sep}`)
    return under(home) || (!write && under(repository))
  }
  for (const name of ['readFileSync', 'readdirSync', 'statSync', 'lstatSync', 'realpathSync', 'readlinkSync', 'accessSync', 'openSync']) {
    const original = (fs as any)[name]
    const wrapper: any = (...args: any[]) => {
      if (!allowedPath(args[0], name === 'openSync' && args[1] !== 'r' && args[1] !== undefined && args[1] !== 0)) return deny(`filesystem:${name}`)
      return original(...args)
    }
    if (original.native) wrapper.native = wrapper
    ;(fs as any)[name] = wrapper
  }
  const exists = fs.existsSync
  fs.existsSync = ((value: any) => allowedPath(value) && exists(value)) as typeof fs.existsSync
  for (const name of ['writeFileSync', 'appendFileSync', 'mkdirSync', 'rmSync', 'unlinkSync', 'rmdirSync', 'truncateSync', 'chmodSync', 'chownSync', 'utimesSync', 'symlinkSync', 'linkSync', 'renameSync', 'copyFileSync']) {
    const original = (fs as any)[name]
    ;(fs as any)[name] = (...args: any[]) => {
      if (!allowedPath(args[0], true) || ['symlinkSync', 'linkSync', 'renameSync', 'copyFileSync'].includes(name) && !allowedPath(args[1], true)) return deny(`filesystem:${name}`)
      return original(...args)
    }
  }
  for (const name of ['createReadStream', 'createWriteStream']) {
    const original = (fs as any)[name]
    ;(fs as any)[name] = (...args: any[]) => allowedPath(args[0], name === 'createWriteStream') ? original(...args) : deny(`filesystem:${name}`)
  }
  for (const name of ['readFile', 'readdir', 'stat', 'lstat', 'realpath', 'readlink', 'access', 'open', 'writeFile', 'appendFile', 'mkdir', 'rm', 'unlink', 'rmdir', 'truncate', 'chmod', 'chown', 'utimes', 'symlink', 'link', 'rename', 'copyFile']) {
    const original = (fs as any)[name]
    const write = !['readFile', 'readdir', 'stat', 'lstat', 'realpath', 'readlink', 'access'].includes(name)
    ;(fs as any)[name] = (...args: any[]) => {
      const writes = name === 'open' ? args[1] !== 'r' && args[1] !== 0 : write
      if (allowedPath(args[0], writes) && (!['symlink', 'link', 'rename', 'copyFile'].includes(name) || allowedPath(args[1], true))) return original(...args)
      blocked.push(`filesystem:${name}`)
      const callback = args.at(-1)
      const error = Object.assign(new Error(`CLI inventory blocked filesystem:${name}`), { code: 'ENOENT' })
      if (typeof callback === 'function') { queueMicrotask(() => callback(error)); return }
      throw error
    }
  }
  for (const name of ['readFile', 'readdir', 'stat', 'lstat', 'realpath', 'readlink', 'access', 'open', 'writeFile', 'appendFile', 'mkdir', 'rm', 'unlink', 'rmdir', 'truncate', 'chmod', 'chown', 'utimes', 'symlink', 'link', 'rename', 'copyFile']) {
    const promises = fs.promises as any
    const original = promises[name]
    const write = !['readFile', 'readdir', 'stat', 'lstat', 'realpath', 'readlink', 'access'].includes(name)
    promises[name] = async (...args: any[]) => {
      const writes = name === 'open' ? args[1] !== 'r' && args[1] !== 0 : write
      if (!allowedPath(args[0], writes) || ['symlink', 'link', 'rename', 'copyFile'].includes(name) && !allowedPath(args[1], true)) return deny(`filesystem:${name}`)
      return original(...args)
    }
  }
  const processes = require('node:child_process')
  for (const name of ['execSync', 'execFileSync', 'spawnSync', 'fork']) processes[name] = () => deny(`subprocess:${name}`)
  // Startup prefetches may ask for keychain/MDM/git reads. Return unavailable
  // without creating any process; registration does not depend on user secrets.
  for (const name of ['exec', 'execFile', 'spawn']) processes[name] = (...args: any[]) => {
    blocked.push(`subprocess:${name}:${String(args[0])}`)
    const error = Object.assign(new Error('CLI inventory subprocess unavailable'), { code: 'ENOENT' })
    const child: any = new EventEmitter()
    child.stdout = new PassThrough()
    child.stderr = new PassThrough()
    child.stdin = new PassThrough()
    child.kill = () => true
    queueMicrotask(() => {
      const callback = args.at(-1)
      if (typeof callback === 'function') callback(error, '', '')
      else child.emit('error', error)
      child.stdout.end()
      child.stderr.end()
      child.emit('exit', 1)
      child.emit('close', 1)
    })
    return child
  }
  globalThis.fetch = (() => deny('network:fetch')) as typeof fetch
  globalThis.WebSocket = class { constructor() { deny('network:websocket') } } as any
  for (const namespace of ['node:net', 'node:tls', 'node:http', 'node:https', 'node:dgram']) {
    const module = require(namespace)
    for (const name of ['connect', 'createConnection', 'request', 'get', 'createServer', 'createSocket']) {
      if (module[name]) module[name] = () => deny(`network:${namespace}:${name}`)
    }
    if (module.Socket) module.Socket.prototype.connect = () => deny(`network:${namespace}:socket-connect`)
  }
  for (const name of ['spawn', 'spawnSync', 'connect', 'listen', 'serve', 'udpSocket', 'dlopen']) (Bun as any)[name] = () => deny(`native:${name}`)
  const bunFile = Bun.file.bind(Bun), bunWrite = Bun.write.bind(Bun)
  Bun.file = ((value: any, ...args: any[]) => allowedPath(value) ? (bunFile as any)(value, ...args) : deny('filesystem:Bun.file')) as typeof Bun.file
  Bun.write = ((value: any, ...args: any[]) => allowedPath(value, true) ? (bunWrite as any)(value, ...args) : deny('filesystem:Bun.write')) as typeof Bun.write
  syncBuiltinESMExports()
  const guardActions = (command: any) => {
    command._lifeCycleHooks = {}
    if (command._actionHandler) command._actionHandler = () => {
      const path = []
      for (let node = command; node; node = node.parent) path.unshift(node.name())
      parsed = { path: path.join(' '), args: command.args, processedArgs: command.processedArgs, options: command.opts(), globals: command.optsWithGlobals() }
      persist(); process.exit(86)
    }
    for (const child of command.commands) guardActions(child)
  }
  for (const method of ['parse', 'parseAsync'] as const) {
    const original = (Command.prototype as any)[method]
    ;(Command.prototype as any)[method] = function (...args: any[]) {
      tree = snapshotCommand(this)
      registration = snapshotCliRegistration(this)
      persist()
      if (process.env.CC_HAHA_CLI_INVENTORY_MODE === 'tree' || process.env.CC_HAHA_CLI_INVENTORY_MODE === 'bootstrap') process.exit(0)
      guardActions(this)
      return original.apply(this, args)
    }
  }
  await import(join(repository, 'preload.ts'))
  if (process.env.CC_HAHA_CLI_INVENTORY_MODE === 'bootstrap') {
    const { spyOn } = await import('bun:test')
    const state = await import(join(repository, 'src/bootstrap/state.ts'))
    const cache = await import(join(repository, 'src/utils/settings/settingsCache.ts'))
    const constants = await import(join(repository, 'src/utils/settings/constants.ts'))
    const events: unknown[] = []
    const setPath = state.setFlagSettingsPath, setSources = state.setAllowedSettingSources, reset = cache.resetSettingsCache
    spyOn(state, 'setFlagSettingsPath').mockImplementation((value: string | undefined) => {
      events.push({ operation: 'set-flag-path', value }); setPath(value)
    })
    spyOn(state, 'setAllowedSettingSources').mockImplementation((value: string[]) => {
      events.push({ operation: 'set-sources', value }); setSources(value)
    })
    spyOn(cache, 'resetSettingsCache').mockImplementation(() => {
      events.push({ operation: 'reset-cache' }); reset()
    })
    captureBootstrap = () => ({
      state: { flagSettingsPath: state.getFlagSettingsPath() ?? null, allowedSettingSources: state.getAllowedSettingSources(), enabledSettingSources: constants.getEnabledSettingSources() },
      events, environment: { marker: process.env.CC_HAHA_CLI_BOOTSTRAP_MARKER ?? null },
    })
  }
}

export async function probeCli(args: string[], options: {
  mode?: 'tree' | 'parse' | 'bootstrap'
  env?: Record<string, string>
  entrypoint?: string
  home?: string
} = {}): Promise<CliProbe> {
  if (!Array.isArray(args) || args.some(value => typeof value !== 'string' || value.includes('\0'))) throw new Error('CLI oracle argv requires NUL-free strings')
  for (const name of Object.keys(options.env ?? {})) {
    if (!['CLAUDE_CODE_ENABLE_XAA', 'CC_HAHA_CLI_BOOTSTRAP_MARKER'].includes(name)) throw new Error(`Unsupported CLI inventory environment override: ${name}`)
  }
  if (options.home) {
    if (!isAbsolute(options.home)) throw new Error('CLI oracle requires an absolute disposable temporary HOME')
    const canonical = fs.realpathSync(options.home)
    const roots = [tmpdir(), ...(fs.existsSync('/tmp') ? ['/tmp'] : [])].map(value => fs.realpathSync(value))
    if (fs.lstatSync(options.home).isSymbolicLink() || !fs.lstatSync(options.home).isDirectory()
      || !roots.some(root => canonical.startsWith(`${root}${sep}`))) throw new Error('CLI oracle requires a real disposable temporary HOME')
  }
  const home = options.home ?? await mkdtemp(join(tmpdir(), 'cc-haha-cli-inventory-'))
  const output = join(home, 'inventory.json')
  const child = Bun.spawn([process.execPath, '--no-env-file', ...cliBuildFeatures.map(value => `--feature=${value}`), '--preload', selfPath, options.entrypoint ?? join(rootDir, 'src/entrypoints/cli.tsx'), ...args], {
    cwd: home, stdin: 'ignore', stdout: 'pipe', stderr: 'pipe',
    env: createSandboxedTestEnvironment(home, {
      [childKey]: '1', CC_HAHA_CLI_INVENTORY_ROOT: rootDir, CC_HAHA_CLI_INVENTORY_OUTPUT: output,
      CC_HAHA_CLI_INVENTORY_MODE: options.mode ?? 'parse',
      CC_HAHA_CLI_INVENTORY_ARGS: JSON.stringify(args),
      CLAUDE_CODE_LOCAL_VERSION: '999.0.0-local', CLAUDE_CODE_LOCAL_PACKAGE_URL: 'claude-code-local',
      CLAUDE_CODE_LOCAL_BUILD_TIME: '2000-01-01T00:00:00.000Z',
      CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1',
      NO_COLOR: '1', TERM: 'dumb', CALLER_DIR: home,
      ...options.env,
    }),
  })
  const timer = setTimeout(() => child.kill('SIGKILL'), 20_000)
  try {
    const [exitCode, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    let evidence: { tree: any; registration: any; parsed: any; blocked: string[]; macro: Record<string, unknown>; bootstrap?: any }
    try { evidence = JSON.parse(await readFile(output, 'utf8')) }
    catch { throw new Error(`CLI inventory child produced no guarded evidence (exit ${exitCode}): ${stderr}`) }
    return { args, exitCode, stdout, stderr, ...evidence }
  } finally {
    clearTimeout(timer)
    child.kill()
    if (!options.home) await rm(home, { recursive: true, force: true })
  }
}

// A compact, reviewable golden fixture. Full descriptions and all profile trees
// remain in the report; registered syntax/defaults and actual stdio are frozen.
export function cliInventoryContract(inventory: Awaited<ReturnType<typeof captureCliInventory>>) {
  const commands = (node: any, prefix: string[] = []): any[] => {
    const path = [...prefix, node.name]
    return [{
      path: path.join(' '), aliases: node.aliases, hidden: node.hidden,
      positionalOptions: node.positionalOptions, passThroughOptions: node.passThroughOptions,
      allowUnknownOption: node.allowUnknownOption, allowExcessArguments: node.allowExcessArguments,
      arguments: node.arguments.map(({ description, ...argument }: any) => argument),
      options: node.options.map(({ description, ...option }: any) => option), helpOption: node.helpOption,
    }, ...node.commands.flatMap((child: any) => commands(child, path))]
  }
  const normal = commands(inventory.profiles.normal)
  return {
    schemaVersion: inventory.schemaVersion, features: inventory.features, macro: inventory.macro,
    scope: inventory.scope, bootstrap: inventory.bootstrap,
    normal,
    profiles: Object.fromEntries(Object.entries(inventory.profiles).map(([name, tree]) => {
      const entries = commands(tree)
      return [name, {
        paths: entries.map(node => node.path),
        overrides: entries.filter(node => JSON.stringify(node) !== JSON.stringify(normal.find(base => base.path === node.path))),
      }]
    })),
    observations: inventory.observations,
  }
}

export async function captureCliInventory() {
  const original = JSON.parse(await readFile(join(rootDir, 'scripts/quality-gate/go-parity/cli-inventory.fixture.json'), 'utf8'))
  assertCliReferenceFeatures(original.features)
  const profiles: Record<string, any> = {}
  let macro: Record<string, unknown> | undefined
  for (const [name, args, env] of [
    ['normal', ['--help'], {}], ['bare', ['--bare', '--help'], {}],
    ['print', ['--print', '--help'], {}], ['xaa', ['--help'], { CLAUDE_CODE_ENABLE_XAA: '1' }],
  ] as const) {
    const probe = await probeCli([...args], { mode: 'tree', env })
    if (probe.exitCode !== 0 || !probe.tree || probe.stderr || probe.stdout) throw new Error(`CLI ${name} inventory failed: ${JSON.stringify(probe)}`)
    if (macro && JSON.stringify(macro) !== JSON.stringify(probe.macro)) throw new Error(`CLI ${name} MACRO configuration differs from normal profile`)
    macro = probe.macro
    profiles[name] = probe.tree
  }
  const observations: Record<string, Pick<CliProbe, 'args' | 'exitCode' | 'stdout' | 'stderr'>> = {}
  for (const [name, args] of [
    ['help', ['--help']], ['mcpHelp', ['mcp', '--help']], ['pluginAliasHelp', ['plugins', '--help']],
    ['autoModeHelp', ['auto-mode', '--help']], ['unknownOption', ['--inventory-unknown-option']],
    ['missingOptionValue', ['--output-format']], ['invalidChoice', ['--output-format', 'invalid']],
    ['missingCommandArgument', ['mcp', 'get']], ['versionLong', ['--version']],
    ['versionLower', ['-v']], ['versionUpper', ['-V']],
  ] as const) {
    const { exitCode, stdout, stderr } = await probeCli([...args])
    if (exitCode === 86) throw new Error(`CLI observation ${name} reached a guarded action`)
    observations[name] = { args: [...args], exitCode, stdout, stderr }
  }
  return {
    schemaVersion: 1, features: cliBuildFeatures,
    macro,
    scope: 'Commander registration and pre-action parsing; defaults are registered defaults, not handler fallbacks. Runtime gates use isolated empty settings, plus explicit bare, print, and XAA profiles. Bootstrap routes are source-derived and do not execute native/model handlers.',
    bootstrap: inventoryBootstrap(await readFile(join(rootDir, 'src/entrypoints/cli.tsx'), 'utf8')),
    profiles, observations,
  }
}

if (process.env[childKey] === '1') await installChildGuard()
else if (import.meta.main) {
  const result = await captureCliInventory()
  const output = process.argv[2] ?? join(rootDir, 'artifacts/cli-inventory/report.json')
  if (!isAbsolute(output)) throw new Error('CLI inventory output path must be absolute')
  await fs.promises.mkdir(dirname(output), { recursive: true })
  await writeFile(output, JSON.stringify(result, null, 2) + '\n')
  console.log(`CLI inventory: ${output}`)
}
