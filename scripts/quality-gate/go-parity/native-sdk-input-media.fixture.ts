import { parse } from 'acorn'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = await Bun.stdin.json()
assertDisposableFixtureHome(input.homeDir, 'SDK input media Source')
if (process.env.CC_HAHA_SDK_INPUT_MEDIA_FIXTURE !== '1') throw new Error('Unowned SDK input media fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setIsInteractive(false)
state.setCwdState(input.homeDir)
state.setOriginalCwd(input.homeDir)
state.setProjectRoot(input.homeDir)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const { processTextPrompt } = await import('../../../src/utils/processUserInput/processTextPrompt')
const { createUserMessage } = await import('../../../src/utils/messages')
const { maybeResizeAndDownsampleImageBlock, createImageMetadataText } = await import('../../../src/utils/imageResizer')
const text = new Bun.Transpiler({loader:'ts'}).transformSync(await Bun.file(new URL('../../../src/utils/processUserInput/processUserInput.ts',import.meta.url)).text())
const ast:any = parse(text,{ecmaVersion:'latest',sourceType:'module'})
const nodes = ast.body.map((n:any)=>n.type==='ExportNamedDeclaration'?n.declaration:n)
const extracted = ['processUserInputBase','addImageMetadataMessage'].map(name=>{
 const node=nodes.find((n:any)=>n.type==='FunctionDeclaration'&&n.id.name===name)
 if(!node)throw new Error('Missing actual input function '+name)
 return text.slice(node.start,node.end)
}).join('\n')
const run = new Function('queryCheckpoint','maybeResizeAndDownsampleImageBlock','createImageMetadataText','feature','processTextPrompt','createUserMessage',extracted+'\nreturn processUserInputBase')(()=>{},maybeResizeAndDownsampleImageBlock,createImageMetadataText,()=>false,processTextPrompt,createUserMessage)
const rows=[]
for(const row of input.rows){
 try{
  const value=await run(row.content,'prompt',()=>{},{options:{commands:[],isNonInteractiveSession:true}},undefined,undefined,[],row.uuid,false,'sdk',undefined,row.mode,true,false,row.meta,true)
  rows.push({content:value.messages[0].message.content, metadata:(value.messages[1]?.message.content??[]).map((b:any)=>b.text), shouldQuery:value.shouldQuery, firstMeta:!!value.messages[0].isMeta})
 }catch(error){rows.push({error:(error as Error).message})}
}
const base64=input.base64.map((s:string)=>Buffer.from(s,'base64').toString('base64'))
if(capabilities.blocked.length)throw new Error('Blocked SDK media Source '+capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({rows,base64,capabilities}))
