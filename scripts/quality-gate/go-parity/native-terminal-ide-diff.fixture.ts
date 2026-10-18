import fs from 'node:fs'
import path from 'node:path'
import { createRequire } from 'node:module'
import { structuredPatch } from 'diff'
import { assertDisposableFixtureHome } from './fixture-guard'

const input=await Bun.stdin.json()
assertDisposableFixtureHome(input.home,'Original IDE permission mount/getUpdatedInput')
if(Bun.version!=='1.3.14'||process.env.CC_HAHA_TERMINAL_IDE_DIFF_SOURCE!=='1')throw new Error('Missing IDE diff Source guard')
const ts=createRequire(new URL('../../../desktop/package.json',import.meta.url))('typescript')
function compile(relative:string,names:string[],deps:any,returns:string){
 const source=ts.createSourceFile(relative,fs.readFileSync(new URL('../../../src/'+relative,import.meta.url),'utf8'),ts.ScriptTarget.Latest,true,relative.endsWith('tsx')?ts.ScriptKind.TSX:ts.ScriptKind.TS)
 const nodes=source.statements.filter((node:any)=>ts.isFunctionDeclaration(node)&&names.includes(node.name?.text)||ts.isVariableStatement(node)&&node.declarationList.declarations.some((declaration:any)=>names.includes(declaration.name?.text)))
 if(nodes.length!==names.length)throw new Error('Original declarations changed: '+relative+' '+nodes.length)
 const code=ts.transpileModule(nodes.map((node:any)=>node.getText(source).replace(/^export /,'')).join('\n'),{compilerOptions:{target:ts.ScriptTarget.ESNext}}).outputText
 return new Function(...Object.keys(deps),code+';return '+returns)(...Object.values(deps))
}
const convertLeadingTabsToSpaces=compile('utils/file.ts',['convertLeadingTabsToSpaces'],{},'convertLeadingTabsToSpaces')
const diff=compile('utils/diff.ts',['CONTEXT_LINES','DIFF_TIMEOUT_MS','AMPERSAND_TOKEN','DOLLAR_TOKEN','escapeForDiff','unescapeFromDiff','getPatchFromContents','getPatchForDisplay'],{structuredPatch,convertLeadingTabsToSpaces},'({getPatchFromContents,getPatchForDisplay})')
const edits=compile('tools/FileEditTool/utils.ts',['applyEditToFile','getPatchForEdits','getEditsForPatch'],{...diff,convertLeadingTabsToSpaces},'({getPatchForEdits,getEditsForPatch})')
const connected=compile('utils/ide.ts',['hasAccessToIDEExtensionDiffFeature','getConnectedIdeClient'],{},'({hasAccessToIDEExtensionDiffFeature,getConnectedIdeClient})')
const createSingleEditDiffConfig=compile('components/permissions/FilePermissionDialog/ideDiffConfig.ts',['createSingleEditDiffConfig'],{},'createSingleEditDiffConfig')
const fileRead=await import('../../../src/utils/fileRead')
const supports={
 Edit:compile('components/permissions/FileEditPermissionRequest/FileEditPermissionRequest.tsx',['ideDiffSupport'],{createSingleEditDiffConfig},'ideDiffSupport'),
 Write:compile('components/permissions/FileWritePermissionRequest/FileWritePermissionRequest.tsx',['ideDiffSupport'],{createSingleEditDiffConfig,readFileSync:fileRead.readFileSync,isENOENT:(error:any)=>error?.code==='ENOENT'},'ideDiffSupport'),
}
const results=[]
for(const row of input.rows){
 const filePath=path.join(input.home,row.file)
 if(row.encoding==='utf16le')fs.writeFileSync(filePath,Buffer.from('\ufeff'+row.old,'utf16le'))
 else if(row.old!==null)fs.writeFileSync(filePath,row.old)
 const originalInput={...row.input,file_path:filePath}
 const support=supports[row.tool as keyof typeof supports]
 const config=support.getConfig(originalInput)
 let current=0,state:any[]=[],effects:any[]=[],changes:any[]=[],calls:any[]=[],events:any[]=[],errors:any[]=[]
 const keep=(init:any)=>{const index=current++;if(!(index in state))state[index]=typeof init==='function'?init():init;return [state[index],(value:any)=>{state[index]=typeof value==='function'?value(state[index]):value}]}
 const deps={...edits,...diff,...connected,randomUUID:()=> 'd10f00-actual-source-fixture',basename:path.basename,readFileSync:fileRead.readFileSync,expandPath:(value:string)=>path.resolve(value),isENOENT:(error:any)=>error?.code==='ENOENT',getGlobalConfig:()=>({diffTool:row.diffTool}),getConnectedIdeName:()=> 'Owned Source IDE',getPlatform:()=> 'darwin',WindowsToWSLConverter:class{},logEvent:(name:any)=>events.push(name),logError:(error:any)=>errors.push(String(error)),callIdeRpc:async(name:string,params:any)=>{calls.push({name,params});if(name==='openDiff')return row.reply;if(row.holdCleanup)return new Promise(()=>{});return undefined},useRef:(value:any)=>{const [ref]=keep({current:value});return ref},useState:keep,useMemo:(fn:any)=>{const [value]=keep(fn);return value},useEffect:(fn:any)=>{const index=current++;if(!(index in state)){state[index]=true;effects.push(fn)}}}
 const original=compile('hooks/useDiffInIDE.ts',['useDiffInIDE','computeEditsFromContents','showDiffInIDE','closeTabInIDE','isClosedMessage','isRejectedMessage','isSaveMessage'],deps,'useDiffInIDE')
 const props={onChange:(option:any,value:any)=>changes.push({option,input:support.applyChanges(originalInput,value.edits)}),toolUseContext:{options:{mcpClients:row.connected?[{name:'ide',type:'connected',config:{type:'ws-ide',ideName:'Owned Source IDE'}}]:[]},abortController:new AbortController()},filePath:config.filePath,edits:config.edits.map((edit:any)=>({...edit,replace_all:edit.replace_all||false})),editMode:config.editMode||'single'}
 const render=()=>{current=0;return original(props)}
 let view=render();const cleanup=effects.map(fn=>fn())
 for(let tick=0;tick<8;tick++)await new Promise(resolve=>setImmediate(resolve))
 view=render();cleanup.forEach(fn=>fn?.())
 results.push({proposal:calls.find(call=>call.name==='openDiff')?.params?.new_file_contents??null,changes,calls,events,errors,showingDiffInIDE:view.showingDiffInIDE,hasError:view.hasError})
}
await new Promise<void>((resolve,reject)=>process.stdout.write(JSON.stringify(results),error=>error?reject(error):resolve()))
