import fs from 'node:fs'
import { createRequire } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'

const input = await Bun.stdin.json()
assertDisposableFixtureHome(input.home, 'Original Plugin discover/browse details menu')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TERMINAL_PLUGIN_DETAILS_SOURCE !== '1') {
  throw new Error('Missing plugin detail Source guard')
}
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
const relative = '../../../src/commands/plugin/pluginDetailsHelpers.tsx'
const source = ts.createSourceFile(relative, fs.readFileSync(new URL(relative, import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
const names = ['extractGitHubRepo', 'buildPluginDetailsMenuOptions']
const nodes = source.statements.filter((node: any) => ts.isFunctionDeclaration(node) && names.includes(node.name?.text))
if (nodes.length !== names.length) throw new Error('Original plugin detail functions changed')
const code = ts.transpileModule(nodes.map((node: any) => node.getText(source).replace(/^export /, '')).join('\n'), { compilerOptions: { target: ts.ScriptTarget.ESNext } }).outputText
const original = new Function(code + '; return {extractGitHubRepo,buildPluginDetailsMenuOptions}')()
const values = input.rows.map((entry: any) => original.buildPluginDetailsMenuOptions(entry.homepage, original.extractGitHubRepo({ entry })))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify(values), error => error ? reject(error) : resolve()))
