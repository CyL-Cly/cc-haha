import {mock} from 'bun:test'
import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import {absentManagedSettingsFixturePaths,installClientFixtureCapabilities} from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
const home=assertDisposableFixtureHome(input.home,'Heap dump')
if(process.env.CC_HAHA_HEAP_DUMP_FIXTURE!=='1')throw new Error('Unowned heap dump fixture')
const capabilities=await installClientFixtureCapabilities(home,{absentPaths:[...absentManagedSettingsFixturePaths(),'/proc/self/fd','/proc/self/smaps_rollup']})
const events:any[]=[]
const physicalFS=await import('fs')
const physicalAsync=await import('fs/promises')
const physicalWrite=physicalAsync.writeFile,physicalWriteSync=physicalFS.writeFileSync,physicalMkdir=physicalAsync.mkdir
const check=(stage:string)=>{if(input.fail===stage)throw new Error('physical '+stage+' failure')}
mock.module('fs/promises',()=>({...physicalAsync,writeFile:async(path:any,data:any,options:any)=>{events.push(['diagnostics',String(path),options]);check('diagnostics');return physicalWrite(path,data,options)}}))
mock.module('fs',()=>({...physicalFS,writeFileSync:(path:any,data:any,options:any)=>{events.push(['heap',String(path),options]);check('heap');return physicalWriteSync(path,data,options)}}))
// This is only the runtime engine primitive; the original workflow, naming,
// diagnostic-before-snapshot IO, error handling and command formatting run.
Bun.generateHeapSnapshot=(()=>new Uint8Array([1,2,3])) as any
Bun.gc=(()=>{events.push(['gc'])}) as any
await import('../../../../preload')
;(globalThis as any).MACRO={VERSION:'fixture',BUILD_TIME:''}
const fsOps=await import('../../../../src/utils/fsOperations')
const fsImplementation=fsOps.getFsImplementation()
const originalMkdir=fsImplementation.mkdir.bind(fsImplementation)
fsImplementation.mkdir=async(path:string)=>{events.push(['mkdir',path]);check('mkdir');await originalMkdir(path)}
const state=await import('../../../../src/bootstrap/state');state.switchSession(input.sid)
const service=await import('../../../../src/utils/heapDumpService')
const memoryUsage=process.memoryUsage
process.memoryUsage=(()=>{events.push(['capture']);return memoryUsage()}) as any
const result=await service.performHeapDump(input.trigger,input.number)
if(capabilities.blocked.length)throw new Error('Blocked original heap dump:'+capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({events,result})+'\n')
