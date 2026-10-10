import {connectViaParentProxy} from '@anthropic-ai/sandbox-runtime/dist/sandbox/parent-proxy.js'
const input=JSON.parse(await Bun.stdin.text())
const socket=await connectViaParentProxy(new URL(input.proxy),'target.owned.test',443)
const payload=Buffer.from(input.hex,'hex'),expected=Buffer.from(input.prefix,'hex')
const chunks:Buffer[]=[]
const received=await new Promise<Buffer>((resolve,reject)=>{
 socket.on('error',reject)
 socket.on('data',(chunk:Buffer)=>{chunks.push(chunk);const bytes=Buffer.concat(chunks);if(bytes.length===expected.length+payload.length)resolve(bytes)})
 socket.write(payload);socket.resume()
})
socket.destroy()
process.stdout.write(JSON.stringify({hex:received.toString('hex')})+'\n')
