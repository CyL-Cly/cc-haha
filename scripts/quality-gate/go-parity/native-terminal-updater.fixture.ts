import fs from 'node:fs'
import { createRequire } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.home,'Original NativeAutoUpdater running ref and interval')
if(Bun.version!=='1.3.14'||process.env.CC_HAHA_TERMINAL_UPDATER_SOURCE!=='1')throw new Error('Missing NativeAutoUpdater Source guard')
const ts=createRequire(new URL('../../../desktop/package.json',import.meta.url))('typescript')
const source=ts.createSourceFile('NativeAutoUpdater.tsx',fs.readFileSync(new URL('../../../src/components/NativeAutoUpdater.tsx',import.meta.url),'utf8'),ts.ScriptTarget.Latest,true,ts.ScriptKind.TSX)
let callback:any,interval:any
function visit(n:any){if(ts.isVariableDeclaration(n)&&n.name.getText(source)==='checkForUpdates')callback=n.initializer.arguments[0];if(ts.isCallExpression(n)&&n.expression.getText(source)==='useInterval')interval=n.arguments[1];ts.forEachChild(n,visit)}
visit(source)
if(!callback||!interval)throw new Error('Missing exact updater callbacks')
const compiled=ts.transpileModule('const callback = '+callback.getText(source),{compilerOptions:{target:ts.ScriptTarget.ESNext}}).outputText
const results=[]
for(const row of input.rows){
 let installs=0;const events:any[]=[],ref={current:row.running??false};let resolveInstall:any
 const pending=new Promise(r=>resolveInstall=r)
 const deps={isUpdatingRef:ref,isAutoUpdaterDisabled:()=>row.disabled??false,onChangeIsUpdating:(v:boolean)=>{ref.current=v;events.push({updating:v})},logForDebugging:()=>{},logEvent:()=>{},logError:()=>{},getErrorType:()=> 'network_error',MACRO:{VERSION:'2.3.4'},getMaxVersion:async()=>null,gt:()=>false,getMaxVersionMessage:async()=>null,setMaxVersionIssue:()=>{},channel:row.channel??'latest',setVersions:(v:any)=>events.push({versions:v}),onAutoUpdaterResult:(v:any)=>events.push({result:v}),installLatest:async()=>{installs++;if(row.hold)return await pending;if(row.error)throw new Error('network_error');return row.result}}
 const fn=new Function(...Object.keys(deps),compiled+'\nreturn callback')(...Object.values(deps))
 const first=fn();if(row.hold){await Promise.resolve();await Promise.resolve();await fn();resolveInstall(row.result)};await first
 results.push({installs,events,running:ref.current})
}
const intervalMs=new Function('return '+interval.getText(source))()
process.stdout.write(JSON.stringify({results,intervalMs}))
