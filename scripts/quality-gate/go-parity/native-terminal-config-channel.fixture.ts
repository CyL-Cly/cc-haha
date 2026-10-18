import fs from 'node:fs'
import { createRequire } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.home,'Original Config updater reason')
if(Bun.version!=='1.3.14'||process.env.CC_HAHA_TERMINAL_CONFIG_CHANNEL_SOURCE!=='1')throw new Error('Missing Config updater Source guard')
const ts=createRequire(new URL('../../../desktop/package.json',import.meta.url))('typescript')
const source=ts.createSourceFile('config.ts',fs.readFileSync(new URL('../../../src/utils/config.ts',import.meta.url),'utf8'),ts.ScriptTarget.Latest,true,ts.ScriptKind.TS)
const fn=source.statements.find((n:any)=>ts.isFunctionDeclaration(n)&&n.name?.text==='getAutoUpdaterDisabledReason')
if(!fn)throw new Error('Original updater reason identity changed')
const privacy=ts.createSourceFile('privacyLevel.ts',fs.readFileSync(new URL('../../../src/utils/privacyLevel.ts',import.meta.url),'utf8'),ts.ScriptTarget.Latest,true,ts.ScriptKind.TS)
const privacyFn=privacy.statements.find((n:any)=>ts.isFunctionDeclaration(n)&&n.name?.text==='getEssentialTrafficOnlyReason')
const envSource=ts.createSourceFile('env.ts',fs.readFileSync(new URL('../../../src/utils/envUtils.ts',import.meta.url),'utf8'),ts.ScriptTarget.Latest,true,ts.ScriptKind.TS)
const truthy=envSource.statements.find((n:any)=>ts.isFunctionDeclaration(n)&&n.name?.text==='isEnvTruthy')
if(!privacyFn||!truthy)throw new Error('Original env producer missing')
const js=ts.transpileModule([truthy,privacyFn,fn].map((n:any)=>n.getText(n.getSourceFile()).replace('export ','')).join('\n'),{compilerOptions:{target:ts.ScriptTarget.ESNext}}).outputText
const reasons=input.rows.map((row:any)=>new Function('process','getGlobalConfig',js+'\nreturn getAutoUpdaterDisabledReason()')({env:row.env},()=>row.global))
let channelCases:any[]=[]
if(input.channels){
 const config=ts.createSourceFile('Config.tsx',fs.readFileSync(new URL('../../../src/components/Settings/Config.tsx',import.meta.url),'utf8'),ts.ScriptTarget.Latest,true,ts.ScriptKind.TSX)
 let toggle:any,choice:any,enable:any
 function visit(n:any){
  if(ts.isVariableDeclaration(n)&&n.name.getText(config)==='toggleSetting')toggle=n.initializer.arguments[0]
  if(ts.isJsxAttribute(n)&&n.name.text==='onChoice')choice=n.initializer.expression
  if(ts.isArrowFunction(n)&&n.parameters[0]?.name?.text==='channel'&&n.getText(config).includes('autoUpdates: true'))enable=n
  ts.forEachChild(n,visit)
 }
 visit(config)
 if(!toggle||!choice||!enable)throw new Error('Missing original channel callbacks')
 const compile=(fn:any,deps:any)=>new Function(...Object.keys(deps),ts.transpileModule('const value = '+fn.getText(config),{compilerOptions:{target:ts.ScriptTarget.ESNext}}).outputText+'\nreturn value')(...Object.values(deps))
 const merge=(target:any,patch:any)=>{for(const key of Object.keys(patch)){if(patch[key]===undefined)delete target[key];else target[key]=structuredClone(patch[key])}}
 channelCases=input.channels.map((row:any)=>{
  const user=structuredClone(row.user);let settingsData=structuredClone(user),global=structuredClone(row.global),submenu:any=null
  const deps={useCallback:(fn:any)=>fn,filteredSettingsItems:[{id:'autoUpdatesChannel',type:'managedEnum',onChange:()=>{}}],selectedIndex:0,autoUpdaterDisabledReason:row.reason,settingsData,
  isDirty:{current:false},setShowSubmenu:(v:any)=>submenu=v,setTabsHidden:()=>{},updateSettingsForSource:(_:any,patch:any)=>merge(user,patch),setSettingsData:(fn:any)=>settingsData=fn(settingsData),
  logEvent:()=>{},MACRO:{VERSION:row.version},saveGlobalConfig:(fn:any)=>global=fn(global),getGlobalConfig:()=>global,setGlobalConfig:(v:any)=>global=v}
  compile(toggle,deps)()
  if(row.choice && submenu==='ChannelDowngrade')compile(choice,deps)(row.choice)
  if(row.choice && submenu==='EnableAutoUpdates' && row.reason?.type==='config')compile(enable,deps)(row.choice)
  return {user,global,submenu}
 })
}
process.stdout.write(JSON.stringify({reasons,channelCases}))
