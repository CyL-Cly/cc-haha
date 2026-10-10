import {parse} from 'acorn'
import axios from 'axios'
import {assertDisposableFixtureHome} from '../../../scripts/quality-gate/go-parity/fixture-guard'
const input=await Bun.stdin.json()
assertDisposableFixtureHome(input.homeDir,'Claude.ai organization catalog physical Source')
if(process.env.CC_HAHA_CLAUDEAI_CATALOG_FIXTURE!=='1')throw new Error('Unowned catalog fixture')
const target=new URL(input.address)
if(target.protocol!=='http:'||target.hostname!=='127.0.0.1'||!target.port)throw new Error('Non-loopback catalog transport')
await import('../../../preload')
const {isEnvDefinedFalsy}=await import('../../../src/utils/envUtils')
const {normalizeNameForMCP}=await import('../../../src/services/mcp/normalization')
const text=new Bun.Transpiler({loader:'ts'}).transformSync(await Bun.file(new URL('../../../src/services/mcp/claudeai.ts',import.meta.url)).text())
const ast:any=parse(text,{ecmaVersion:'latest',sourceType:'module'})
const node=ast.body.map((n:any)=>n.type==='ExportNamedDeclaration'?n.declaration:n).find((n:any)=>n?.type==='VariableDeclaration'&&n.declarations.some((d:any)=>d.id.name==='fetchClaudeAIMcpConfigsIfEligible'))
if(!node)throw new Error('Missing actual catalog Source declaration')
for(const [key,value] of Object.entries(input.environment??{}))process.env[key]=value as string
const dependencies={axios,memoize:(fn:any)=>fn,getClaudeAIOAuthTokens:()=>input.tokens,isEnvDefinedFalsy,getOauthConfig:()=>({BASE_API_URL:input.address}),logForDebugging:()=>{},logEvent:()=>{},FETCH_TIMEOUT_MS:5000,MCP_SERVERS_BETA_HEADER:'mcp-servers-2025-12-04',normalizeNameForMCP}
const original=new Function('dependencies',`with(dependencies){${text.slice(node.start,node.end)};return fetchClaudeAIMcpConfigsIfEligible}`)(dependencies)
process.stdout.write(JSON.stringify(await original())+'\n')
