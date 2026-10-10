import { parse } from 'acorn'
import { assertDisposableFixtureHome } from './fixture-guard'
const input=await Bun.stdin.json()
assertDisposableFixtureHome(input.homeDir,'CLI MCP eligibility Source')
if(process.env.CC_HAHA_CLI_MCP_ELIGIBILITY_FIXTURE!=='1')throw new Error('Unowned eligibility fixture')
await import('../../../preload')
const {isEnvDefinedFalsy,isBareMode}=await import('../../../src/utils/envUtils')
const declaration=async(path:string,name:string)=>{
 const source=new Bun.Transpiler({loader:'ts'}).transformSync(await Bun.file(new URL(path,import.meta.url)).text())
 const ast:any=parse(source,{ecmaVersion:'latest',sourceType:'module'})
 for(const entry of ast.body){const node=entry.type==='ExportNamedDeclaration'?entry.declaration:entry;if(node?.type==='VariableDeclaration'&&node.declarations.some((d:any)=>d.id.name===name))return source.slice(node.start,node.end)}
 throw new Error('Missing actual Source declaration '+name)
}
const tokens=await declaration('../../../src/utils/auth.ts','getClaudeAIOAuthTokens')
const eligible=await declaration('../../../src/services/mcp/claudeai.ts','fetchClaudeAIMcpConfigsIfEligible')
const rows=[]
for(const row of input.rows){
 let fetched=false
 for(const key of ['ENABLE_CLAUDEAI_MCP_SERVERS','CLAUDE_CODE_OAUTH_TOKEN','ANTHROPIC_API_KEY','ANTHROPIC_AUTH_TOKEN','CLAUDE_CODE_SIMPLE'])delete process.env[key]
 for(const [key,value] of Object.entries(row.environment??{}))process.env[key]=value as string
 process.argv=process.argv.filter((v:string)=>v!=='--bare');if(row.bare)process.argv.push('--bare')
 const deps={memoize:(fn:any)=>fn,isBareMode,getOAuthTokenFromFileDescriptor:()=>null,getSecureStorage:()=>({read:()=>row.secure}),logError:()=>{},isEnvDefinedFalsy,logForDebugging:()=>{},logEvent:()=>{},getOauthConfig:()=>({BASE_API_URL:'http://127.0.0.1:1'}),FETCH_TIMEOUT_MS:5000,MCP_SERVERS_BETA_HEADER:'mcp-servers-2025-12-04',axios:{get:async()=>{fetched=true;return {data:{data:[]}}}}}
 const invoke=new Function('deps',`with(deps){${tokens}\n${eligible}\nreturn fetchClaudeAIMcpConfigsIfEligible}`)(deps)
 await invoke()
 rows.push(fetched)
}
process.stdout.write(JSON.stringify(rows))
