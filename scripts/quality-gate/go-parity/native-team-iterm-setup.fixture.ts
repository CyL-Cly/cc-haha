import { chmod, mkdir, readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import * as children from 'node:child_process'
import { syncBuiltinESMExports } from 'node:module'
import { mock, spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Actual iTerm2 teammate setup producer and UI')
if (process.env.CC_HAHA_TEAM_ITERM_SETUP_FIXTURE !== '1') throw new Error('Missing it2 setup fixture')
const nativeSpawn=children.spawn, nativeBunSpawn=Bun.spawn.bind(Bun)
const capabilities=await installClientFixtureCapabilities(input.homeDir,{absentPaths:absentManagedSettingsFixturePaths()})
const bin=join(input.homeDir,'bin'), log=join(input.homeDir,'log'), config=join(input.homeDir,'fixture-config')
await mkdir(bin,{recursive:true})
for(const name of ['which','uv','pipx','pip','pip3','it2','tmux']){await writeFile(join(bin,name),input.binaryScript);await chmod(join(bin,name),0o755)}
process.env.PATH=`${bin}:/usr/bin:/bin`;process.env.CC_HAHA_ITERM_SETUP_LOG=log;process.env.CC_HAHA_ITERM_SETUP_CONFIG=config
const permit=(file:string,options:any)=>{if(!['which','uv','pipx','pip','pip3','it2','tmux'].some(name=>file===name||file===join(bin,name))||options?.shell||options?.cwd&&!options.cwd.startsWith(input.homeDir))throw new Error('it2 setup subprocess escaped private scripts')}
const spawn=((file:string,args:string[],options:any)=>{permit(file,options);return nativeSpawn(file,args,options)}) as typeof children.spawn
require('node:child_process').spawn=spawn;spyOn(children,'spawn').mockImplementation(spawn)
Bun.spawn=((command:any,nativeOptions:any={})=>{const options=Array.isArray(command)?nativeOptions:command,args=Array.isArray(command)?command:command.cmd;permit(args[0],options);return nativeBunSpawn(command,nativeOptions)}) as typeof Bun.spawn
syncBuiltinESMExports()
await import('../../../preload')
const work=join(input.homeDir,'work');await mkdir(work,{recursive:true})
const state=await import('../../../src/bootstrap/state');state.setCwdState(work);state.setOriginalCwd(work)
const global=await import('../../../src/utils/config');global.enableConfigs()
const setup=await import('../../../src/utils/swarm/backends/it2Setup')
const rows=[]
for(const row of input.rows??[]){
 await writeFile(config,JSON.stringify(row.config));await writeFile(log,'')
 let value:any
 if(row.operation==='detect')value=await setup.detectPythonPackageManager()
 if(row.operation==='install')value=await setup.installIt2(row.manager)
 if(row.operation==='verify')value=await setup.verifyIt2Setup()
 const args=(await readFile(log,'utf8')).trim().split('\n').filter(Boolean).map(line=>JSON.parse(line))
 rows.push({value,args})
}
// Only React hook storage and the input/render primitives are supplied here.
// The entire original compiled It2SetupPrompt runs, including its callbacks,
// install/retry/verification state changes and physical global-config saves.
const React=await import('react')
let slots:any[]=['initial',null,null],index=0,effected=false,managerSettled=false,inputHandler:any
mock.module('react',()=>({...React,default:React.default,useState:(initial:any)=>{const at=index++;return [slots[at]??initial,(next:any)=>{slots[at]=next;if(at===1)managerSettled=true}]},useEffect:(effect:any)=>{if(!effected){effected=true;effect()}}}))
// Observe the original async producer's settlement, including a valid null
// manager result. A fixed 500ms delay raced real private subprocesses under the
// full Source/race lane and attempted selection while the UI was still busy.
const waitForState=async(condition:()=>boolean,label:string)=>{
 const deadline=performance.now()+10000
 while(!condition()){
  if(performance.now()>=deadline)throw new Error(`Original iTerm setup did not settle: ${label}; step=${slots[0]}`)
  await new Promise(resolve=>setTimeout(resolve,5))
 }
}
mock.module('react/compiler-runtime',()=>({c:(n:number)=>Array(n).fill(Symbol.for('react.memo_cache_sentinel'))}))
const ink=await import('../../../src/ink')
mock.module('../../../src/ink',()=>({...ink,useInput:(fn:any)=>{inputHandler=fn}}))
const keybindings=await import('../../../src/keybindings/useKeybinding')
mock.module('../../../src/keybindings/useKeybinding',()=>({...keybindings,useKeybinding:()=>{},useKeybindings:()=>{}}))
mock.module('../../../src/hooks/useExitOnCtrlCDWithKeybindings',()=>({useExitOnCtrlCDWithKeybindings:()=>({})}))
const {It2SetupPrompt}=await import('../../../src/utils/swarm/It2SetupPrompt')
process.env.TERM_PROGRAM='iTerm.app'
const detection=await import('../../../src/utils/swarm/backends/detection')
const registry=await import('../../../src/utils/swarm/backends/registry')
const ui=[]
for(const row of input.ui??[]){
 await writeFile(config,JSON.stringify(row.config));await writeFile(log,'')
 global.saveGlobalConfig((current:any)=>({...current,preferTmuxOverIterm2:false,iterm2It2SetupComplete:false,future:{keep:true}}))
 await detection.isTmuxAvailable()
 slots=['initial',null,null];effected=false;managerSettled=false
 let result:any, finished:()=>void=()=>{}, done=new Promise<void>(resolve=>{finished=resolve})
 const render=()=>{index=0;const tree:any=It2SetupPrompt({tmuxAvailable:true,onDone:(value:any)=>{result=value;finished()}});let select:any;const visit=(node:any)=>{if(!node||typeof node!=='object')return;if(node.props?.options)select=node.props;const value=node.props?.children;if(Array.isArray(value))value.forEach(visit);else visit(value)};visit(tree);return select}
 render();await waitForState(()=>managerSettled,'package manager detection')
 const observations:any[]=[]
 for(const action of row.actions){
  const select=render();observations.push({step:slots[0],options:select?.options??null})
  if(action==='enter')inputHandler('',{return:true});else select.onChange(action)
  await waitForState(()=>!['installing','verifying'].includes(slots[0]),`action ${action}`)
 }
 if(slots[0]==='success')await done
 let backend:any=null
 if(result!=='cancelled'){registry.resetBackendDetection();backend=(await registry.detectAndGetBackend()).backend.type}
 const saved=global.getGlobalConfig()
 ui.push({observations,result,backend,preferTmux:saved.preferTmuxOverIterm2===true,setupComplete:saved.iterm2It2SetupComplete===true,args:(await readFile(log,'utf8')).trim().split('\n').filter(Boolean).map(line=>JSON.parse(line))})
}
process.stdout.write(JSON.stringify({rows,ui,capabilities}))
