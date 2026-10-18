import fs from 'node:fs'
import { readFile, stat } from 'node:fs/promises'
import { join } from 'node:path'
import { createRequire } from 'node:module'
import { mock } from 'bun:test'
import chokidar from 'chokidar'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'Actual keybinding loader')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_KEYBINDING_LOADER_FIXTURE !== '1') throw new Error('Missing original loader guard')
Object.assign(globalThis, { MACRO: { VERSION: 'fixture', PACKAGE_URL: 'fixture', NATIVE_PACKAGE_URL: 'fixture', BUILD_TIME: 'fixture', FEEDBACK_CHANNEL: 'local', VERSION_CHANGELOG: '', ISSUES_EXPLAINER: '' } })
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
mock.module(new URL('../../../src/utils/platform.ts', import.meta.url).pathname, () => ({ getPlatform: () => input.platform ?? 'macos' }))
const { parseBindings } = await import('../../../src/keybindings/parser')
const { DEFAULT_BINDINGS } = await import('../../../src/keybindings/defaultBindings')
const { validateBindings, checkDuplicateKeysInJson } = await import('../../../src/keybindings/validate')
const { createSignal } = await import('../../../src/utils/signal')
const sourcePath = new URL('../../../src/keybindings/loadUserBindings.ts', import.meta.url).pathname
const source = ts.createSourceFile(sourcePath, fs.readFileSync(sourcePath, 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)
const functions = ['isKeybindingCustomizationEnabled', 'logCustomBindingsLoadedOncePerDay', 'isKeybindingBlock', 'isKeybindingBlockArray', 'getKeybindingsPath', 'getDefaultParsedBindings', 'loadKeybindings', 'loadKeybindingsSync', 'loadKeybindingsSyncWithWarnings', 'initializeKeybindingWatcher', 'disposeKeybindingWatcher', 'handleChange', 'handleDelete', 'getCachedKeybindingWarnings', 'resetKeybindingLoaderForTesting']
const variables = ['FILE_STABILITY_THRESHOLD_MS', 'FILE_STABILITY_POLL_INTERVAL_MS', 'watcher', 'initialized', 'disposed', 'cachedBindings', 'cachedWarnings', 'keybindingsChanged', 'lastCustomBindingsLogDate', 'subscribeToKeybindingChanges']
const selected = source.statements.filter((node: any) => ts.isFunctionDeclaration(node) && node.name && functions.includes(node.name.text) || ts.isVariableStatement(node) && node.declarationList.declarations.some((declaration: any) => variables.includes(declaration.name.getText(source))))
if (selected.filter((node: any) => ts.isFunctionDeclaration(node)).length !== functions.length) throw new Error('Original keybinding loader function identity changed')
const body = new Bun.Transpiler({ loader: 'ts' }).transformSync(selected.map((node: any) => node.getText(source)).join('\n')).replace(/export /g, '')
const results = []
for (const [index, row] of input.rows.entries()) {
  const config = join(home, `config-${index}`)
  fs.mkdirSync(config, { recursive: true })
  const file = join(config, 'keybindings.json')
  if (row.content !== null) fs.writeFileSync(file, row.content)
  let reads = 0
  let watched = 0
  const gateCalls: unknown[] = []
  const closed: Promise<void>[] = []
  const deps = {
    getFeatureValue_CACHED_MAY_BE_STALE: (name: string, fallback: boolean) => { gateCalls.push({ name, fallback }); return row.enabled },
    logEvent: () => {}, registerCleanup: () => {}, logForDebugging: () => {},
    getClaudeConfigHomeDir: () => config, errorMessage: (error: Error) => error.message,
    isENOENT: (error: any) => error.code === 'ENOENT', jsonParse: JSON.parse,
    readFile: async (file: string, encoding: any) => { reads++; return readFile(file, encoding) },
    readFileSync: (file: string, encoding: any) => { reads++; return fs.readFileSync(file, encoding) },
    dirname: (file: string) => config, join, stat, createSignal,
    DEFAULT_BINDINGS, parseBindings, validateBindings, checkDuplicateKeysInJson,
    chokidar: { watch: (file: string, options: any) => {
      watched++
      if (!row.changes) return { on() { return this }, close: async () => {} }
      const watcher = chokidar.watch(file, options)
      const close = watcher.close.bind(watcher)
      watcher.close = () => { const result = close(); closed.push(result); return result }
      return watcher
    } },
  }
  const loader = new Function(...Object.keys(deps), body + '\nreturn { loadKeybindings, loadKeybindingsSyncWithWarnings, initializeKeybindingWatcher, disposeKeybindingWatcher, subscribeToKeybindingChanges, getWatcher: () => watcher }')(...Object.values(deps))
  const result = row.synchronous ? loader.loadKeybindingsSyncWithWarnings() : await loader.loadKeybindings()
  await loader.initializeKeybindingWatcher()
  const changes = []
  if (row.changes) {
    const watcher = loader.getWatcher()
    await new Promise<void>((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error('Original physical watcher did not become ready')), 4000)
      watcher.once('ready', () => { clearTimeout(timer); resolve() })
    })
    for (const content of row.changes) {
      const next = new Promise<any>((resolve, reject) => {
        const timer = setTimeout(() => reject(new Error('Original physical keybinding change was not delivered')), 4000)
        const remove = loader.subscribeToKeybindingChanges((result: any) => { clearTimeout(timer); remove(); resolve(result) })
      })
      if (content === null) fs.unlinkSync(file)
      else { const temp = file + '.replacement'; fs.writeFileSync(temp, content); fs.renameSync(temp, file) }
      changes.push(await next)
    }
  }
  loader.disposeKeybindingWatcher()
  await Promise.all(closed)
  results.push({ ...result, changes, reads, watched, gateCalls })
}
process.stdout.write(JSON.stringify({ results, capabilities }))
