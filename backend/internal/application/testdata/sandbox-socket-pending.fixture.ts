import net from 'node:net'
import http from 'node:http'
import {once} from 'node:events'
import {StructuredIO} from '../../../../src/cli/structuredIO'
import {Stream} from '../../../../src/utils/stream'
import {createHttpProxyServer} from '@anthropic-ai/sandbox-runtime/dist/sandbox/http-proxy.js'
const input=JSON.parse(await Bun.stdin.text())
const rows=[]
for(const row of input.rows){
 const raw=new Stream<string>(),io=new StructuredIO(raw,false)
 let upstreamRequests=0
 const target=http.createServer((req,res)=>{upstreamRequests++;res.end('must not contact disconnected target')})
 target.listen(0,'127.0.0.1');await once(target,'listening')
 let callbackFinished!:()=>void
 const finished=new Promise<void>(resolve=>callbackFinished=resolve)
 let acceptedSocket!:net.Socket
 let accept!:()=>void
 const accepted=new Promise<void>(resolve=>accept=resolve)
 const proxy=createHttpProxyServer({filter:async(port:number,host:string,socket:net.Socket)=>{acceptedSocket=socket;accept();const allowed=await io.createSandboxAskCallback()({host,port});callbackFinished();return allowed}})
 proxy.listen(0,'127.0.0.1');await once(proxy,'listening')
 const socket=net.connect((proxy.address()as net.AddressInfo).port,'127.0.0.1');await once(socket,'connect')
 socket.write(`GET http://127.0.0.1:${(target.address()as net.AddressInfo).port}/original HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n`)
 await accepted
 const frame=(await io.outbound.next()).value
 const waiting=io.getPendingPermissionRequests().length
 const peerClosed=once(acceptedSocket,'close')
 socket.destroy()
 await peerClosed
 const disconnected=io.getPendingPermissionRequests().length
 if(row.eof){raw.done();for await(const _ of io.structuredInput){}}
 else await(io as any).processLine(JSON.stringify({type:'control_response',response:{subtype:'success',request_id:frame.request_id,response:{behavior:'allow',updatedInput:{ignored:true},updatedPermissions:[{type:'setMode',mode:'bypassPermissions',destination:'session'}]}}}))
 await finished
 // A callback reaction has finished. server.close callback additionally joins
 // the concrete client socket, without fabricating an arbitrary sleep/drain.
 await new Promise<void>(resolve=>proxy.close(()=>resolve()))
 await new Promise<void>(resolve=>target.close(()=>resolve()))
 rows.push({waiting,disconnected,remaining:io.getPendingPermissionRequests().length,upstreamRequests,toolName:frame.request.tool_name,host:frame.request.input.host,description:frame.request.description})
}
process.stdout.write(JSON.stringify(rows)+'\n')
