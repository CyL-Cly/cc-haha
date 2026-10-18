import fs from 'node:fs'
import { createRequire } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'

const input = await Bun.stdin.json()
assertDisposableFixtureHome(input.home, 'Original Manage unified plugin/MCP computation')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_PLUGIN_INSTALLED_SOURCE !== '1') throw new Error('Missing Installed Source guard')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
const relative = '../../../src/commands/plugin/ManagePlugins.tsx'
const source = ts.createSourceFile(relative, fs.readFileSync(new URL(relative, import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
let callback: any
function visit(node: any) {
  if (ts.isVariableDeclaration(node) && node.name?.text === 'unifiedItems') callback = node.initializer.arguments[0]
  ts.forEachChild(node, visit)
}
visit(source)
if (!callback) throw new Error('Original unified Installed computation changed')
const code = ts.transpileModule('const compute = ' + callback.getText(source), { compilerOptions: { target: ts.ScriptTarget.ESNext } }).outputText
const identifiers = ts.createSourceFile('pluginIdentifier.ts', fs.readFileSync(new URL('../../../src/utils/plugins/pluginIdentifier.ts', import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true)
const parse = identifiers.statements.find((node: any) => ts.isFunctionDeclaration(node) && node.name?.text === 'parsePluginIdentifier')
if (!parse) throw new Error('Original plugin identifier changed')
const identifierCode = ts.transpileModule(parse.getText(identifiers).replace(/^export /, ''), { compilerOptions: { target: ts.ScriptTarget.ESNext } }).outputText
const parsePluginIdentifier = new Function(identifierCode + ';return parsePluginIdentifier')()
const observations = input.profiles.map((profile: any) => {
  const states = profile.snapshot.manageInstalled.filter((row: any) => !row.blocked).map((row: any) => ({ plugin: { name: row.name, source: row.id, manifest: row.manifest, isBuiltin: row.scope === 'builtin' }, marketplace: row.id.split('@')[1] || 'local', scope: row.scope }))
  const deps: any = {
    getSettings_DEPRECATED: () => profile.settings, pluginStates: states,
    mcpClients: profile.clients.map((client: any) => ({ ...client, type: client.status })),
    pluginErrors: profile.snapshot.errors ?? [], pendingToggles: new Map(),
    flaggedPlugins: profile.snapshot.flaggedPlugins ?? {},
    getPluginEditableScopes: () => new Map(Object.entries(profile.snapshot.pluginScopes ?? {})),
    parsePluginIdentifier, getMcpStatus: (client: any) => ['connected', 'disabled', 'pending', 'needs-auth'].includes(client.type) ? client.type : 'failed'
  }
  const rows = new Function(...Object.keys(deps), code + ';return compute()')(...Object.values(deps))
  return rows.map((row: any) => ({ itemType: row.type, id: row.id, name: row.name, scope: row.scope, status: row.status ?? null, indented: row.indented ?? false }))
})
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify(observations), error => error ? reject(error) : resolve()))
