import fs from 'node:fs'
import path from 'node:path'
import { mock } from 'bun:test'
import { syncBuiltinESMExports } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

// Leave the original CLI stdin untouched: its startup consumes EOF itself.
const input=JSON.parse(process.env.CC_HAHA_NATIVE_BARE_CLI_INPUT??'null')
const home=assertDisposableFixtureHome(input.homeDir,'Actual bare CLI')
if(process.env.CC_HAHA_NATIVE_BARE_CLI_FIXTURE!=='1'||process.env.NODE_ENV!=='production')throw new Error('Unowned original CLI fixture')
const requests:any[]=[]
const subprocesses:any[]=[]
const server=Bun.serve({hostname:'127.0.0.1',port:0,async fetch(request){
  const endpoint=new URL(request.url).pathname
  const text=await request.text()
  const body=text?JSON.parse(text):null
  requests.push({endpoint,method:request.method,body})
  if(endpoint!=='/v1/messages'||request.method!=='POST')return new Response(JSON.stringify({error:{type:'not_found_error',message:'No model lookup in this fixture'}}),{status:404,headers:{'content-type':'application/json'}})
  const events=[['message_start',{type:'message_start',message:{id:'source-bare',type:'message',role:'assistant',model:'fixture-model',content:[],usage:{input_tokens:1,output_tokens:0}}}],['content_block_start',{type:'content_block_start',index:0,content_block:{type:'text',text:''}}],['content_block_delta',{type:'content_block_delta',index:0,delta:{type:'text_delta',text:'SOURCE_BARE_OK'}}],['content_block_stop',{type:'content_block_stop',index:0}],['message_delta',{type:'message_delta',delta:{stop_reason:'end_turn',stop_sequence:null},usage:{output_tokens:1}}],['message_stop',{type:'message_stop'}]]
  return new Response(events.map(([name,data])=>`event: ${name}\ndata: ${JSON.stringify(data)}\n\n`).join(''),{headers:{'content-type':'text/event-stream','request-id':'source-bare'}})
}})
const absent=[...absentManagedSettingsFixturePaths(),'/Library/Application Support/ClaudeCode/managed-mcp.json','/home/claude/.claude/remote/.api_key','/home/claude/.claude/remote/.oauth_token',path.join(path.dirname(process.execPath),'rg'),'/usr/bin/rg']
for(let directory=path.dirname(fs.realpathSync(home));;directory=path.dirname(directory)){
  for(const suffix of ['.git','.claude/skills','.claude/workflows'])absent.push(path.join(directory,suffix))
  if(directory===path.dirname(directory))break
}
const capabilities=await installClientFixtureCapabilities(home,{loopbackFetchOrigin:server.url.origin,absentPaths:absent})
const processes=require('node:child_process')
for(const name of ['exec','execFile','spawn','execSync','execFileSync','spawnSync']){
  const guarded=processes[name]
  processes[name]=(...args:any[])=>{subprocesses.push({name,command:args[0],arguments:Array.isArray(args[1])?args[1]:undefined});return guarded(...args)}
}
syncBuiltinESMExports()
// Only the external managed-store primitive is absent; the original CLI,
// startup, permissions, tools, query engine and request writer all execute.
mock.module('../../../src/utils/settings/mdm/rawRead',()=>({startMdmRawRead:()=>{},getMdmRawReadPromise:()=>Promise.resolve({plistStdouts:[],hklmStdout:null,hkcuStdout:null}),fireRawRead:async()=>({plistStdouts:[],hklmStdout:null,hkcuStdout:null})}))
process.env.ANTHROPIC_BASE_URL=server.url.origin
process.env.ANTHROPIC_API_KEY='fake-original-cli-key'
process.env.CLAUDE_CODE_SIMPLE='1'
;(globalThis as any).MACRO={VERSION:'source-fixture',BUILD_TIME:'',PACKAGE_URL:'https://fixture.invalid/package'}
await import('../../../preload')
const state=await import('../../../src/bootstrap/state')
state.setCwdState(home);state.setOriginalCwd(home);state.setProjectRoot(home)
process.argv=[process.execPath,new URL('../../../src/entrypoints/cli.tsx',import.meta.url).pathname,'--bare','-p','--model','fixture-model','--system-prompt','actual original bare CLI pool','--no-computer-use','--output-format','stream-json','--verbose','--debug-file',path.join(home,'source-debug.log'),'--setting-sources','user','check actual final bare pool']
process.on('exit',code=>fs.writeFileSync(path.join(home,'original-cli-receipt.json'),JSON.stringify({code,requests,subprocesses,capabilities,marks:performance.getEntriesByType('mark').map(mark=>mark.name)})))
setTimeout(()=>{fs.writeFileSync(path.join(home,'original-cli-timeout.json'),JSON.stringify({requests,subprocesses,capabilities}));process.exit(94)},25000).unref()
await import('../../../src/entrypoints/cli')
