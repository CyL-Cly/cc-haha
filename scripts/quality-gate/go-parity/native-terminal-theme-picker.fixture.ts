import fs from 'node:fs'
import { createRequire } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'
const input = await Bun.stdin.json()
assertDisposableFixtureHome(input.home,'Actual ThemeProvider/ThemePicker')
if(Bun.version!=='1.3.14'||process.env.CC_HAHA_THEME_PICKER_SOURCE!=='1')throw new Error('Missing actual Theme picker fixture guard')
const ts=createRequire(new URL('../../../desktop/package.json',import.meta.url))('typescript')
const sentinel=Symbol.for('react.memo_cache_sentinel')
function compile(path:string,deps:any,returns:string){
 const source=ts.createSourceFile(path,fs.readFileSync(new URL(path,import.meta.url),'utf8'),ts.ScriptTarget.Latest,true,ts.ScriptKind.TSX)
 const body=source.statements.filter((n:any)=>!ts.isImportDeclaration(n)).map((n:any)=>n.getText(source).replace(/^export /,'')).join('\n')
 const js=ts.transpileModule(body,{compilerOptions:{target:ts.ScriptTarget.ESNext,module:ts.ModuleKind.ESNext,jsx:ts.JsxEmit.React}}).outputText
 return new Function(...Object.keys(deps),js+'\nreturn '+returns)(...Object.values(deps))
}
const colorModule=await import('../../../src/components/StructuredDiff/colorDiff')
const themes=await import('../../../src/utils/theme')
const {default:wrapText}=await import('../../../src/ink/wrap-text')
const {stringWidth}=await import('../../../src/ink/stringWidth')
const {diffWordsWithSpace}=await import('diff')
const React={createElement:(type:any,props:any,...children:any[])=>({type,props:{...props,children}}),Fragment:'Fragment',memo:(fn:any)=>fn}
function walk(node:any,predicate:any):any {
 if(Array.isArray(node)){for(const child of node){const found=walk(child,predicate);if(found)return found};return null}
 if(!node||typeof node!=='object')return null
 if(predicate(node))return node
 return walk(node.props?.children,predicate)
}
const results=[]
for(const item of input.cases){
 if(process.env.CC_HAHA_THEME_PICKER_TRACE==='1')process.stderr.write(`case ${item.name} width ${item.width}\n`)
 if(item.syntaxEnv===null)delete process.env.CLAUDE_CODE_SYNTAX_HIGHLIGHT;else process.env.CLAUDE_CODE_SYNTAX_HIGHLIGHT=item.syntaxEnv
 let user={...item.user},global={theme:item.theme,futureGlobal:{keep:true}},app={settings:{...item.user}}
 let selected:any=null,canceled=0,saveCalls:any[]=[],writes:any[]=[],bindings:any={},values:any[]=[],cursor=0,context:any
 const state=(initial:any)=>{const slot=cursor++;if(!(slot in values))values[slot]=typeof initial==='function'?initial():initial;return [values[slot],(next:any)=>values[slot]=typeof next==='function'?next(values[slot]):next]}
 const Provider=compile('../../../src/components/design-system/ThemeProvider.tsx',{...React,React,useState:state,useMemo:(fn:any)=>fn(),useEffect:()=>{},createContext:(initial:any)=>({Provider:'Provider',value:initial}),useContext:()=>context,useStdin:()=>({internal_querier:null}),feature:(name:string)=>name==='AUTO_THEME'&&item.autoBuildFeature,getGlobalConfig:()=>global,saveGlobalConfig:(fn:any)=>global=fn(global),getSystemThemeName:()=>item.systemTheme??'dark',_c:(count:number)=>Array(count).fill(sentinel)},'ThemeProvider')
 const pickerCache=Array(59).fill(sentinel)
 const Picker=compile('../../../src/components/ThemePicker.tsx',{React,_c:()=>pickerCache,feature:(name:string)=>name==='AUTO_THEME'&&item.autoBuildFeature,useTheme:()=>[context.currentTheme,context.setThemeSetting],useThemeSetting:()=>context.themeSetting,usePreviewTheme:()=>context,useTerminalSize:()=>({columns:item.width}),useAppState:(fn:any)=>fn(app),useSetAppState:()=>((fn:any)=>app=fn(app)),useRegisterKeybindingContext:()=>{},useShortcutDisplay:()=>item.shortcut??'ctrl+t',useKeybinding:(name:string,fn:any)=>bindings[name]=fn,useExitOnCtrlCDWithKeybindings:()=>({pending:false}),getColorModuleUnavailableReason:colorModule.getColorModuleUnavailableReason,getSyntaxTheme:colorModule.getSyntaxTheme,updateSettingsForSource:(source:string,patch:any)=>{writes.push({source,patch});user={...user,...patch}},gracefulShutdown:()=>{throw new Error('Unexpected original shutdown')},Box:'Box',Text:'Text',Select:'Select',Byline:'Byline',KeyboardShortcutHint:'KeyboardShortcutHint',StructuredDiff:'StructuredDiff'},'ThemePicker')
 function render(){cursor=0;context=Provider({initialState:item.theme,onThemeSave:(theme:string)=>{global={...global,theme};saveCalls.push(theme)},children:null}).props.value;return Picker({skipExitHandling:true,onThemeSelect:(theme:string)=>{selected=theme;context.setThemeSetting(theme)},onCancel:()=>canceled++})}
 let tree=render(),select=walk(tree,(n:any)=>n.type==='Select')
 const focus=select.props.options.some((o:any)=>o.value===item.theme)?item.theme:select.props.options[0].value
 select.props.onFocus(focus);tree=render()
 const observations=[]
 for(const step of item.steps){
  select=walk(tree,(n:any)=>n.type==='Select')
  if(step.action==='focus')select.props.onFocus(step.theme)
  else if(step.action==='toggle')bindings['theme:toggleSyntaxHighlighting']()
  else if(step.action==='accept')select.props.onChange(step.theme)
  else if(step.action==='cancel')select.props.onCancel()
  else if(step.action==='env'){if(step.value===null)delete process.env.CLAUDE_CODE_SYNTAX_HIGHLIGHT;else process.env.CLAUDE_CODE_SYNTAX_HIGHLIGHT=step.value}
  else throw new Error('Unknown original picker action')
  tree=render()
  const texts:any[]=[]
  function collect(n:any){if(Array.isArray(n)){n.forEach(collect);return};if(n?.type==='Text')texts.push(n.props.children.flat(Infinity).filter((v:any)=>typeof v==='string').join(''));if(n?.props)collect(n.props.children)}collect(tree)
  observations.push({saved:context.themeSetting,current:context.currentTheme,global:structuredClone(global),user:structuredClone(user),app:structuredClone(app),selected,canceled,saveCalls:[...saveCalls],writes:structuredClone(writes),label:texts.find((v:string)=>v.includes('Syntax'))??null})
 }
 const color=new (colorModule.expectColorDiff()??class{render(){return null}})({oldStart:1,newStart:1,oldLines:3,newLines:3,lines:[' function greet() {','-  console.log("Hello, World!");','+  console.log("Hello, Claude!");',' }']},null,'demo.js')
 const Fallback=compile('../../../src/components/StructuredDiff/Fallback.tsx',{React,_c:(count:number)=>Array(count).fill(sentinel),useTheme:()=>[context.currentTheme,()=>{}],useMemo:(fn:any)=>fn(),diffWordsWithSpace,stringWidth,wrapText,Box:'Box',Text:'Text',NoSelect:'NoSelect'},'({formatDiff,calculateWordDiffs})')
 const diff=Fallback.formatDiff([' function greet() {','-  console.log("Hello, World!");','+  console.log("Hello, Claude!");',' }'],1,item.width,false,context.currentTheme)
 const styledRows=diff.map((row:any)=>{const spans:any[]=[];function collect(node:any,style:any={}){if(Array.isArray(node)){node.forEach(n=>collect(n,style));return};if(typeof node==='string'||typeof node==='number'){spans.push({...style,text:String(node)});return};if(!node?.props)return;const next=node.type==='Text'?{color:node.props.color??style.color,backgroundColor:node.props.backgroundColor??style.backgroundColor,dim:node.props.dimColor??style.dim}:style;collect(node.props.children,next)}collect(row);return spans})
 results.push({name:item.name,options:select.props.options,observations,stubReason:colorModule.getColorModuleUnavailableReason(),stubSyntax:colorModule.getSyntaxTheme(context.currentTheme),stubRender:color.render(context.currentTheme,item.width,false),styledRows,palette:themes.getTheme(context.currentTheme),moduleResolution:Bun.resolveSync('color-diff-napi',new URL('../../../src/components/StructuredDiff',import.meta.url).pathname)})
}
await new Promise<void>((resolve,reject)=>process.stdout.write(JSON.stringify(results)+'\n',error=>error?reject(error):resolve()))
if(process.env.CC_HAHA_THEME_PICKER_TRACE==='1')process.stderr.write('actual Theme response written\n')
