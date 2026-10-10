import fs from 'node:fs'
import path from 'node:path'
import { mock, spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'
const input = JSON.parse(await Bun.stdin.text())
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_WHATSAPP_REGISTRATION_FIXTURE !== '1') throw new Error('pinned WhatsApp protocol fixture only')
const home = assertDisposableFixtureHome(input.home, 'WhatsApp protocol')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
let sockets: any[] = [], activeRow:any, warnings:string[]=[]
const originalWarn=console.warn
console.warn=(...values:any[])=>{warnings.push(values.map(String).join(" "))}
// The actual protocol's public createSocket seam, as used by its own tests.
// Session clear/close/wait remain the actual production functions. Dedicated
// physical SDK tests prove the native factory and actual queued-save path.
const createSocket = async (options:any) => {
  if(activeRow?.failAt===sockets.length)throw new Error("private restart failure")
  fs.mkdirSync(options.authDir,{recursive:true,mode:0o700})
  const handlers: Function[] = [], ends:string[]=[]
  let listenerReady!: () => void
  const ready = new Promise<void>(resolve => { listenerReady=resolve })
  const socket = {ready, user:{id:'222:3@s.whatsapp.net'}, ev:{on(event:string,handler:Function){if(event==='connection.update'){handlers.push(handler);listenerReady()}}}, end(error:Error){ends.push(error.message)}, async emit(_event:string,value:any){if(value.qr)options.onQr?.(value.qr);await Promise.all(handlers.map(fn=>fn(value)))}, ends }
  sockets.push(socket);return socket as any
}
const protocol = await import('../../../../adapters/whatsapp/protocol')
let now=1000
spyOn(Date,'now').mockImplementation(()=>now)
const timers = new Map<number,()=>void>();let timerID=0
const realSetInterval=globalThis.setInterval, realClearInterval=globalThis.clearInterval
;(globalThis as any).setInterval=(fn:()=>void)=>{const id=++timerID;timers.set(id,fn);return {id,unref(){return this},ref(){return this}}}
;(globalThis as any).clearInterval=(timer:any)=>{timers.delete(typeof timer === 'number' ? timer : timer.id)}
const tick=()=>{for(const fn of [...timers.values()])fn()}
const rows=[]
for(let index=0;index<input.rows.length;index++){
 const row=input.rows[index],dir=path.join(home,'auth-'+index)
 sockets=[];now=1000;activeRow=row;warnings=[]
 spyOn(crypto,'randomUUID').mockImplementation(()=>('00000000-0000-4000-8000-'+String(index).padStart(12,'0')) as any)
 let completed=false
 const pending=protocol.startWhatsAppLoginWithQr({authDir:dir,force:true,createSocket}).then(result=>{completed=true;return result})
 // Actual createWhatsAppSocket awaits real auth-state reads then our one SDK
 // boundary. Wait for that concrete creation event, without arbitrary sleeps.
 for(let count=0;sockets.length===0;count++){if(count>20000)throw new Error('original socket not created');await Bun.sleep(0)}
 await sockets[0].ready
 if(row.initial==='open')await sockets[0].emit('connection.update',{connection:'open'})
 else if(row.initial!=='timeout')await sockets[0].emit('connection.update',{qr:'qr-1'})
 if(row.initial==='timeout')now=21001+1000
 tick();const start=await pending
 const results=[]
 for(const action of row.actions){
  if(action.kind==='advance')now=1000+action.ms
  if(action.kind==='event'){
   const socket=sockets[action.index??sockets.length-1]
   const update=action.qr?{qr:action.qr}:action.open?{connection:'open'}:{connection:'close',lastDisconnect:{error:action.status===undefined?{}:{output:{statusCode:action.status}}}}
   await socket.emit('connection.update',update)
  }
  if(action.kind==='poll')results.push(await protocol.pollWhatsAppLoginWithQr({sessionKey:start.sessionKey}))
  if(action.kind==='pollPair')results.push(...await Promise.all([protocol.pollWhatsAppLoginWithQr({sessionKey:start.sessionKey}),protocol.pollWhatsAppLoginWithQr({sessionKey:start.sessionKey})]))
  if(action.kind==='logout')await protocol.logoutWhatsAppAuth(dir)
 }
 await protocol.logoutWhatsAppAuth(dir)
 rows.push({start,results,sockets:sockets.length,ends:sockets.map(socket=>socket.ends),completed,warnings})
}
console.warn=originalWarn
globalThis.setInterval=realSetInterval;globalThis.clearInterval=realClearInterval
process.stdout.write(JSON.stringify({rows,capabilities,consumer:'actual protocol.ts public createSocket seam + actual session clear/close/wait functions'}))
