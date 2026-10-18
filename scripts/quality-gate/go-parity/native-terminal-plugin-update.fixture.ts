import fs from 'node:fs'
import { createRequire } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'

const input = await Bun.stdin.json()
assertDisposableFixtureHome(input.home, 'Original installed-plugin details menu and operation guards')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_PLUGIN_UPDATE_SOURCE !== '1') throw new Error('Missing plugin update Source guard')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
const path = new URL('../../../src/commands/plugin/ManagePlugins.tsx', import.meta.url)
const source = ts.createSourceFile(path.pathname, fs.readFileSync(path, 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
let menu: any
let operation: any
let done: any
function visit(node: any) {
  if (ts.isVariableDeclaration(node) && node.name.getText(source) === 'detailsMenuItems') menu = node.initializer.arguments[0]
  if (ts.isVariableDeclaration(node) && node.name.getText(source) === 'handleSingleOperation') operation = node.initializer
  if (ts.isJsxAttribute(node) && node.name.text === 'onDone' && node.initializer?.expression?.getText(source).includes("case 'configured'")) done = node.initializer.expression
  ts.forEachChild(node, visit)
}
visit(source)
if (!menu || !operation || !done) throw new Error('Original installed plugin consumers changed')
const transpile = (code: string) => ts.transpileModule(code, { compilerOptions: { target: ts.ScriptTarget.ESNext } }).outputText
const getMenu = new Function('selectedPlugin', 'selectedPluginHasMcpb', 'getSettings_DEPRECATED', 'viewState', transpile('const menu = ' + menu.getText(source)) + ';return menu().map(row=>row.label)')
const observations = []
for (const profile of input.profiles) {
  const selectedPlugin = { plugin: { name: 'sample', manifest: profile.manifest }, marketplace: profile.scope === 'builtin' ? 'builtin' : 'mkt', scope: profile.scope, pendingUpdate: profile.pendingUpdate }
  const labels = getMenu(selectedPlugin, profile.hasMcpb, () => ({ enabledPlugins: { ['sample@' + selectedPlugin.marketplace]: profile.enabled } }), 'plugin-details')
  const guards: Record<string, string | null> = {}
  for (const name of ['enable', 'disable', 'update', 'uninstall']) {
    let error: string | null = null
    const handle = new Function('selectedPlugin', 'isInstallableScope', 'setProcessError', 'setIsProcessing', transpile('const handle = ' + operation.getText(source)) + ';return handle')(selectedPlugin, (scope: string) => ['user', 'project', 'local'].includes(scope), (value: string | null) => { error = value }, () => { throw new Error('operation admitted') })
    try { await handle(name) } catch (failure) { if (!(failure instanceof Error) || failure.message !== 'operation admitted') throw failure }
    guards[name] = error
  }
  const configurationMessages: Record<string, string> = {}
  const callback = new Function('finish', 'selectedPlugin', transpile('const done = ' + done.getText(source)) + ';return done')((message: string) => { configurationMessages[outcome] = message }, selectedPlugin)
  let outcome = ''
  for (outcome of ['configured', 'skipped', 'error']) callback(outcome, 'owned save failure')
  observations.push({ labels, guards, configurationMessages })
}
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify(observations), error => error ? reject(error) : resolve()))
