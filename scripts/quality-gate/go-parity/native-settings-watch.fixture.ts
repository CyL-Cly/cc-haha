import { parse } from 'acorn'
import chokidar from 'chokidar'
import { mkdir, stat, writeFile, rm } from 'node:fs/promises'
import * as platformPath from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { createSignal } from '../../../src/utils/signal'
import { markInternalWrite, consumeInternalWrite, clearInternalWrites } from '../../../src/utils/settings/internalWrites'

assertDisposableFixtureHome(process.env.HOME, 'Native settings watch')
if (process.env.CC_HAHA_SETTINGS_WATCH_FIXTURE !== '1') throw new Error('Unowned settings watch fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.root, 'Native settings watch data')
const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/utils/settings/changeDetector.ts', import.meta.url)).text())
const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
const declarations = ast.body.map((node: any) => node.type === 'ExportNamedDeclaration' ? node.declaration : node)
const variables = ['FILE_STABILITY_THRESHOLD_MS', 'FILE_STABILITY_POLL_INTERVAL_MS', 'INTERNAL_WRITE_WINDOW_MS', 'MDM_POLL_INTERVAL_MS', 'DELETION_GRACE_MS', 'watcher', 'mdmPollTimer', 'lastMdmSnapshot', 'initialized', 'disposed', 'pendingDeletions', 'settingsChanged', 'testOverrides']
const statements = declarations.filter((node: any) => node?.type === 'VariableDeclaration' && node.declarations.some((d: any) => variables.includes(d.id.name)))
const functions = declarations.filter((node: any) => node?.type === 'FunctionDeclaration')
const noop = () => {}
let trace: unknown[] = []
const paths = Object.fromEntries(input.files.map((file: any) => [file.source, file.path]))
for (const path of Object.values(paths) as string[]) {
  const relative = platformPath.relative(input.root, path)
  if (relative === '..' || relative.startsWith(`..${platformPath.sep}`) || platformPath.isAbsolute(relative)) throw new Error('Unowned settings source path')
}
const dependencies = {
  chokidar, platformPath, stat, createSignal,
  getIsRemoteMode: () => input.remote ?? false,
  getSettingsFilePathForSource: (source: string) => paths[source] ?? null,
  getManagedSettingsDropInDir: () => input.dropIn,
  SETTING_SOURCES: ['userSettings', 'projectSettings', 'localSettings', 'flagSettings', 'policySettings'],
  getMdmSettings: () => ({ settings: undefined }), getHkcuSettings: () => ({ settings: undefined }),
  refreshMdmSettings: async () => ({ mdm: { settings: undefined }, hkcu: { settings: undefined } }),
  setMdmSettingsCache: noop, jsonStringify: JSON.stringify, logForDebugging: noop, errorMessage: (e: any) => e.message,
  registerCleanup: noop, setInterval: () => ({ unref: noop }), clearInterval: noop,
  consumeInternalWrite, clearInternalWrites,
  executeConfigChangeHooks: async (source: string, path: string) => {
    trace.push({ stage: 'hook', source, path: platformPath.relative(input.root, path) })
    return [{ blocked: input.blocked?.includes(platformPath.relative(input.root, path)) ?? false }]
  },
  hasBlockingResult: (rows: any[]) => rows.some(row => row.blocked),
  resetSettingsCache: () => trace.push({ stage: 'reset' }),
}
const original = new Function('dependencies', `with(dependencies){${statements.map((node: any) => source.slice(node.start, node.end)).join('\n')}\n${functions.map((node: any) => source.slice(node.start, node.end)).join('\n')}\nreturn {initialize,dispose,getWatchTargets,subscribe:settingsChanged.subscribe,watcher:()=>watcher,resetForTesting,constants:{stability:FILE_STABILITY_THRESHOLD_MS,poll:FILE_STABILITY_POLL_INTERVAL_MS,deletionGrace:DELETION_GRACE_MS,mdmPoll:MDM_POLL_INTERVAL_MS,internalWriteWindow:INTERNAL_WRITE_WINDOW_MS}}}`)(dependencies)
const targets = await original.getWatchTargets()
if (!input.actions) {
  process.stdout.write(JSON.stringify({ targets: { directories: targets.dirs, files: [...targets.settingsFiles], dropInDirectory: targets.dropInDir }, constants: original.constants }))
} else {
  await original.resetForTesting({ stabilityThreshold: 60, pollInterval: 10, deletionGrace: 170 })
  original.subscribe((source: string) => trace.push({ stage: 'apply', source }))
  await original.initialize()
  const watcher = original.watcher()
  if (watcher) await new Promise(resolve => watcher.once('ready', resolve))
  try {
    for (const action of input.actions) {
      const path = action.path ? platformPath.join(input.root, action.path) : undefined
      if (action.mark) markInternalWrite(path!)
      if (action.write !== undefined) { await mkdir(platformPath.dirname(path!), { recursive: true }); await writeFile(path!, action.write) }
      if (action.remove) await rm(path!, { force: true })
      await new Promise(resolve => setTimeout(resolve, action.wait ?? 0))
    }
    process.stdout.write(JSON.stringify({ trace }))
  } finally { await original.dispose() }
}
