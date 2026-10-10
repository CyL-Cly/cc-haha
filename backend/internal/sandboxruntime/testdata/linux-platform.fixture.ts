import fs from 'node:fs'
import { parse } from 'acorn'
const input = JSON.parse(await Bun.stdin.text())
const platform = fs.readFileSync(new URL('../../../../node_modules/@anthropic-ai/sandbox-runtime/dist/utils/platform.js', import.meta.url), 'utf8')
const cli = new Bun.Transpiler({loader:'ts'}).transformSync(fs.readFileSync(new URL('../../../../src/utils/platform.ts',import.meta.url),'utf8'))
const cliAst:any = parse(cli,{ecmaVersion:'latest',sourceType:'module'})
const cliNodes = cliAst.body.map((n:any)=>n.type==='ExportNamedDeclaration'?n.declaration:n)
const cliNode = cliNodes.find((n:any)=>n?.type==='VariableDeclaration'&&n.declarations?.[0]?.id?.name==='getPlatform')
if (!cliNode) throw new Error('Missing Source CLI getPlatform')
const manager = fs.readFileSync(new URL('../../../../node_modules/@anthropic-ai/sandbox-runtime/dist/sandbox/sandbox-manager.js', import.meta.url), 'utf8')
const extract = (source: string, name: string) => {
 const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
 const nodes = ast.body.map((n: any) => n.type === 'ExportNamedDeclaration' ? n.declaration : n)
 const node = nodes.find((n: any) => n?.type === 'FunctionDeclaration' && n.id.name === name)
 if (!node) throw new Error('Missing Source ' + name)
 return source.slice(node.start, node.end)
}
const rows = input.versions.map((version: string | null) => {
 const dependencies = { process: { platform: 'linux' }, fs: { readFileSync: () => { if (version === null) throw new Error('ENOENT'); return version } }, getPlatform: () => 'linux', memoize:(fn:any)=>fn,logError:()=>{},getFsImplementation:()=>dependencies.fs }
 const supported = new Function('dependencies', `with(dependencies){${extract(platform, 'getWslVersion')};${extract(manager, 'isSupportedPlatform')};return isSupportedPlatform()}`)(dependencies)
 const label = new Function('dependencies', `with(dependencies){${cli.slice(cliNode.start,cliNode.end)};return getPlatform()}`)(dependencies)
 return {supported,platform:label}
})
process.stdout.write(JSON.stringify(rows) + '\n')
