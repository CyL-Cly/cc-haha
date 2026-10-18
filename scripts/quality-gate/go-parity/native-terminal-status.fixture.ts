import fs from 'node:fs'
import React from 'react'
import path from 'node:path'
import { createRequire } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'

const input = await new Response(Bun.stdin.stream()).json()
const home = assertDisposableFixtureHome(input.home, 'Original Status property and policy consumers')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TERMINAL_STATUS_SOURCE !== '1') throw new Error('Original Status guard required')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
function original(relative: string,names: string[]) {
 const file=new URL('../../../'+relative,import.meta.url).pathname
 const source=ts.createSourceFile(file,fs.readFileSync(file,'utf8'),ts.ScriptTarget.Latest,true,ts.ScriptKind.TSX)
 const declarations=source.statements.filter((node:any)=>ts.isFunctionDeclaration(node)&&names.includes(node.name?.text))
 if(declarations.length!==names.length)throw new Error('Frozen original Status function changed')
 return ts.transpileModule(declarations.map((node:any)=>node.getText(source)).join('\n'),{compilerOptions:{jsx:ts.JsxEmit.React,target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText.replaceAll('export ','')
}
const policyBody=original('src/utils/settings/settings.ts',['loadManagedFileSettings','getManagedFileSettingsPresence','getPolicySettingsOrigin'])+'\nreturn {getPolicySettingsOrigin,getManagedFileSettingsPresence}'
const policy=[]
for(const row of input.policies) {
 const managed=path.join(home,'managed')
 fs.rmSync(managed,{recursive:true,force:true});fs.mkdirSync(managed,{recursive:true})
 if(row.base!==undefined)fs.writeFileSync(path.join(managed,'managed-settings.json'),JSON.stringify(row.base))
 for(const [name,value] of Object.entries(row.drop??{})){fs.mkdirSync(path.join(managed,'managed-settings.d'),{recursive:true});fs.writeFileSync(path.join(managed,'managed-settings.d',name),JSON.stringify(value))}
 const deps={getRemoteManagedSettingsSyncFromCache:()=>row.remote??null,getMdmSettings:()=>({settings:row.admin??{}}),getHkcuSettings:()=>({settings:row.user??{}}),getPlatform:()=>row.platform??'macos',getManagedSettingsFilePath:()=>path.join(managed,'managed-settings.json'),getManagedSettingsDropInDir:()=>path.join(managed,'managed-settings.d'),getFsImplementation:()=>({readdirSync:(file:string)=>fs.readdirSync(file,{withFileTypes:true})}),parseSettingsFile:(file:string)=>{try{return {settings:JSON.parse(fs.readFileSync(file,'utf8')),errors:[]}}catch{return {settings:null,errors:[]}}},mergeWith:(...values:any[])=>Object.assign({},...values.slice(0,-1)),settingsMergeCustomizer:()=>{},getErrnoCode:(error:any)=>error.code,logError:()=>{},join:path.join}
 const f=new Function(...Object.keys(deps),policyBody)(...Object.values(deps))
 policy.push({origin:f.getPolicySettingsOrigin(),...f.getManagedFileSettingsPresence()})
}
const mcpBody=original('src/utils/status.tsx',['buildMcpProperties'])+'\nreturn buildMcpProperties'
const mcp=new Function('color',mcpBody)((_:string)=> (text:string)=>text)
const ideBody=original('src/utils/status.tsx',['buildIDEProperties'])+'\nreturn buildIDEProperties'
function text(value:any):string {if(value==null||typeof value==='boolean')return '';if(typeof value==='string'||typeof value==='number')return String(value);if(Array.isArray(value))return value.map(text).join('');return text(value.props?.children)}
const ideNames:Record<string,string>={vscode:'VS Code',cursor:'Cursor',intellij:'IntelliJ IDEA'}
const ide=new Function('React','Text','color','figures','getIdeClientName','toIDEDisplayName','isJetBrainsIde',ideBody)(React,'text',(_:string)=>(value:string)=>value,{cross:'×'},(client:any)=>client.ideName??null,(kind:string)=>ideNames[kind]??'IDE',(kind:string)=>kind==='intellij')
const modelBody=original('src/utils/model/model.ts',['modelDisplayString','getClaudeAiUserDefaultModelDescription'])+'\n'+original('src/utils/status.tsx',['getModelDisplayLabel'])+'\nreturn getModelDisplayLabel'
const model=input.models.map((row:any)=>{
 const deps={process:{env:row.env??{}},isClaudeAISubscriber:()=>row.subscriber,isOpenAIAuthActive:()=>false,isMaxSubscriber:()=>row.subscription==='max',isTeamPremiumSubscriber:()=>row.subscription==='team'&&row.tier==='default_claude_max_5x',isOpus1mMergeEnabled:()=>row.merge,renderDefaultModelSetting:()=>row.marketing,getDefaultMainLoopModelSetting:()=>row.resolved,getDefaultMainLoopModel:()=>row.resolved,parseUserSpecifiedModel:()=>row.resolved,chalk:{bold:(value:string)=>value},getOpus46PricingSuffix:()=>''}
 const originalModel=new Function(...Object.keys(deps),modelBody)(...Object.values(deps))
 return originalModel(row.model)
})
process.stdout.write(JSON.stringify({policy,mcp:input.clients.map((clients:any[])=>mcp(clients,'dark')),ide:input.ide.map((row:any)=>ide(row.clients,row.installation,'dark').map((property:any)=>({...property,value:text(property.value)}))),model}))
