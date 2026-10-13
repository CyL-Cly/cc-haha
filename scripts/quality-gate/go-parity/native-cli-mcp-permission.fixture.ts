import { parse } from 'acorn'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input=await Bun.stdin.json()
const home=assertDisposableFixtureHome(input.home,'MCP permission prompt Source')
if(process.env.CC_HAHA_MCP_PERMISSION_FIXTURE!=='1')throw new Error('Unowned MCP permission fixture')
await installClientFixtureCapabilities(home,{absentPaths:absentManagedSettingsFixturePaths()})
await import('../../../preload')
const text=await Bun.file(new URL('../../../src/cli/print.ts',import.meta.url)).text()
const code=new Bun.Transpiler({loader:'tsx'}).transformSync(text)
const ast=parse(code,{ecmaVersion:'latest',sourceType:'module'}) as any
const nodes=ast.body.map((node:any)=>node.type==='ExportNamedDeclaration'?node.declaration:node)
const node=nodes.find((node:any)=>node?.type==='FunctionDeclaration'&&node.id?.name==='createCanUseToolWithPermissionPrompt')
if(!node)throw new Error('Missing original MCP permission prompt producer')
const { createCombinedAbortSignal }=await import('../../../src/utils/combinedAbortSignal')
const { outputSchema,permissionPromptToolResultToPermissionDecision }=await import('../../../src/utils/permissions/PermissionPromptToolResultSchema')
const { safeParseJSON }=await import('../../../src/utils/json')
const create=new Function('deps',`with(deps){${code.slice(node.start,node.end)};return createCanUseToolWithPermissionPrompt}`)({createCombinedAbortSignal,permissionToolOutputSchema:outputSchema,permissionPromptToolResultToPermissionDecision,safeParseJSON,hasPermissionsToUseTool:()=>{throw new Error('Fixture must supply the original forceDecision input')}})
const rows=[]
for(const row of input.rows){
 const controller=new AbortController(),calls:any[]=[]
 if(row.aborted)controller.abort()
 const context:any={abortController:controller,setAppState:()=>{throw new Error('Unexpected Source updates')},getAppState:()=>({})}
 const prompt:any={name:'mcp__private__approval',call:async(args:any)=>{calls.push(args);if(row.abortDuring)controller.abort();return {data:row.content}},mapToolResultToToolResultBlockParam:(data:any)=>({content:data})}
 try{const result=await create(prompt)({name:'Write'},row.input??{file_path:'private.txt',content:'original'},context,{},'actual-tool-use',row.decision??{behavior:'ask'});rows.push({decision:result,calls,aborted:controller.signal.aborted})}catch(error:any){rows.push({error:error.name==='ZodError'?error.message:error.message,calls,aborted:controller.signal.aborted})}
}
process.stdout.write(JSON.stringify(rows))
