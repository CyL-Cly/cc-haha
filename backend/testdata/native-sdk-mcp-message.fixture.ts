import {assertDisposableFixtureHome} from '../../scripts/quality-gate/go-parity/fixture-guard'
import {absentManagedSettingsFixturePaths,installClientFixtureCapabilities} from '../../scripts/quality-gate/go-parity/client-options-capabilities'
if(Bun.version!=='1.3.14'||process.env.CC_HAHA_SDK_MCP_MESSAGE_FIXTURE!=='1')throw new Error('Explicit Source MCP message fixture required')
const home=assertDisposableFixtureHome(process.env.HOME,'SDK MCP message')
await installClientFixtureCapabilities(home,{absentPaths:absentManagedSettingsFixturePaths()})
await import('../../preload')
const {StructuredIO}=await import('../../src/cli/structuredIO')
const input=await Bun.stdin.json()
const rows:any[]=[]
for(const row of input.rows){
 let deliver:(line:string)=>void=()=>{}
 const inbound=(async function*(){yield await new Promise<string>(resolve=>deliver=resolve)})()
 const io=new StructuredIO(inbound)
 const reading=(async()=>{for await(const _ of io.structuredInput){}})()
 const result=io.sendMcpMessage('peer',input.message).then(value=>({value}),error=>({error:error.message}))
 const iterator=io.outbound[Symbol.asyncIterator]();const first=await iterator.next()
 if(first.done)throw new Error('Original MCP message not enqueued')
 const event:any=first.value
 if(row.eof)deliver('')
 else deliver(JSON.stringify({type:'control_response',response:{subtype:row.error?'error':'success',request_id:event.request_id,...(row.error?{error:'actual peer error'}:{response:row.response})}})+'\n')
 const outcome=await result;await reading
 rows.push({request:event,...outcome})
}
process.stdout.write(JSON.stringify(rows)+'\n')
