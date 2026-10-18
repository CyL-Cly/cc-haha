import fs from 'node:fs'
import { createRequire } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'
import * as cursorModule from '../../../src/utils/Cursor.js'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.home, 'Original Config navigation and AppState callbacks')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TERMINAL_CONFIG_NAVIGATION_SOURCE !== '1') throw new Error('Missing Config navigation guard')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
const source = ts.createSourceFile('Config.tsx', fs.readFileSync(new URL('../../../src/components/Settings/Config.tsx', import.meta.url),'utf8'), ts.ScriptTarget.Latest,true,ts.ScriptKind.TSX)
const initializers = new Map<string,any>()
let navigation: any
function visit(node: any) {
  if (ts.isVariableDeclaration(node)) initializers.set(node.name.getText(source),node.initializer)
  if (ts.isCallExpression(node) && node.expression.getText(source)==='useKeybindings' && node.arguments[0]?.getText(source).includes("'settings:search'")) navigation = node.arguments[0]
  ts.forEachChild(node,visit)
}
visit(source)
function compile(text:string,deps:any) {
 const js=ts.transpileModule('const value = '+text,{compilerOptions:{target:ts.ScriptTarget.ESNext,module:ts.ModuleKind.ESNext,jsx:ts.JsxEmit.React}}).outputText
 return new Function(...Object.keys(deps),js+'\nreturn value')(...Object.values(deps))
}
for (const name of ['handleKeyDown','filteredSettingsItems','revertChanges']) if (!initializers.has(name)) throw new Error('Missing original callback '+name)
if (!navigation) throw new Error('Missing original navigation actions')
const searchSource=ts.createSourceFile('useSearchInput.ts',fs.readFileSync(new URL('../../../src/hooks/useSearchInput.ts',import.meta.url),'utf8'),ts.ScriptTarget.Latest,true,ts.ScriptKind.TS)
let searchHandler:any
function visitSearch(n:any){if(ts.isVariableDeclaration(n)&&n.name.getText(searchSource)==='handleKeyDown')searchHandler=n.initializer;ts.forEachChild(n,visitSearch)}
visitSearch(searchSource)
if(!searchHandler)throw new Error('Missing actual search input callback')
let query='',search=false,selected=0,header=false,cursorOffset=0
const settingsItems=input.rows
const traces=[]
function deps() { return {
 React:{useMemo:(fn:any)=>fn()},useCallback:(fn:any)=>fn,
 settingsItems,filteredSettingsItems:settingsItems,searchQuery:query,isSearchMode:search,headerFocused:header,showSubmenu:null,
 setIsSearchMode:(v:boolean)=>search=v,setSearchQuery:(v:string)=>query=v,setSelectedIndex:(v:number)=>selected=v,setScrollOffset:()=>{},
 selectedIndex:selected,setShowThinkingWarning:()=>{},adjustScrollOffset:()=>{},focusHeader:()=>header=true,
 moveSelection:(delta:number)=>selected=Math.max(0,Math.min(settingsItems.length-1,selected+delta)),toggleSetting:()=>{},
} }
for(const step of input.steps) {
 const d=deps()
 if(step.action) compile(navigation.getText(source),d)[step.action]()
 else if(search && step.key.length===1) compile(searchHandler.getText(searchSource),{...cursorModule,isActive:true,query,effectiveColumns:80,cursorOffset,passthroughCtrlKeys:['c','d'],onExit:()=>search=false,onCancel:undefined,onExitUp:()=>header=true,backspaceExitsOnEmpty:true,isKillKey:()=>false,isYankKey:()=>false,UNHANDLED_SPECIAL_KEYS:new Set(),setQueryState:(v:string)=>query=v,setCursorOffset:(v:number)=>cursorOffset=v})({key:step.key,ctrl:false,meta:false,preventDefault:()=>{}})
 else compile(initializers.get('handleKeyDown').arguments[0].getText(source),d)({key:step.key,ctrl:false,meta:false,preventDefault:()=>{}})
 const filtered=compile(initializers.get('filteredSettingsItems').arguments[0].getText(source),deps())()
 traces.push({query,search,selected,header,ids:filtered.map((r:any)=>r.id)})
}
const stateSource=ts.createSourceFile('onChangeAppState.ts',fs.readFileSync(new URL('../../../src/state/onChangeAppState.ts',import.meta.url),'utf8'),ts.ScriptTarget.Latest,true,ts.ScriptKind.TS)
const callback=stateSource.statements.find((n:any)=>ts.isFunctionDeclaration(n)&&n.name?.text==='onChangeAppState')
if(!callback)throw new Error('Missing actual AppState persistence consumer')
const noop=()=>{}
const js=ts.transpileModule(callback.getText(stateSource).replace('export ',''),{compilerOptions:{target:ts.ScriptTarget.ESNext}}).outputText
let user=structuredClone(input.user),global=structuredClone(input.global),override:any
const merge=(target:any,patch:any)=>{for(const key of Object.keys(patch)){if(patch[key]===undefined)delete target[key];else if(patch[key]&&typeof patch[key]==='object'&&!Array.isArray(patch[key]))merge(target[key]??={},patch[key]);else target[key]=structuredClone(patch[key])}}
const persistDeps={updateSettingsForSource:(_:any,patch:any)=>merge(user,patch),setMainLoopModelOverride:(v:any)=>override=v,
 getGlobalConfig:()=>global,saveGlobalConfig:(fn:any)=>global=fn(global),permissionModeFromString:(v:any)=>v,toExternalPermissionMode:(v:any)=>v,
 notifySessionMetadataChanged:noop,notifyPermissionModeChanged:noop,clearApiKeyHelperCache:noop,clearAwsCredentialsCache:noop,clearGcpCredentialsCache:noop,applyConfigEnvironmentVariables:noop,logError:noop,toError:(e:any)=>e}
