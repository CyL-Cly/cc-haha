import fs from 'node:fs'
import { stat as physicalStat } from 'node:fs/promises'
import * as path from 'node:path'
import { createRequire } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'

const input = await new Response(Bun.stdin.stream()).json()
const home = assertDisposableFixtureHome(input.home, 'Actual directory validation')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TERMINAL_DIRECTORY_SOURCE !== '1') throw new Error('Source directory guard required')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
function originals(relative: string, names: string[]) {
  const file = new URL('../../../' + relative, import.meta.url).pathname
  const source = ts.createSourceFile(file, fs.readFileSync(file, 'utf8'), ts.ScriptTarget.Latest, true)
  const declarations = source.statements.filter((node: any) => ts.isFunctionDeclaration(node) && names.includes(node.name?.text))
  if (declarations.length !== names.length) throw new Error('Frozen Source functions changed: ' + names)
  return new Bun.Transpiler({ loader: 'ts' }).transformSync(declarations.map((node: any) => node.getText(source)).join('\n')).replaceAll('export ', '')
}
const body = originals('src/utils/path.ts', ['expandPath', 'containsPathTraversal']) + '\n' + originals('src/utils/permissions/filesystem.ts', ['normalizeCaseForComparison', 'relativePath', 'pathInWorkingPath', 'allWorkingDirectories']) + '\n' + originals('src/commands/add-dir/validation.ts', ['validateDirectoryForWorkspace', 'addDirHelpMessage']) + '\nreturn {validateDirectoryForWorkspace,addDirHelpMessage}'
const results = []
for (const row of input.rows) {
  let stats = 0
  const stat = async (file: string) => {
    stats++
    if (row.errno) throw Object.assign(new Error('owned stat failure'), { code: row.errno })
    return physicalStat(file)
  }
  const deps = { dirname: path.dirname, resolve: path.resolve, normalize: path.normalize, isAbsolute: path.isAbsolute, join: path.join, posix: path.posix, getPlatform: () => 'darwin', getCwd: () => input.cwd, getOriginalCwd: () => input.originalCwd, homedir: () => home, stat, getErrnoCode: (error: any) => error.code, chalk: { bold: (text: string) => text } }
  const original = new Function(...Object.keys(deps), body)(...Object.values(deps))
  const context = { additionalWorkingDirectories: new Map(row.working.map((file: string) => [file, { path: file, source: 'session' }])) }
  try {
    const value = await original.validateDirectoryForWorkspace(row.path, context)
    results.push({ value, help: original.addDirHelpMessage(value), stats })
  } catch (error: any) { results.push({ error: error.code ?? error.message, stats }) }
}
process.stdout.write(JSON.stringify(results))
