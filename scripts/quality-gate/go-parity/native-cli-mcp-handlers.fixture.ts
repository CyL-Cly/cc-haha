import { parse } from 'acorn'
import { Command, Option } from '@commander-js/extra-typings'
import { assertDisposableFixtureHome } from './fixture-guard'
const input=await Bun.stdin.json()
assertDisposableFixtureHome(input.homeDir,'CLI MCP handler Source')
if(process.env.CC_HAHA_CLI_MCP_HANDLERS_FIXTURE!=='1')throw new Error('Unowned CLI MCP handler fixture')
await import('../../../preload')
const {ensureConfigScope,ensureTransport,parseHeaders,getScopeLabel}=await import('../../../src/services/mcp/utils')
const {parseEnvVars}=await import('../../../src/utils/envUtils')
const {safeParseJSON}=await import('../../../src/utils/json')
const extract=async(path:string,names:string[])=>{
 const source=new Bun.Transpiler({loader:path.endsWith('tsx')?'tsx':'ts'}).transformSync(await Bun.file(new URL(path,import.meta.url)).text())
 const ast:any=parse(source,{ecmaVersion:'latest',sourceType:'module'})
 const nodes=ast.body.map((n:any)=>n.type==='ExportNamedDeclaration'?n.declaration:n)
 return names.map(name=>{const node=nodes.find((n:any)=>n.type==='FunctionDeclaration'&&n.id.name===name);if(!node)throw new Error('Missing Source handler '+name);return source.slice(node.start,node.end)}).join('\n')
}
const add=await extract('../../../src/commands/mcp/addCommand.ts',['registerMcpAddCommand'])
const handlers=await extract('../../../src/cli/handlers/mcp.tsx',['checkMcpServerHealth','mcpRemoveHandler','mcpListHandler','mcpGetHandler','mcpAddJsonHandler','mcpResetChoicesHandler'])
const rows=[]
for(const row of input.rows){
 const stdout:string[]=[];const stderr:string[]=[];const effects:any[]=[]
 const originalOut=process.stdout.write,originalErr=process.stderr.write,originalLog=console.log
 process.stdout.write=((s:any)=>{stdout.push(String(s));return true}) as any
 process.stderr.write=((s:any)=>{stderr.push(String(s));return true}) as any
 console.log=(...args:any[])=>stdout.push(args.map(String).join(' ')+'\n')
 const configs=row.configs??{}
 // process.exit never returns through the handler's catch. A getter preserves
 // that boundary when its catch tries to inspect a synthetic exit's message.
 const leave=(exit:number)=>{const error={exit};Object.defineProperty(error,'message',{get(){throw error}});throw error}
 const cliOk=(message?:string)=>{if(message)stdout.push(message+'\n');leave(0)}
 const cliError=(message?:string)=>{if(message)stderr.push(message+'\n');leave(1)}
 const deps={Option,cliOk,cliError,ensureConfigScope,ensureTransport,parseHeaders,parseEnvVars,safeParseJSON,getScopeLabel,
  logEvent:()=>{},jsonStringify:JSON.stringify,isXaaEnabled:()=>row.xaaEnabled??false,getXaaIdpSettings:()=>row.xaaIdp,
  readClientSecret:async()=>{effects.push({op:'read-secret'});if(row.secretError)throw new Error(row.secretError);return row.secret??''},
  addMcpConfig:async(name:string,config:any,scope:string)=>{effects.push({op:'add',name,config,scope});if(row.addError)throw new Error(row.addError)},
  removeMcpConfig:async(name:string,scope:string)=>{effects.push({op:'remove',name,scope});if(row.removeError)throw new Error(row.removeError)},
  saveMcpClientSecret:(name:string,config:any,secret:string)=>effects.push({op:'save-secret',name,config,secret}),
  describeMcpConfigFilePath:(scope:string)=>row.locations?.[scope]??(scope==='project'?input.homeDir+'/.mcp.json':scope==='local'?input.homeDir+'/.claude/.claude.json [project: '+input.homeDir+']':scope==='user'?input.homeDir+'/.claude/.claude.json':scope),
  getMcpConfigByName:(name:string)=>configs[name],getCurrentProjectConfig:()=>row.project??{},getGlobalConfig:()=>row.global??{},
  getMcpConfigsByScope:()=>({servers:row.projectServers??{}}),getAllMcpConfigs:async()=>({servers:configs}),
  connectToServer:async(name:string)=>{effects.push({op:'connect',name});if(row.health?.[name]==='throw')throw new Error('physical connection failure');return {type:row.health?.[name]??'connected'}},
  getMcpServerConnectionBatchSize:()=>3,pMap:async(values:any[],mapper:any)=>Promise.all(values.map(mapper)),
  clearServerTokensFromLocalStorage:(name:string,config:any)=>effects.push({op:'clear-token',name,config}),clearMcpClientConfig:(name:string,config:any)=>effects.push({op:'clear-client',name,config}),getMcpClientConfig:()=>row.clientConfig,
  saveCurrentProjectConfig:(updater:any)=>effects.push({op:'project',value:updater(row.project??{})}),
  gracefulShutdown:async(code=0)=>{effects.push({op:'shutdown',code})},
 }
 let code=0
 try{
  const functions=new Function('deps',`with(deps){${add}\n${handlers}\nreturn {registerMcpAddCommand,mcpRemoveHandler,mcpListHandler,mcpGetHandler,mcpAddJsonHandler,mcpResetChoicesHandler}}`)(deps)
  if(row.action==='add'){
   const program=new Command('claude');program.exitOverride();program.configureOutput({writeOut:(s:string)=>stdout.push(s),writeErr:(s:string)=>stderr.push(s)});const mcp=program.command('mcp').enablePositionalOptions();functions.registerMcpAddCommand(mcp);await program.parseAsync(['node','fixture','mcp','add',...row.args])
  }else if(row.action==='add-json')await functions.mcpAddJsonHandler(row.name,row.json,row.options??{})
  else if(row.action==='remove')await functions.mcpRemoveHandler(row.name,row.options??{})
  else if(row.action==='list')await functions.mcpListHandler()
  else if(row.action==='get')await functions.mcpGetHandler(row.name)
  else if(row.action==='reset')await functions.mcpResetChoicesHandler()
  else throw new Error('Unowned handler action')
 }catch(error:any){if(error?.exit!==undefined)code=error.exit;else{code=1;stderr.push(error.message+'\n')}}
 finally {process.stdout.write=originalOut;process.stderr.write=originalErr;console.log=originalLog}
 rows.push({code,stdout:stdout.join(''),stderr:stderr.join(''),effects})
}
process.stdout.write(JSON.stringify({rows}))
