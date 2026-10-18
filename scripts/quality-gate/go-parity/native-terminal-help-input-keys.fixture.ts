import fs from 'node:fs'
import {createRequire} from 'node:module'
import {join} from 'node:path'
import {PassThrough} from 'node:stream'
import {assertDisposableFixtureHome} from './fixture-guard'
import {absentManagedSettingsFixturePaths,installClientFixtureCapabilities} from './client-options-capabilities'

const input=await Bun.stdin.json()
const home=assertDisposableFixtureHome(input.home,'Original PromptInput help keyboard consumers')
if(Bun.version!=='1.3.14'||process.env.CC_HAHA_HELP_INPUT_KEYS_SOURCE!=='1')throw new Error('Pinned original help keys required')
// Read and compile exact callback/hook bodies before installing the business
// boundary. TypeScript's host case probe belongs to this dependency preparation.
const ts=createRequire(new URL('../../../desktop/package.json',import.meta.url))('typescript')
const path=new URL('../../../src/components/PromptInput/PromptInput.tsx',import.meta.url)
const source=ts.createSourceFile('PromptInput.tsx',fs.readFileSync(path,'utf8'),ts.ScriptTarget.Latest,true,ts.ScriptKind.TSX)
const names=['trackAndSetInput','onChange','handleUndo','handleNewline','handleStash','handleModelPicker','handleThinkingToggle','handleImagePaste','chatHandlers']
const declarations=new Map<string,any>()
let buffer:any,chatBinding:any,helpBinding:any,rawInput:any
function visit(node:any){
 if(ts.isVariableDeclaration(node)&&names.includes(node.name?.text))declarations.set(node.name.text,node)
 if(ts.isVariableDeclaration(node)&&node.initializer?.getText(source).startsWith('useInputBuffer('))buffer=node
 if(ts.isCallExpression(node)&&node.expression.getText(source)==='useKeybindings'&&node.arguments[0]?.getText(source)==='chatHandlers')chatBinding=node
 if(ts.isCallExpression(node)&&node.expression.getText(source)==='useKeybinding'&&node.arguments[0]?.getText(source)==="'help:dismiss'")helpBinding=node
 if(ts.isCallExpression(node)&&node.expression.getText(source)==='useInput'&&node.arguments[0]?.getText(source).includes('showTeamsDialog || showQuickOpen'))rawInput=node
 ts.forEachChild(node,visit)
}
visit(source)
if(declarations.size!==names.length||!buffer||!chatBinding||!helpBinding||!rawInput)throw new Error('Original PromptInput consumers missing')
const exact=names.map(name=>'const '+declarations.get(name).getText(source)+';').join('\n')
const code=new Bun.Transpiler({loader:'tsx'}).transformSync(`function Consumer(){
 const [input,onInputChange]=React.useState(row.draft??'');
 const [cursorOffset,setCursorOffset]=React.useState((row.draft??'').length);
 const [helpOpen,setHelpOpen]=React.useState(row.help??false);
 const [mode,onModeChange]=React.useState('prompt');
 const [stashedPrompt,setStashedPrompt]=React.useState(row.stash);
 const [pastedContents,setPastedContents]=React.useState({});
 const [showModelPicker,setShowModelPicker]=React.useState(false);
 const [showThinkingToggle,setShowThinkingToggle]=React.useState(false);
 const lastInternalInputRef=React.useRef(input);
 const setAppState=useSetAppState();
 const {addNotification}=useNotifications();
 const ${buffer.getText(source)};
 ${exact}
 ${chatBinding.getText(source)};
 ${helpBinding.getText(source)};
 ${rawInput.getText(source)};
 report({helpOpen,input,mode,stash:stashedPrompt?.text??null,showModelPicker,showThinkingToggle});
 return React.createElement(row.vim?VimTextInput:TextInput,{value:input,onChange,onSubmit:(value)=>submitted.push(value),cursorOffset,onChangeCursorOffset:setCursorOffset,focus:true,multiline:true,columns:100,showCursor:false,onUndo:canUndo?handleUndo:undefined});
}`)
fs.mkdirSync(join(home,'.claude'),{recursive:true})
fs.writeFileSync(join(home,'.claude','settings.json'),'{}')
fs.writeFileSync(join(home,'.claude','.claude.json'),JSON.stringify({hasCompletedOnboarding:true}))
const caps=await installClientFixtureCapabilities(home,{absentPaths:absentManagedSettingsFixturePaths()})
;(globalThis as any).MACRO={VERSION:'999.0.0-help-key-source',BUILD_TIME:'',PACKAGE_URL:'https://fixture.invalid/package'}
await import('../../../preload')
const bootstrap=await import('../../../src/bootstrap/state')
bootstrap.setCwdState(home);bootstrap.setOriginalCwd(home);bootstrap.setProjectRoot(home);bootstrap.setAllowedSettingSources(['userSettings']);bootstrap.setIsInteractive(true)
const config=await import('../../../src/utils/config');config.enableConfigs()
const React=await import('react')
const {useCallback,useMemo}=React
const {render,useInput}=await import('../../../src/ink')
const {AppStateProvider,getDefaultAppState,useSetAppState}=await import('../../../src/state/AppState')
const {ThemeProvider}=await import('../../../src/components/design-system/ThemeProvider')
const {VoiceProvider}=await import('../../../src/context/voice')
const {useNotifications}=await import('../../../src/context/notifications')
const {useInputBuffer}=await import('../../../src/hooks/useInputBuffer')
const {useKeybinding,useKeybindings}=await import('../../../src/keybindings/useKeybinding')
const {KeybindingProvider}=await import('../../../src/keybindings/KeybindingContext')
const {parseBindings}=await import('../../../src/keybindings/parser')
const {DEFAULT_BINDINGS}=await import('../../../src/keybindings/defaultBindings')
const {default:TextInput}=await import('../../../src/components/TextInput')
const {default:VimTextInput}=await import('../../../src/components/VimTextInput')
const {getModeFromInput,getValueFromInput}=await import('../../../src/components/PromptInput/inputModes')
const {abortPromptSuggestion}=await import('../../../src/services/PromptSuggestion/promptSuggestion')
const {abortSpeculation}=await import('../../../src/services/PromptSuggestion/speculation')
const {isMacosOptionChar,MACOS_OPTION_SPECIAL_CHARS}=await import('../../../src/utils/keyboardShortcuts')
const {getNativeCSIuTerminalDisplayName}=await import('../../../src/commands/terminalSetup/terminalSetup')
const {getPlatform}=await import('../../../src/utils/platform')
const {getShortcutDisplay}=await import('../../../src/keybindings/shortcutFormat')
const {env}=await import('../../../src/utils/env')
const {logEvent}=await import('../../../src/services/analytics')
const settle=async()=>{for(let i=0;i<12;i++)await new Promise(resolve=>setImmediate(resolve));await new Promise(resolve=>setTimeout(resolve,75))}
const results=[]
for(const row of input.rows){
 let current:any
 const submitted:string[]=[],clipboard:any[]=[]
 const forbid=(label:string)=>()=>{throw new Error('Unproduced Source branch '+label)}
 const deps={React,useCallback,useMemo,useSetAppState,useNotifications,useInputBuffer,useInput,useKeybinding,useKeybindings,TextInput,VimTextInput,row,submitted,logEvent,getModeFromInput,getValueFromInput,abortPromptSuggestion,abortSpeculation,
  dismissStashHint:()=>{},saveGlobalConfig:config.saveGlobalConfig,getShortcutDisplay,env,isMacosOptionChar,MACOS_OPTION_SPECIAL_CHARS,getNativeCSIuTerminalDisplayName,getPlatform,
  // The OS clipboard primitive alone is unavailable in this profile. Preserve
  // the complete original async empty-clipboard notification handler.
  getImageFromClipboard:async()=>{clipboard.push('read');return null},onImagePaste:forbid('nonempty image'),
  handleExternalEditor:forbid('editor'),handleCycleMode:forbid('cycle'),isModalOverlayActive:false,
  showTeamsDialog:false,showQuickOpen:false,showGlobalSearch:false,showHistoryPicker:false,footerItemSelected:null,
  speculation:{status:'inactive'},isSideQuestionVisible:false,onDismissSideQuestion:undefined,queuedCommands:[],isQueuedCommandEditable:forbid('queue'),popAllCommandsFromQueue:forbid('queue pop'),messages:[],isLoading:false,doublePressEscFromEmpty:forbid('selector'),
  report:(value:any)=>current=value}
 const Consumer=new Function(...Object.keys(deps),code+'\nreturn Consumer')(...Object.values(deps))
 const stdin=new PassThrough() as any;stdin.isTTY=true;stdin.setRawMode=()=>{};stdin.ref=()=>stdin;stdin.unref=()=>stdin
 const stdout=new PassThrough() as any;stdout.isTTY=false;stdout.columns=100;stdout.on('data',()=>{})
 const pending={current:null},handlers={current:new Map()},active=new Set(['Chat','Help'])
 const provider={bindings:parseBindings([...DEFAULT_BINDINGS,...(row.bindings??[])]),pendingChordRef:pending,pendingChord:null,setPendingChord:(value:any)=>pending.current=value,handlerRegistryRef:handlers,activeContexts:active,registerActiveContext:(value:any)=>active.add(value),unregisterActiveContext:(value:any)=>active.delete(value)}
 const app=await render(React.createElement(ThemeProvider,{initialState:'dark'},React.createElement(AppStateProvider,{initialState:getDefaultAppState()},React.createElement(VoiceProvider,null,React.createElement(KeybindingProvider,provider as any,React.createElement(Consumer))))),{stdin,stdout,stderr:stdout,patchConsole:false,exitOnCtrlC:false})
 await settle()
 const frames=[]
 for(const key of row.keys){
  if(typeof key!=='string')throw new Error('Original help keys accept physical input only')
  stdin.write(key)
  await settle();frames.push({...current,submitted:[...submitted],clipboard:[...clipboard]})
 }
 app.unmount();await settle();results.push(frames)
}
if(caps.blocked.length||caps.secureReads.length)throw new Error('Original help keyboard escaped private boundary '+JSON.stringify(caps))
await new Promise<void>((resolve,reject)=>process.stdout.write(JSON.stringify({results,blocked:caps.blocked}),error=>error?reject(error):resolve()))
process.exit(0)
