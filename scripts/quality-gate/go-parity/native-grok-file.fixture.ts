import { parse } from 'acorn'
import { assertDisposableFixtureHome } from './fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'Grok file normalization')
if (process.env.CC_HAHA_GROK_FILE_FIXTURE !== '1') throw new Error('Unowned Grok token normalization fixture')
const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/services/grokAuth/storage.ts', import.meta.url)).text())
const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
const declarations = ast.body.map((node: any) => node.type === 'ExportNamedDeclaration' ? node.declaration : node).filter((node: any) => node?.type === 'FunctionDeclaration')
const code = ['normalizeTokenFile', 'stringField', 'optionalString', 'expiryField'].map(name => {
  const node = declarations.find((node: any) => node.id?.name === name)
  if (!node) throw new Error('Source Grok normalization declaration missing: ' + name)
  return source.slice(node.start, node.end)
}).join('\n')
const normalize = new Function(code + '\nreturn normalizeTokenFile')()
const rows = await Bun.stdin.json()
process.stdout.write(JSON.stringify(rows.map((row: string) => { try { return normalize(JSON.parse(row)) } catch { return null } })))
