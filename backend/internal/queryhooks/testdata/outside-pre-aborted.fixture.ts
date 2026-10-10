import fs from 'node:fs'
import {parse} from 'acorn'
import {createCombinedAbortSignal} from '../../../../src/utils/combinedAbortSignal'
const parent = new AbortController()
parent.abort()
const child = createCombinedAbortSignal(parent.signal, {timeoutMs:10000})
const immediatelyAborted = child.signal.aborted
child.cleanup()
const source = new Bun.Transpiler({loader:'ts'}).transformSync(fs.readFileSync(new URL('../../../../src/utils/hooks.ts',import.meta.url),'utf8'))
const ast:any=parse(source,{ecmaVersion:'latest',sourceType:'module'})
const node=ast.body.map((n:any)=>n.type==='ExportNamedDeclaration'?n.declaration:n).find((n:any)=>n?.type==='FunctionDeclaration'&&n.id.name==='executeHooksOutsideREPL')
if(!node||Bun.version!=='1.3.14')throw new Error('actual Source outside required')
let matched=0,spawned=0
const dependencies:any={process:{env:{}},TOOL_HOOK_EXECUTION_TIMEOUT_MS:600000,isEnvTruthy:()=>false,shouldDisableAllHooksIncludingManaged:()=>false,shouldSkipHookDueToTrust:()=>false,getSessionId:()=> 'owned',getMatchingHooks:async()=>{matched++;return[{hook:{type:'command',command:'never'}}]},execCommandHook:()=>{spawned++;throw new Error('pre-aborted Source spawned')},logForDebugging:()=>{}}
const outside=new Function('dependencies',`with(dependencies){${source.slice(node.start,node.end)};return executeHooksOutsideREPL}`)(dependencies)
const result=await outside({hookInput:{hook_event_name:'WorktreeCreate'},signal:parent.signal,timeoutMs:10000})
process.stdout.write(JSON.stringify({immediatelyAborted,matched,spawned,result})+'\n')
