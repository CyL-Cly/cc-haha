import fs from 'node:fs'
import path from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input=await Bun.stdin.json()
const home=assertDisposableFixtureHome(input.home,'Original IDE updatedInput actual file consumer')
if(Bun.version!=='1.3.14'||process.env.CC_HAHA_TERMINAL_IDE_FILE_SOURCE!=='1'||process.env.NODE_ENV!=='production')throw new Error('Missing original file consumer guard')
const work=path.join(home,'work'),config=path.join(home,'.claude')
fs.mkdirSync(work,{recursive:true});fs.mkdirSync(config,{recursive:true})
fs.writeFileSync(path.join(config,'.claude.json'),JSON.stringify({hasCompletedOnboarding:true,fileCheckpointingEnabled:false,futureIDEFixture:{keep:true}}))
const capabilities=await installClientFixtureCapabilities(home,{absentPaths:absentManagedSettingsFixturePaths()})
await import('../../../preload')
process.env.CLAUDE_CODE_SIMPLE='1'
const state=await import('../../../src/bootstrap/state')
process.chdir(work);state.setCwdState(process.cwd());state.setOriginalCwd(process.cwd());state.setProjectRoot(process.cwd());state.setIsInteractive(true)
state.setAllowedSettingSources(['userSettings','projectSettings','localSettings','policySettings'])
const configModule=await import('../../../src/utils/config');configModule.enableConfigs()
const {FileReadTool}=await import('../../../src/tools/FileReadTool/FileReadTool')
const {FileEditTool}=await import('../../../src/tools/FileEditTool/FileEditTool')
const {FileWriteTool}=await import('../../../src/tools/FileWriteTool/FileWriteTool')
const {getEmptyToolPermissionContext}=await import('../../../src/Tool')
const {computeEditsFromContents}=await import('../../../src/hooks/useDiffInIDE')
const results=[]
for(const row of input.rows){
 const file=path.join(work,row.name+'.txt');fs.writeFileSync(file,row.old)
 let app={toolPermissionContext:{...getEmptyToolPermissionContext(),mode:'default'}}
 const context:any={getAppState:()=>app,setAppState:(update:any)=>{app=update(app)},readFileState:new Map(),dynamicSkillDirTriggers:new Set(),abortController:new AbortController(),options:{tools:[FileReadTool,FileEditTool,FileWriteTool],agents:[],mainLoopModel:'deepseek-flash'},messages:[],userModified:true,nestedMemoryAttachmentTriggers:new Set()}
 const parent:any={uuid:'private-ide-parent',message:{id:'private-ide-message'}}
 await FileReadTool.call(FileReadTool.inputSchema.parse({file_path:file}),context,undefined,parent)
 const edits=computeEditsFromContents(file,row.old,row.saved,'single')
 const originalInput=row.tool==='Write'?{file_path:file,content:row.proposal}:{file_path:file,old_string:'old',new_string:'new',replace_all:false}
 const first=edits[0]
 const updatedInput=row.tool==='Write'?{...originalInput,content:first?.new_string??originalInput.content}:{...originalInput,...first}
 if(row.physicalSave){
  // Real IDE Save changes bytes before onAllow reaches the actual tool. Force
  // a later physical mtime, as the WS/UI roundtrip does in the product path.
  fs.writeFileSync(file,row.saved);const later=new Date(Date.now()+1000);fs.utimesSync(file,later,later)
 }
 let data:any=null,error:any=null
 try {const tool=row.tool==='Write'?FileWriteTool:FileEditTool;data=(await tool.call(tool.inputSchema.parse(updatedInput),context,undefined,parent)).data}
 catch(cause){error=String((cause as Error).message)}
 results.push({updatedInput,data,error,bytes:fs.readFileSync(file,'utf8')})
}
if(capabilities.blocked.length||capabilities.secureReads.length)throw new Error('Source consumer used forbidden external primitive '+JSON.stringify(capabilities))
await new Promise<void>((resolve,reject)=>process.stdout.write(JSON.stringify({results,capabilities}),error=>error?reject(error):resolve()))
