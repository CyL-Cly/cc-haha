import {parse} from 'acorn'
import {assertDisposableFixtureHome} from '../../../scripts/quality-gate/go-parity/fixture-guard'
const input=await Bun.stdin.json()
assertDisposableFixtureHome(input.homeDir,'Claude.ai proxy physical Source')
const target=new URL(input.address)
if(target.protocol!=='http:'||target.hostname!=='127.0.0.1'||!target.port||process.env.CC_HAHA_CLAUDEAI_PROXY_FIXTURE!=='1')throw new Error('Unowned proxy fixture')
const text=new Bun.Transpiler({loader:'ts'}).transformSync(await Bun.file(new URL('../../../src/services/mcp/client.ts',import.meta.url)).text())
const ast:any=parse(text,{ecmaVersion:'latest',sourceType:'module'})
const node=ast.body.map((n:any)=>n.type==='ExportNamedDeclaration'?n.declaration:n).find((n:any)=>n?.type==='FunctionDeclaration'&&n.id.name==='createClaudeAiProxyFetch')
if(!node)throw new Error('Missing original proxy fetch declaration')
let token:any=input.missing?null:{accessToken:'fake-old'}
let checks=0,handled:string[]=[]
const dependencies={Headers,checkAndRefreshOAuthTokenIfNeeded:async()=>{checks++},getClaudeAIOAuthTokens:()=>token,handleOAuth401Error:async(sent:string)=>{handled.push(sent);if(input.change)token={accessToken:'fake-new'};if(input.throwRefresh)throw new Error('owned refresh error');return input.changedResult},logEvent:()=>{}}
const original=new Function('dependencies',`with(dependencies){${text.slice(node.start,node.end)};return createClaudeAiProxyFetch}`)(dependencies)
try {
 const response=await original(fetch)(input.address,{method:'POST',headers:{'X-Owned':'retained',Authorization:'will-be-replaced'},body:'{"jsonrpc":"2.0","id":1,"method":"initialize"}'})
 process.stdout.write(JSON.stringify({status:response.status,body:await response.text(),checks,handled})+'\n')
}catch(e:any){process.stdout.write(JSON.stringify({error:e.message,checks,handled})+'\n')}
