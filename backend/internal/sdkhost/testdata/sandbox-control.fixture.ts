import {StructuredIO} from '../../../../src/cli/structuredIO'
import {Stream} from '../../../../src/utils/stream'
const input=JSON.parse(await Bun.stdin.text())
const output=[]
for(const row of input.rows){
 const raw=new Stream<string>(),io=new StructuredIO(raw,row.replay)
 const original=io.createSandboxAskCallback()({host:row.host,port:443})
 const frame=(await io.outbound.next()).value
 const waiting=io.getPendingPermissionRequests().length
 // A disconnected proxy client has no signal argument in actual sendRequest.
 const client=new AbortController();client.abort()
 await Promise.resolve()
 const disconnected=io.getPendingPermissionRequests().length
 let replay:any=null
 if(row.close){raw.done();for await(const _ of io.structuredInput){}}
 else {replay=await (io as any).processLine(JSON.stringify({type:'control_response',response:{subtype:row.error?'error':'success',request_id:frame.request_id,...(row.error?{error:'owned SDK host rejected'}:{response:row.response})}}))??null}
 const allowed=await original
 const normalize=(message:any)=>{if(!message)return null;const copy=structuredClone(message);if(copy.request_id)copy.request_id='<request>';if(copy.request?.tool_use_id)copy.request.tool_use_id='<tool>';if(copy.response?.request_id)copy.response.request_id='<request>';return copy}
 output.push({frame:normalize(frame),waiting,disconnected,allowed,remaining:io.getPendingPermissionRequests().length,replay:normalize(replay)})
}
process.stdout.write(JSON.stringify(output)+'\n')
