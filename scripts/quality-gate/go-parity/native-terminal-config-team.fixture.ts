import fs from 'node:fs'
import { createRequire } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'

const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.home,'Actual Config team snapshot callbacks')
if(Bun.version!=='1.3.14'||process.env.CC_HAHA_TERMINAL_CONFIG_TEAM_SOURCE!=='1')throw new Error('Missing actual team Config guard')
const ts=createRequire(new URL('../../../desktop/package.json',import.meta.url))('typescript')
const source=(relative:string)=>ts.createSourceFile(relative,fs.readFileSync(new URL('../../../src/'+relative,import.meta.url),'utf8'),ts.ScriptTarget.Latest,true,relative.endsWith('tsx')?ts.ScriptKind.TSX:ts.ScriptKind.TS)
const modes=source('utils/swarm/backends/teammateModeSnapshot.ts')
const names=['setCliTeammateModeOverride','getCliTeammateModeOverride','clearCliTeammateModeOverride','captureTeammateModeSnapshot','getTeammateModeFromSnapshot']
const modeNodes=modes.statements.filter((node:any)=>(ts.isFunctionDeclaration(node)&&names.includes(node.name?.text))||(ts.isVariableStatement(node)&&node.declarationList.declarations.some((declaration:any)=>['initialTeammateMode','cliTeammateModeOverride'].includes(declaration.name?.text))))
if(modeNodes.length!==7)throw new Error('Original teammate mode snapshot declarations changed')
const modeJS=ts.transpileModule(modeNodes.map((node:any)=>node.getText(modes).replace(/^export /,'')).join('\n'),{compilerOptions:{target:ts.ScriptTarget.ESNext}}).outputText
const config=source('components/Settings/Config.tsx')
let modeCallback:any,modelCallback:any,toggleCallback:any
const scan=(node:any)=>{
 if(ts.isObjectLiteralExpression(node)&&node.properties.some((property:any)=>property.name?.text==='id'&&property.initializer?.text==='teammateMode'))modeCallback=node.properties.find((property:any)=>property.name?.text==='onChange')
 if(ts.isJsxSelfClosingElement(node)&&node.tagName.getText(config)==='ModelPicker'&&node.attributes.properties.some((attribute:any)=>attribute.name?.text==='skipSettingsWrite'))modelCallback=node.attributes.properties.find((attribute:any)=>attribute.name?.text==='onSelect')?.initializer?.expression
 if(ts.isVariableDeclaration(node)&&node.name?.text==='toggleSetting'&&ts.isCallExpression(node.initializer))toggleCallback=node.initializer.arguments[0]
 ts.forEachChild(node,scan)
}
scan(config)
if(!modeCallback||!modelCallback||!toggleCallback)throw new Error('Actual Config team callbacks changed')
const modeCallbackJS=ts.transpileModule('const original={'+modeCallback.getText(config)+'}',{compilerOptions:{target:ts.ScriptTarget.ESNext}}).outputText
const modelCallbackJS=ts.transpileModule('const original='+modelCallback.getText(config),{compilerOptions:{target:ts.ScriptTarget.ESNext}}).outputText
const toggleCallbackJS=ts.transpileModule('const originalToggle='+toggleCallback.getText(config),{compilerOptions:{target:ts.ScriptTarget.ESNext}}).outputText
const traces=[]
for(const row of input.rows){
 let global=structuredClone(row.global),writes=0,dirty=false
 const snapshot=new Function('getGlobalConfig','logForDebugging','logError',modeJS+';return {setCliTeammateModeOverride,getCliTeammateModeOverride,clearCliTeammateModeOverride,captureTeammateModeSnapshot,getTeammateModeFromSnapshot}')(()=>global,()=>{},()=>{})
 if(row.cli)snapshot.setCliTeammateModeOverride(row.cli)
 snapshot.captureTeammateModeSnapshot()
 const initial={global:structuredClone(global),mode:snapshot.getTeammateModeFromSnapshot(),override:snapshot.getCliTeammateModeOverride()}
 const dependencies=()=>({globalConfig:global,isDirty:{get current(){return dirty},set current(value){dirty=value}},saveGlobalConfig:(updater:any)=>{const value=updater(global);if(value!==global){global=value;writes++}},setGlobalConfig:(value:any)=>{global=value},getGlobalConfig:()=>global,clearCliTeammateModeOverride:snapshot.clearCliTeammateModeOverride,setChanges:()=>{},teammateModelDisplayString:(value:any)=>value??"Default (leader's model)",setShowSubmenu:()=>{},setTabsHidden:()=>{},logEvent:()=>{}})
 const trace=[initial]
 for(const step of row.steps){
  if(step.kind==='mode'){
   const dependenciesValue=dependencies()
   const callback=new Function(...Object.keys(dependenciesValue),modeCallbackJS+';return original.onChange')(...Object.values(dependenciesValue))
   const item={id:'teammateMode',type:'enum',value:global.teammateMode??'auto',options:['auto','tmux','in-process'],onChange:callback}
   const expected=item.options[(item.options.indexOf(item.value)+1)%item.options.length]
   if(step.value!==expected)throw new Error('Mode fixture must use actual Config next-choice consumer')
   new Function(...Object.keys(dependenciesValue),'filteredSettingsItems','selectedIndex',toggleCallbackJS+';originalToggle()')(...Object.values(dependenciesValue),[item],0)
  }
  else if(step.kind==='model'){const dependenciesValue=dependencies();new Function(...Object.keys(dependenciesValue),modelCallbackJS+';original(arguments[arguments.length-1],"high")')(...Object.values(dependenciesValue),step.value)}
  else if(step.kind==='escape'){if(dirty)global=structuredClone(initial.global)}
  else if(step.kind==='external-global-write'){global={...global,teammateMode:step.value}}
  else throw new Error('Unknown original mode trace step')
  trace.push({global:structuredClone(global),mode:snapshot.getTeammateModeFromSnapshot(),override:snapshot.getCliTeammateModeOverride(),writes,dirty})
 }
 traces.push(trace)
}
await new Promise<void>((resolve,reject)=>process.stdout.write(JSON.stringify(traces),error=>error?reject(error):resolve()))
