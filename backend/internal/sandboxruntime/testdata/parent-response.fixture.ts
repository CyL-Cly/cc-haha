import {connectViaParentProxy} from '@anthropic-ai/sandbox-runtime/dist/sandbox/parent-proxy.js'
const input=JSON.parse(await Bun.stdin.text())
try {
 const socket=await connectViaParentProxy(new URL(input.proxy),'target.owned.test',443)
 const chunks:Buffer[]=[]
 const payload=await new Promise<Buffer>((resolve,reject)=>{
   socket.on('error',reject)
   socket.on('data',(b:Buffer)=>{chunks.push(b);if(Buffer.concat(chunks).length>=input.length)resolve(Buffer.concat(chunks))})
   if(input.length===0)resolve(Buffer.alloc(0))
   socket.resume()
 })
 socket.destroy()
 process.stdout.write(JSON.stringify({hex:payload.toString('hex')})+'\n')
}catch(e:any){process.stdout.write(JSON.stringify({error:e.message})+'\n')}
