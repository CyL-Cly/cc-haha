import fs from 'node:fs'
import {PassThrough} from 'node:stream'
import {join} from 'node:path'
import {assertDisposableFixtureHome} from './fixture-guard'
import {absentManagedSettingsFixturePaths,installClientFixtureCapabilities} from './client-options-capabilities'

const input=await Bun.stdin.json()
const home=assertDisposableFixtureHome(input.home,'Original prompt shortcut help')
if(input.terminal!==undefined)process.env.TERM_PROGRAM=input.terminal
if(Bun.version!=='1.3.14'||process.env.CC_HAHA_SHORTCUT_HELP_SOURCE!=='1')throw new Error('Missing original shortcut help guard')
fs.mkdirSync(join(home,'.claude'),{recursive:true})
fs.writeFileSync(join(home,'.claude','settings.json'),'{}')
fs.writeFileSync(join(home,'.claude','.claude.json'),JSON.stringify({hasCompletedOnboarding:true}))
const caps=await installClientFixtureCapabilities(home,{absentPaths:absentManagedSettingsFixturePaths()})
;(globalThis as any).MACRO={VERSION:'999.0.0-shortcut-help',BUILD_TIME:'',PACKAGE_URL:'https://fixture.invalid/package'}
await import('../../../preload')
const bootstrap=await import('../../../src/bootstrap/state')
bootstrap.setCwdState(home);bootstrap.setOriginalCwd(home);bootstrap.setProjectRoot(home);bootstrap.setAllowedSettingSources(['userSettings']);bootstrap.setIsInteractive(true)
const config=await import('../../../src/utils/config');config.enableConfigs()
const React=await import('react')
const {ThemeProvider}=await import('../../../src/components/design-system/ThemeProvider')
const {PromptInputHelpMenu}=await import('../../../src/components/PromptInput/PromptInputHelpMenu')
const {KeybindingProvider}=await import('../../../src/keybindings/KeybindingContext')
const {parseBindings}=await import('../../../src/keybindings/parser')
const {DEFAULT_BINDINGS}=await import('../../../src/keybindings/defaultBindings')
const {renderToAnsiString}=await import('../../../src/utils/staticRender')
const {render}=await import('../../../src/ink')
const instances=(await import('../../../src/ink/instances')).default
const {Parser}=await import('../../../src/ink/termio')
const {getNewlineInstructions}=await import('../../../src/components/PromptInput/utils')
const {getPlatform}=await import('../../../src/utils/platform')
const {isFastModeEnabled,isFastModeAvailable}=await import('../../../src/utils/fastMode')
const {isKeybindingCustomizationEnabled}=await import('../../../src/keybindings/loadUserBindings')
const cells=(value:string)=>new Parser().feed(value).flatMap(action=>action.type==='text'?action.graphemes.map(grapheme=>({text:grapheme.value,style:action.style})):[])
const output=[]
for(const row of input.rows){
 process.env.CLAUDE_CODE_DISABLE_FAST_MODE=row.disabledFast?'1':''
 config.saveGlobalConfig(current=>({...current,shiftEnterKeyBindingInstalled:row.installed??false,hasUsedBackslashReturn:row.backslash??false}))
 const active=new Set(['Global','Chat','Help'])
 const provider={bindings:parseBindings([...DEFAULT_BINDINGS,...(row.bindings??[])]),handlerRegistryRef:{current:new Map()},pendingChordRef:{current:null},pendingChord:null,setPendingChord:()=>{},activeContexts:active,registerActiveContext:(value:string)=>active.add(value),unregisterActiveContext:(value:string)=>active.delete(value)}
 const original=await renderToAnsiString(React.createElement(ThemeProvider,{initialState:row.theme},React.createElement(KeybindingProvider,provider as any,React.createElement(PromptInputHelpMenu,{dimColor:true,fixedWidth:true,paddingX:2}))),row.width)
 let layout
 if(row.captureLayout){
  const stdout=new PassThrough() as any;stdout.isTTY=false;stdout.columns=row.width;stdout.on('data',()=>{})
  const app=await render(React.createElement(ThemeProvider,{initialState:row.theme},React.createElement(KeybindingProvider,provider as any,React.createElement(PromptInputHelpMenu,{dimColor:true,fixedWidth:true,paddingX:2}))),{stdout,stderr:stdout,patchConsole:false})
  for(let i=0;i<10;i++)await new Promise(resolve=>setImmediate(resolve))
  const visit=(node:any):any=>({name:node.nodeName,text:node.nodeValue,style:node.style,box:node.yogaNode?{width:node.yogaNode.getComputedWidth(),height:node.yogaNode.getComputedHeight(),left:node.yogaNode.getComputedLeft(),top:node.yogaNode.getComputedTop()}:undefined,children:node.childNodes?.map(visit)})
  layout=visit(instances.get(stdout)!.rootNode);app.unmount()
 }
 output.push({original,cells:cells(original),nativeCells:cells(row.nativeRows.join('\n')),layout,metadata:{Platform:getPlatform(),Newline:getNewlineInstructions(),FastModeAvailable:isFastModeEnabled()&&isFastModeAvailable(),KeybindingsEnabled:isKeybindingCustomizationEnabled()}})
}
if(caps.blocked.length||caps.secureReads.length)throw new Error('Original shortcut help escaped private home '+JSON.stringify(caps))
await new Promise<void>((resolve,reject)=>process.stdout.write(JSON.stringify(output),error=>error?reject(error):resolve()))
process.exit(0)