const onChangeAppState=new Function(...Object.keys(persistDeps),js+'\nreturn onChangeAppState')(...Object.values(persistDeps))
let app={mainLoopModel:input.base,mainLoopModelForSession:input.session,settings:{},toolPermissionContext:{mode:'default'},effortValue:'medium',fastMode:true}
const initialAppState=app
function setAppState(fn:any){const old=app;app=fn(old);onChangeAppState({oldState:old,newState:app})}
let changeModel:any
function findModel(node:any){if(ts.isFunctionDeclaration(node)&&node.name?.text==='onChangeMainModelConfig')changeModel=node;ts.forEachChild(node,findModel)}
findModel(source)
if(!changeModel)throw new Error('Missing Config model callback')
const modelDeps={mainLoopModel:app.mainLoopModel,setAppState,setChanges:(fn:any)=>fn({}),logEvent:noop,modelDisplayString:(v:any)=>String(v),isBilledAsExtraUsage:()=>false,isOpus1mMergeEnabled:()=>false}
const modelJS=ts.transpileModule(changeModel.getText(source),{compilerOptions:{target:ts.ScriptTarget.ESNext}}).outputText
new Function(...Object.keys(modelDeps),modelJS+'\nreturn onChangeMainModelConfig')(...Object.values(modelDeps))(input.chosen)
const selectedUser=structuredClone(user),selectedApp=structuredClone(app)
const revertDeps={useCallback:(fn:any)=>fn,themeSetting:'dark',initialThemeSetting:{current:'dark'},setTheme:noop,saveGlobalConfig:()=>{},initialConfig:{current:global},
 initialLocalSettings:{},initialUserSettings:{},updateSettingsForSource:(_:any,patch:any)=>merge(user,patch),instructionFilesSettingsPatch:()=>({}),INSTRUCTION_FILES_PLUGIN:'agents-md@builtin',
 clearMemoryFileCaches:noop,getUserContext:{cache:{clear:noop}},feature:()=>false,initialAppState,setAppState,transitionPlanAutoMode:(v:any)=>v,
 getUserMsgOptIn:()=>false,initialUserMsgOptIn:false,setUserMsgOptIn:noop}
compile(initializers.get('revertChanges').arguments[0].getText(source),revertDeps)()
process.stdout.write(JSON.stringify({traces,selectedUser,selectedApp,revertedUser:user,revertedApp:app,override}))
