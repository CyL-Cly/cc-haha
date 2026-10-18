import fs from 'node:fs'
import * as path from 'node:path'
import { createRequire } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'Actual Config external import callbacks')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TERMINAL_CONFIG_EXTERNAL_SOURCE !== '1') throw new Error('Missing original external Config fixture guard')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
function originals(relative: string, names: string[]) {
  const file = new URL('../../../' + relative, import.meta.url).pathname
  const source = ts.createSourceFile(file, fs.readFileSync(file, 'utf8'), ts.ScriptTarget.Latest, true, relative.endsWith('tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS)
  const declarations = source.statements.filter((node: any) => ts.isFunctionDeclaration(node) && names.includes(node.name?.text))
  if (declarations.length !== names.length) throw new Error('Frozen original declarations changed: ' + names)
  return new Bun.Transpiler({loader:'tsx'}).transformSync(declarations.map((node: any) => node.getText(source)).join('\n')).replaceAll('export ', '')
}
const dependencies = { dirname:path.dirname, resolve:path.resolve, normalize:path.normalize, isAbsolute:path.isAbsolute, join:path.join, posix:path.posix, getPlatform:()=>input.platform ?? 'darwin', getCwd:()=>input.cwd, getOriginalCwd:()=>input.cwd, homedir:()=>home }
const body = originals('src/utils/path.ts',['expandPath','containsPathTraversal']) + '\n' + originals('src/utils/permissions/filesystem.ts',['normalizeCaseForComparison','relativePath','pathInWorkingPath']) + '\n' + originals('src/utils/claudemd.ts',['pathInOriginalCwd','getExternalClaudeMdIncludes','hasExternalClaudeMdIncludes']) + '\nreturn {getExternalClaudeMdIncludes,hasExternalClaudeMdIncludes}'
const consumer = new Function(...Object.keys(dependencies),body)(...Object.values(dependencies))
const rows = input.rows.map((files: any) => ({ includes:consumer.getExternalClaudeMdIncludes(files), admitted:consumer.hasExternalClaudeMdIncludes(files) }))
const callbacks = originals('src/components/ClaudeMdExternalIncludesDialog.tsx',['_temp2','_temp3'])+'\nreturn {_temp2,_temp3}'
const dialog = new Function(callbacks)()
const initial = input.project
const declined = dialog._temp2(initial)
const accepted = dialog._temp3(initial)
const dialogSource = fs.readFileSync(new URL('../../../src/components/ClaudeMdExternalIncludesDialog.tsx',import.meta.url),'utf8')
const configSource = fs.readFileSync(new URL('../../../src/components/Settings/Config.tsx',import.meta.url),'utf8')
if (!dialogSource.includes('handleSelection("no")') || !configSource.includes('React.use(getMemoryFiles(true))') || !configSource.includes("externalIncludes={getExternalClaudeMdIncludes(memoryFiles)}")) throw new Error('Original forced discovery/decline Escape consumer changed')
const configAST = ts.createSourceFile('Config.tsx',configSource,ts.ScriptTarget.Latest,true,ts.ScriptKind.TSX)
let rollback: any
const scan = (node: any) => {
  if (ts.isVariableDeclaration(node) && node.name?.text === 'revertChanges') {
    rollback = node.initializer.arguments[0].body.statements.find((statement: any) => ts.isExpressionStatement(statement) && statement.expression.expression?.getText(configAST) === 'saveGlobalConfig')
  }
  ts.forEachChild(node,scan)
}
scan(configAST)
if (!rollback) throw new Error('Original Config full global rollback callback changed')
const rollbackJS = new Bun.Transpiler({loader:'tsx'}).transformSync(rollback.getText(configAST))
let rolledBack = accepted
new Function('saveGlobalConfig','initialConfig',rollbackJS)((updater: any)=>{rolledBack=updater(accepted)},{current:initial})
process.stdout.write(JSON.stringify({rows,declined,accepted,rolledBack}))
