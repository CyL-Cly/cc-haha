import fs from 'node:fs'
import {PassThrough} from 'node:stream'
import {join} from 'node:path'
import {assertDisposableFixtureHome} from './fixture-guard'
import {absentManagedSettingsFixturePaths,installClientFixtureCapabilities} from './client-options-capabilities'

const input=await Bun.stdin.json()
const home=assertDisposableFixtureHome(input.home,'Original whole HelpV2')
process.env.ANTHROPIC_API_KEY='owned-help-source-api-key'
if(Bun.version!=='1.3.14'||process.env.CC_HAHA_HELP_V2_SOURCE!=='1')throw new Error('Missing original HelpV2 guard')
fs.mkdirSync(join(home,'.claude'),{recursive:true})
fs.writeFileSync(join(home,'.claude','settings.json'),'{}')
fs.writeFileSync(join(home,'.claude','.claude.json'),JSON.stringify({hasCompletedOnboarding:true,customApiKeyResponses:{approved:['-help-source-api-key'],rejected:[]}}))
const caps=await installClientFixtureCapabilities(home,{absentPaths:absentManagedSettingsFixturePaths()})
;(globalThis as any).MACRO={VERSION:input.version??'999.0.0-help',BUILD_TIME:'',PACKAGE_URL:'https://fixture.invalid/package',NATIVE_PACKAGE_URL:'https://fixture.invalid/package',FEEDBACK_CHANNEL:'local',VERSION_CHANGELOG:'',ISSUES_EXPLAINER:''}
await import('../../../preload')
const bootstrap=await import('../../../src/bootstrap/state')
bootstrap.setCwdState(home);bootstrap.setOriginalCwd(home);bootstrap.setProjectRoot(home);bootstrap.setAllowedSettingSources(['userSettings']);bootstrap.setIsInteractive(true)
const config=await import('../../../src/utils/config');config.enableConfigs()
const React=await import('react')
const {render}=await import('../../../src/ink')
const {ThemeProvider}=await import('../../../src/components/design-system/ThemeProvider')
const {HelpV2}=await import('../../../src/components/HelpV2/HelpV2')
const {KeybindingProvider}=await import('../../../src/keybindings/KeybindingContext')
const {parseBindings}=await import('../../../src/keybindings/parser')
const {DEFAULT_BINDINGS}=await import('../../../src/keybindings/defaultBindings')
const {Parser}=await import('../../../src/ink/termio')
const {builtInCommandNames}=await import('../../../src/commands')
const {AppStoreContext,getDefaultAppState}=await import('../../../src/state/AppState')
const {createStore}=await import('../../../src/state/store')
const helpCommand=(await import('../../../src/commands/help/index')).default
const {processSlashCommand}=await import('../../../src/utils/processUserInput/processSlashCommand')
const {normalizeMessagesForAPI}=await import('../../../src/utils/messages')
const teammate=await import('../../../src/utils/teammate')
const instances=(await import('../../../src/ink/instances')).default
const cells=(value:string)=>new Parser().feed(value).flatMap(action=>action.type==='text'?action.graphemes.map(grapheme=>({text:grapheme.value,style:action.style})):[])
const results=[]
for(const row of input.rows){
 teammate.setDynamicTeamContext(row.pane??null)
 const active=new Set(['Global','Help'])
 const provider={bindings:parseBindings([...DEFAULT_BINDINGS,...(row.bindings??[])]),handlerRegistryRef:{current:new Map()},pendingChordRef:{current:null},pendingChord:null,setPendingChord:()=>{},activeContexts:active,registerActiveContext:(value:string)=>active.add(value),unregisterActiveContext:(value:string)=>active.delete(value)}
 const stdin=new PassThrough() as any;stdin.isTTY=true;stdin.setRawMode=()=>{};stdin.ref=()=>stdin;stdin.unref=()=>stdin
 const stdout=new PassThrough() as any;stdout.isTTY=false;stdout.columns=row.width;stdout.rows=row.height
 let output='',closed:any[]=[]
 stdout.on('data',(value:any)=>output+=value.toString())
 const store=createStore(getDefaultAppState())
 let jsx:any,promiseSettled=false,completion:any
 let pending:Promise<any>|undefined
 if(row.caller){
  pending=processSlashCommand('/help '+(row.args??''),[],[],[],{getAppState:()=>store.getState(),setAppState:(update:any)=>store.setState(update),abortController:new AbortController(),messages:[],options:{commands:[helpCommand,...row.commands],tools:[],isNonInteractiveSession:false,agentDefinitions:{activeAgents:[]}}} as any,(value:any)=>{if(value?.jsx)jsx=value.jsx}).then(value=>{promiseSettled=true;completion=value;closed.push({text:'actual generic completion',options:null});return value})
  const until=Date.now()+5000
  while(!jsx){if(Date.now()>until)throw new Error('Actual help caller did not mount JSX');await new Promise(resolve=>setImmediate(resolve))}
 }
 const view=React.createElement(AppStoreContext.Provider,{value:store},React.createElement(ThemeProvider,{initialState:row.theme??'dark'},React.createElement(KeybindingProvider,provider as any,jsx??React.createElement(HelpV2,{commands:row.commands,onClose:(text:any,options:any)=>closed.push({text,options})}))))
 const app=await render(view,{stdin,stdout,stderr:stdout,exitOnCtrlC:false,patchConsole:false})
 const deadline=Date.now()+5000
 const flush=async()=>{for(let i=0;i<8;i++)await new Promise(resolve=>setImmediate(resolve))}
 const until=async(predicate:()=>boolean)=>{while(!predicate()){if(Date.now()>deadline)throw new Error('Original Help frame did not commit '+JSON.stringify({output,caps}));await new Promise(resolve=>setTimeout(resolve,5))};await flush()}
 await until(()=>output.includes('\x1b[?2026l'))
 let committedFrame=''
 const frame=()=>{if(output.includes('\x1b[?2026l'))committedFrame=output.split('\x1b[?2026h').at(-1)?.replace(/\x1b\[\?2026l$/,'')??'';return committedFrame}
 const snapshots=[]
 const visit=(node:any):any=>({name:node.nodeName,text:node.nodeValue,style:node.style,box:node.yogaNode?{width:node.yogaNode.getComputedWidth(),height:node.yogaNode.getComputedHeight(),left:node.yogaNode.getComputedLeft(),top:node.yogaNode.getComputedTop()}:undefined,children:node.childNodes?.map(visit)})
 const initialLayout=row.captureLayout?visit(instances.get(stdout)!.rootNode):undefined
 const settledBeforeKeys=promiseSettled
 snapshots.push({frame:frame(),cells:cells(frame())})
 for(const step of row.keys??[]){const key=typeof step==='string'?step:step.input;if(typeof step!=='string'&&step.delayBefore)await new Promise(resolve=>setTimeout(resolve,step.delayBefore));output='';stdin.write(key);if(typeof step!=='string'&&step.noRender)await flush();else await until(()=>output.includes('\x1b[?2026l')||closed.length>0);snapshots.push({frame:frame(),cells:cells(frame()),closed:structuredClone(closed)})}
 if(pending&&closed.length)await pending
 results.push({snapshots,closed,settledBeforeKeys,promiseSettled,completion,normalizedAPI:completion?normalizeMessagesForAPI(completion.messages,[],'claude-sonnet-4-5'):undefined,paneAgentId:teammate.getAgentId(),builtinNames:[...builtInCommandNames()],initialLayout,layout:row.captureLayout?visit(instances.get(stdout)!.rootNode):undefined,nativeCells:row.nativeRows?.map((rows:string[])=>cells(rows.join('\n')))})
 app.unmount()
}
if(caps.blocked.length||caps.secureReads.length)throw new Error('Original HelpV2 escaped owned home '+JSON.stringify(caps))
await new Promise<void>((resolve,reject)=>process.stdout.write(JSON.stringify(results),error=>error?reject(error):resolve()))
process.exit(0)
