import {mock} from 'bun:test'
import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import {absentManagedSettingsFixturePaths,installClientFixtureCapabilities} from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
const home=assertDisposableFixtureHome(input.home,'Thinking picker')
if(process.env.CC_HAHA_TUI_THINKING_FIXTURE!=='1')throw new Error('Unowned thinking picker fixture')
const capabilities=await installClientFixtureCapabilities(home,{absentPaths:absentManagedSettingsFixturePaths()})
await import('../../../../preload')
let bindings:any={}
// Only framework/terminal hook primitives are supplied. Actual ThinkingToggle
// selection, confirmation, options, labels and callbacks remain original code.
mock.module('../../../../src/keybindings/useKeybinding',()=>({useKeybinding:(name:string,handler:any,options:any)=>{if(options?.isActive!==false)bindings[name]=handler},useKeybindings:()=>{}}))
mock.module('../../../../src/hooks/useExitOnCtrlCDWithKeybindings',()=>({useExitOnCtrlCDWithKeybindings:()=>({pending:false,keyName:'Ctrl+C'})}))
const React=await import('react')
const {ThinkingToggle}=await import('../../../../src/components/ThinkingToggle')
const internals=(React as any).__CLIENT_INTERNALS_DO_NOT_USE_OR_WARN_USERS_THEY_CANNOT_UPGRADE
const previous=internals.H
const find=(node:any,predicate:any):any=>{
 if(!node||typeof node!=='object')return null
 if(Array.isArray(node)){for(const child of node){const found=find(child,predicate);if(found)return found};return null}
 if(predicate(node))return node
 return find(node.props?.children,predicate)
}
const textOf=(node:any):string=>typeof node==='string'||typeof node==='number'?String(node):Array.isArray(node)?node.map(textOf).join(''):node&&typeof node==='object'?textOf(node.props?.children):''
const rows=[]
try{
 for(const scenario of input.rows){
  let states:any[]=[];let stateIndex=0;const callbacks:any[]=[];let closed=false
  internals.H={useMemoCache:(n:number)=>Array(n).fill(Symbol.for('react.memo_cache_sentinel')),useState:(initial:any)=>{const i=stateIndex++;if(!(i in states))states[i]=initial;return[states[i],(value:any)=>{states[i]=typeof value==='function'?value(states[i]):value}]},useContext:()=>null,useRef:(value:any)=>({current:value}),useEffect:()=>{},useCallback:(value:any)=>value,useMemo:(fn:any)=>fn()}
  const render=()=>{stateIndex=0;bindings={};return ThinkingToggle({currentValue:scenario.current,isMidConversation:scenario.mid,onSelect:(enabled:boolean)=>{callbacks.push({enabled});closed=true},onCancel:()=>{callbacks.push({cancelled:true});closed=true}})}
  let element=render();const select=find(element,(node:any)=>Array.isArray(node.props?.options)&&typeof node.props?.onChange==='function')
  const options=select.props.options;const initial=select.props.defaultFocusValue
  const stages=[]
  for(const action of scenario.actions){
   if(!closed&&(action==='enabled'||action==='disabled')){const selector=find(element,(node:any)=>typeof node.props?.onChange==='function');selector.props.onChange(action==='enabled'?'true':'false')}
   else if(!closed&&action==='confirm'){bindings['confirm:yes']?.()}
   else if(!closed&&action==='escape'){bindings['confirm:no']?.()}
   if(!closed)element=render();stages.push({confirmation:find(element,(node:any)=>typeof node.props?.onChange==='function')===null,text:textOf(element),callbacks:[...callbacks]})
  }
  rows.push({initial,options,stages})
 }
}finally{internals.H=previous}
if(capabilities.blocked.length)throw new Error('Blocked thinking fixture:'+capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({rows,capabilities:{blocked:capabilities.blocked}})+'\n')
