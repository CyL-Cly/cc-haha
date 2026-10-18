import fs from 'node:fs'
import { createRequire } from 'node:module'
import { basename, resolve, sep } from 'node:path'
import { execSync } from 'node:child_process'
import capitalize from 'lodash-es/capitalize.js'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual original file editor and physical command')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_FILE_EDITOR_SOURCE !== '1') throw new Error('Pinned editor Source required')
const ts = createRequire(new URL('../../../../desktop/package.json', import.meta.url))('typescript')
function original(path: string, names: string[], deps: Record<string, unknown>, returned: string) {
  const source = ts.createSourceFile(path, fs.readFileSync(new URL(path, import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)
  const selected = source.statements.filter((node: any) => ts.isFunctionDeclaration(node) && node.name && names.includes(node.name.text) || ts.isVariableStatement(node) && node.declarationList.declarations.some((d: any) => names.includes(d.name.getText(source))))
  if (selected.length !== names.length) throw new Error('Original file editor identity changed')
  const js = ts.transpileModule(selected.map((node: any) => node.getText(source).replace(/^export /, '')).join('\n'), { compilerOptions: { target: ts.ScriptTarget.ESNext, module: ts.ModuleKind.ESNext } }).outputText
  return new Function(...Object.keys(deps), js + '\nreturn ' + returned)(...Object.values(deps))
}
const classifyGuiEditor = original('../../../../src/utils/editor.ts', ['GUI_EDITORS', 'classifyGuiEditor'], { basename }, 'classifyGuiEditor')
const toIDEDisplayName = original('../../../../src/utils/ide.ts', ['supportedIdeConfigs', 'EDITOR_DISPLAY_NAMES', 'toIDEDisplayName'], { basename, capitalize }, 'toIDEDisplayName')
const inside = (path: string) => { if (!resolve(path).startsWith(home + sep)) throw new Error('Private editor path escaped'); return path }
const rows = []
for (const row of input.rows) {
  const path = inside(row.path)
  if (row.missing) fs.rmSync(path, { force: true })
  else fs.writeFileSync(path, 'ORIGINAL_PLAN_BYTES')
  const lifecycle: string[] = [], commands: string[] = []
  const ink = Object.fromEntries(['enterAlternateScreen', 'exitAlternateScreen', 'pause', 'suspendStdin', 'resumeStdin', 'resume'].map(name => [name, () => lifecycle.push(name)]))
  const editorCommand = row.editor === 'code' ? 'code -w' : row.editor === 'subl' ? 'subl --wait' : row.editor
  const call = original('../../../../src/utils/promptEditor.ts', ['EDITOR_OVERRIDES', 'isGuiEditor', 'editFileInEditor'], {
    process, instances: new Map([[process.stdout, ink]]), getExternalEditor: () => row.editor || undefined, classifyGuiEditor, toIDEDisplayName,
    getFsImplementation: () => ({ statSync: (path: string) => fs.statSync(inside(path)), readFileSync: (path: string, options: any) => fs.readFileSync(inside(path), options) }),
    execSync_DEPRECATED: (command: string, options: any) => {
      if (command !== `${editorCommand} "${path}"`) throw new Error('Unexpected original editor shell command')
      commands.push(command)
      return execSync(command, { ...options, cwd: home, env: { ...row.env, HOME: home, USERPROFILE: home, CLAUDE_CONFIG_DIR: home + '/.claude' } })
    },
  }, 'editFileInEditor')
  const result = call(path)
  rows.push({ result, lifecycle, commands, after: fs.existsSync(path) ? fs.readFileSync(path).toString('base64') : null, argv: fs.existsSync(row.env.EDITOR_ARGUMENTS) ? fs.readFileSync(row.env.EDITOR_ARGUMENTS, 'utf8') : '' })
}
process.stdout.write(JSON.stringify({ rows }))
