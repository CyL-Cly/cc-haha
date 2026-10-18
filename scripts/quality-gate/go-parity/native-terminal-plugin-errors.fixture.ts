import fs from 'node:fs'
import { createRequire } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'

const input = await Bun.stdin.json()
assertDisposableFixtureHome(input.home, 'Original Plugin Errors formatter and remediation rows')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TERMINAL_PLUGIN_ERRORS_SOURCE !== '1') throw new Error('Missing plugin Errors Source guard')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
function compile(relative: string, names: string[], deps: any, returns: string) {
  const source = ts.createSourceFile(relative, fs.readFileSync(new URL('../../../src/' + relative, import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
  const nodes = source.statements.filter((node: any) => ts.isFunctionDeclaration(node) && names.includes(node.name?.text))
  if (nodes.length !== names.length) throw new Error('Original declarations changed: ' + relative)
  const code = ts.transpileModule(nodes.map((node: any) => node.getText(source).replace(/^export /, '')).join('\n'), { compilerOptions: { target: ts.ScriptTarget.ESNext } }).outputText
  return new Function(...Object.keys(deps), code + '; return ' + returns)(...Object.values(deps))
}
const display = compile('commands/plugin/PluginErrors.tsx', ['formatErrorMessage', 'getErrorGuidance'], { getPluginErrorMessage: () => { throw new Error('Undeclared error profile') } }, '{ formatErrorMessage, getErrorGuidance }')
const results = input.snapshots.map((snapshot: any) => {
  const getSettingsForSource = (source: string) => snapshot.errorSettings?.[{ userSettings: 'user', projectSettings: 'project', localSettings: 'local', policySettings: 'managed' }[source] ?? source]
  const original = compile('commands/plugin/PluginSettings.tsx', ['getExtraMarketplaceSourceInfo', 'buildMarketplaceAction', 'buildPluginAction', 'isTransientError', 'getPluginNameFromError', 'buildErrorRows', '_temp6', '_temp7'], { getSettingsForSource, ...display, TRANSIENT_ERROR_TYPES: new Set(['git-auth-failed', 'git-timeout', 'network-error']) }, '{ buildErrorRows, isTransientError, _temp6, _temp7 }')
  const failed = snapshot.failedMarketplaces ?? []
  const names = new Set(failed.map((row: any) => row.name))
  const errors = snapshot.errors ?? []
  const market = errors.filter((row: any) => ['marketplace-not-found', 'marketplace-load-failed', 'marketplace-blocked-by-policy'].includes(row.type) && !names.has(row.marketplace))
  const rows = original.buildErrorRows(failed, market, errors.filter(original._temp6), errors.filter(original._temp7), snapshot.brokenMarketplaces ?? [], errors.filter(original.isTransientError), new Map(Object.entries(snapshot.pluginScopes ?? {})))
  return { rows, display: errors.map((error: any) => ({ message: display.formatErrorMessage(error), guidance: display.getErrorGuidance(error) })) }
})
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify(results), error => error ? reject(error) : resolve()))
