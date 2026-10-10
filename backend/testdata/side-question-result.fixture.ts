import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'side question result Source')
if (process.env.CC_HAHA_SIDE_QUESTION_FIXTURE !== '1') throw new Error('Missing fixture marker')
await import('../../preload')
const input=await Bun.stdin.json()
let current:any[]=[]
const fork=await import('../../src/utils/forkedAgent')
mock.module('../../src/utils/forkedAgent',()=>({...fork,runForkedAgent:async(options:any)=>{for(const message of current)options.onMessage?.(message);return{messages:current,totalUsage:{}}}}))
const {runSideQuestion}=await import('../../src/utils/sideQuestion')
const revive=(spec:any):any=>{if(spec===null || typeof spec!=='object')return spec;if(spec.actualError){const error=new Error(spec.message??'');if(spec.message===undefined)delete(error as any).message;for(const name of ['code','status','error'])if(name in spec)(error as any)[name]=spec[name];if('cause'in spec)(error as any).cause=revive(spec.cause);return error}return spec}
const rows=[]
for(const history of input.histories){current=history.map((row:any)=>row.type==='system'&&row.subtype==='api_error'?{...row,error:revive(row.error)}:row)
 try{const result=await runSideQuestion({question:'current',cacheSafeParams:{forkContextMessages:[]} as any});rows.push({response:result.response})}catch(e:any){rows.push({error:e.message})}
}
process.stdout.write(JSON.stringify(rows));process.exit(0)
