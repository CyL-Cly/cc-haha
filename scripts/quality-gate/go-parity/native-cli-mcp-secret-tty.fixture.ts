import { parse } from 'acorn'
import { assertDisposableFixtureHome } from './fixture-guard'
assertDisposableFixtureHome(process.env.HOME,'CLI MCP raw terminal Source')
if(process.env.CC_HAHA_CLI_MCP_SECRET_TTY_FIXTURE!=='1')throw new Error('Unowned MCP terminal fixture')
const source=new Bun.Transpiler({loader:'ts'}).transformSync(await Bun.file(new URL('../../../src/services/mcp/auth.ts',import.meta.url)).text())
const ast:any=parse(source,{ecmaVersion:'latest',sourceType:'module'})
const node=ast.body.map((n:any)=>n.type==='ExportNamedDeclaration'?n.declaration:n).find((n:any)=>n?.type==='FunctionDeclaration'&&n.id.name==='readClientSecret')
if(!node)throw new Error('Original readClientSecret missing')
const read=new Function(source.slice(node.start,node.end)+'\nreturn readClientSecret')()
let secret='',failure=''
try { secret=await read() } catch (error:any) { failure=error.message }
await Bun.write(new URL('source-secret-receipt.json','file://'+process.env.HOME+'/'),JSON.stringify({secret,failure}))
process.stdout.write('Source secret input finished\n')
process.exit(0)
