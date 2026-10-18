import fs from 'node:fs'
import * as fsp from 'node:fs/promises'
import { dirname, join, relative, resolve, sep } from 'node:path'
import { spyOn, mock } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'Original Git branch continuation')
if (process.env.CC_HAHA_GIT_BRANCH_FIXTURE !== '1' || process.env.NODE_ENV !== 'production' || Object.keys(input).some(k => !['home','profile'].includes(k))) throw new Error('Explicit original private Git inputs required')
const local = (value: string) => {
 const path=resolve(home,value)
 if(path!==home&&!path.startsWith(home+sep))throw new Error('Original Git path escaped private HOME')
 let candidate=path
 for(;;){try{fs.lstatSync(candidate);break}catch(error){if(!['ENOENT','ENOTDIR'].includes((error as any).code))throw error;const parent=dirname(candidate);if(parent===candidate)throw error;candidate=parent}}
 const canonical=resolve(fs.realpathSync(candidate),relative(candidate,path))
 if(canonical!==home&&!canonical.startsWith(home+sep))throw new Error('Original Git symlink escaped private HOME')
 return path
}
for(const path of Object.keys(input.profile.files)) local(path)
const absent:string[]=[]
for(let path=dirname(home);;path=dirname(path)){absent.push(join(path,'.git'));if(dirname(path)===path)break}
const caps=await installClientFixtureCapabilities(home,{absentPaths:absent})
;(globalThis as any).MACRO={BUILD_TIME:'',VERSION:'fixture-version',PACKAGE_URL:'',README_URL:'',ISSUES_URL:''}
process.env.CLAUDE_CODE_SIMPLE='1';process.env.CLAUDE_CODE_MANAGED_SETTINGS_PATH=join(home,'policy')
const values:any[]=[];const asyncReads:any[]=[];let watchEvent: (()=>void)|undefined
const watchTrace: Array<{ path: string; phase: string; initialMissing?: boolean }> = []
// Explicit new caller control. The archived original fixture/golden are
// unchanged; only these two actual initial missing-loose-ref callbacks are
// awaited. No sleep, general task drain, cache mutation, or count filtering.
const firstRefCallbacks = new Set<string>()
const firstRefWaiters = new Map<string, () => void>()
const missingRefPaths = ['work/.git/refs/heads/first', 'work/.git/refs/heads/second'].map(local)
const firstRefCallback = async (path: string) => {
 if (firstRefCallbacks.has(path)) return
 await new Promise<void>((resolve, reject) => {
  const timeout = setTimeout(() => reject(new Error('Actual Source initial missing-loose-ref callback did not arrive '+JSON.stringify({watchTrace,values,asyncReads}))), 5000)
  timeout.unref()
  firstRefWaiters.set(path, () => { clearTimeout(timeout); resolve() })
 })
}
const fsActual=await import('fs'), originalWatch=fsActual.watchFile
const watched = ((path:any,options:any,cb:any)=>{
 const p=local(String(path))
 watchTrace.push({path:relative(home,p),phase:'registered'})
 return originalWatch(p,options,(...args:any[])=>{
  cb(...args)
  watchTrace.push({path:relative(home,p),phase:'native-callback',initialMissing:args[0]?.nlink===0&&args[1]?.nlink===0})
  if(missingRefPaths.includes(p)){firstRefCallbacks.add(p);firstRefWaiters.get(p)?.()}
  if(p===local('work/.git/HEAD'))watchEvent?.()
 })
}) as typeof fs.watchFile
mock.module('fs',()=>({...fsActual,watchFile:watched}))
// The capability guard already spied these exports. Bun mutates that mock
// function in place when another spy is installed; capturing the mock itself
// recurses into our observer. Retain its guarded implementation closure.
const originalRead=(fsp.readFile as any).getMockImplementation?.(),originalStat=(fsp.stat as any).getMockImplementation?.()
if(typeof originalRead!=='function'||typeof originalStat!=='function')throw new Error('Original guarded fs implementations unavailable')
spyOn(fsp,'readFile').mockImplementation((async(...args:any[])=>{asyncReads.push({operation:'read',path:relative(home,String(args[0]))});return (originalRead as any)(...args)}) as any)
spyOn(fsp,'stat').mockImplementation((async(...args:any[])=>{asyncReads.push({operation:'stat',path:relative(home,String(args[0]))});return (originalStat as any)(...args)}) as any)
const bootstrap=await import('../../../src/bootstrap/state')
bootstrap.setCwdState(local(input.profile.cwd));bootstrap.setOriginalCwd(local(input.profile.cwd));bootstrap.setProjectRoot(home);bootstrap.setIsInteractive(false)
const git=await import('../../../src/utils/git')
const branch=await import('../../../src/utils/git/gitFilesystem')
branch.resetGitFileWatcher();branch.clearResolveGitDirCache();git.findGitRoot.cache.clear()
const cwdModule=await import('../../../src/utils/cwd');const originalCWD=cwdModule.getCwd,cwdReads:string[]=[];spyOn(cwdModule,'getCwd').mockImplementation(()=>{const value=originalCWD();cwdReads.push(value);return value})
const clockCalls:number[]=[];Date.now=()=>{const n=input.profile.clockStart+clockCalls.length;clockCalls.push(n);return n}
for(const op of input.profile.ops){
 if(op.kind==='parallelBranches'){
  const promises:any[]=[],states=Array(op.count).fill(false)
  for(let i=0;i<op.count;i++){const p=branch.getCachedBranch();void p.then(()=>{states[i]=true});promises.push(p)}
  values.push({kind:'parallelReturned',settled:[...states]});values.push({kind:op.kind,value:await Promise.all(promises)});continue
 }

 if(op.kind==='write'||op.kind==='watchChange'){
  let event:Promise<void>|undefined
  if(op.kind==='watchChange') { await new Promise(r=>setImmediate(r)); if(input.profile.name==='watch-head-dirty') await firstRefCallback(missingRefPaths[0]) }
  if(op.kind==='watchChange') event=new Promise<void>((r,reject)=>{watchEvent=r;setTimeout(()=>reject(new Error('Actual Source watchFile event did not arrive')),5000).unref()})
  const path=local(op.path);fs.mkdirSync(dirname(path),{recursive:true});fs.writeFileSync(path,op.value)
  if(event){await event;await new Promise(r=>setImmediate(r));if(input.profile.name==='watch-head-dirty') await firstRefCallback(missingRefPaths[1]);watchEvent=undefined}
  values.push({kind:op.kind});continue
 }
 if(op.kind==='reset'){branch.resetGitFileWatcher();values.push({kind:op.kind});continue}
 if(op.kind==='clearDirs'){branch.clearResolveGitDirCache();values.push({kind:op.kind});continue}
 if(op.kind==='clearRoots'){git.findGitRoot.cache.clear();values.push({kind:op.kind});continue}
 if(op.kind==='find'){values.push({kind:op.kind,value:git.findGitRoot(local(op.path))});continue}
 let settled=false
 const promise=op.kind==='head'?branch.readGitHead(local(op.path)):op.kind==='resolve'?branch.resolveGitDir(local(op.path)):branch.getCachedBranch()
 void promise.then(()=>{settled=true})
 values.push({kind:'returned',operation:op.kind,settled})
 const value=await promise;values.push({kind:op.kind,value})
}
branch.resetGitFileWatcher()
if(caps.blocked.length)throw new Error('Git fixture capability violation '+JSON.stringify(caps.blocked))
const norm=(v:any):any=>typeof v==='string'?v.split(home).join('<HOME>'):Array.isArray(v)?v.map(norm):v&&typeof v==='object'?Object.fromEntries(Object.entries(v).map(([k,x])=>[k,norm(x)])):v
console.log(JSON.stringify(norm({values,asyncReads,clockCalls,cwdReads,watchTrace,caps:{blocked:caps.blocked,secureReads:caps.secureReads},scope:'actual exported helpers; async IO/branch values; source-only watch callback phase; no full Record'})))
