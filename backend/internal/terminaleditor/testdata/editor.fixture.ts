import fs from 'node:fs'
import { createRequire } from 'node:module'
import { basename } from 'node:path'
import memoize from 'lodash-es/memoize.js'
import capitalize from 'lodash-es/capitalize.js'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual external editor selection')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_EDITOR_SOURCE !== '1') throw new Error('Pinned editor Source required')
const ts = createRequire(new URL('../../../../desktop/package.json', import.meta.url))('typescript')
function sourceModule(path: string, names: string[], deps: Record<string, unknown>, returned: string) {
  const source = ts.createSourceFile(path, fs.readFileSync(new URL(path, import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)
  const nodes = source.statements.filter((node: any) => ts.isFunctionDeclaration(node) && node.name && names.includes(node.name.text) || ts.isVariableStatement(node) && node.declarationList.declarations.some((d: any) => names.includes(d.name.getText(source))))
  if (nodes.length !== names.length) throw new Error('Original editor declaration identity changed')
  const body = nodes.map((node: any) => node.getText(source).replace(/^export /, '')).join('\n')
  const js = ts.transpileModule(body, { compilerOptions: { target: ts.ScriptTarget.ESNext, module: ts.ModuleKind.ESNext } }).outputText
  return new Function(...Object.keys(deps), js + '\nreturn ' + returned)(...Object.values(deps))
}
const display = sourceModule('../../../../src/utils/ide.ts', ['supportedIdeConfigs', 'EDITOR_DISPLAY_NAMES', 'toIDEDisplayName'], { basename, capitalize }, 'toIDEDisplayName')
const rows = []
for (const row of input.rows) {
  const localProcess = { env: row.env, platform: row.platform }
  const lookups: string[] = []
  const select = sourceModule('../../../../src/utils/editor.ts', ['isCommandAvailable', 'getExternalEditor'], { process: localProcess, memoize, whichSync: (command: string) => { lookups.push(command); return Bun.which(command, { cwd: home, PATH: row.env.PATH ?? '' }) } }, 'getExternalEditor')
  const selected = select()
  localProcess.env = { ...row.env, VISUAL: 'changed-after-memoization' }
  rows.push({ selected: selected ?? '', retained: select() ?? '', lookups })
}
process.stdout.write(JSON.stringify({ rows, names: input.names.map((name: string) => display(name)) }))
