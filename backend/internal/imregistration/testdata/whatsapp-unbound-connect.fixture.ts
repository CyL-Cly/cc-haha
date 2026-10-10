import fs from 'node:fs'
import path from 'node:path'
import https from 'node:https'
import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import { WebSocketClient } from '../../../../adapters/node_modules/@whiskeysockets/baileys/lib/Socket/Client/websocket.js'
import { startWhatsAppLoginWithQr, pollWhatsAppLoginWithQr, logoutWhatsAppAuth } from '../../../../adapters/whatsapp/protocol'
import { createWhatsAppSocket } from '../../../../adapters/whatsapp/session'
const input = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'))
const home = assertDisposableFixtureHome(input.home, 'actual unbound WhatsApp SDK connecting')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_WHATSAPP_LEAF !== '1') throw new Error('private actual SDK only')
const target = new URL(input.peer)
if (target.protocol !== 'https:' || !['127.0.0.1','localhost','[::1]'].includes(target.hostname)) throw new Error('private TLS transport only')
const actualFetch = globalThis.fetch
;(globalThis as any).fetch = (url:any, options:any) => {
 const text = String(url)
 if (text === 'https://raw.githubusercontent.com/WhiskeySockets/Baileys/master/src/Defaults/index.ts') return actualFetch(input.peer + '/version', options)
 if (text.startsWith(input.peer + '/')) return actualFetch(url, options)
 throw new Error('Unexpected source HTTP target ' + text)
}
const actualConnect = WebSocketClient.prototype.connect
spyOn(WebSocketClient.prototype, 'connect').mockImplementation(function(this:any) {
 if (String(this.url) !== 'wss://web.whatsapp.com/ws/chat') throw new Error('Unexpected source WSS target')
 this.url = new URL(input.peer.replace('https:', 'wss:') + '/ws/chat')
 this.config = {...this.config, agent:new https.Agent({ca:fs.readFileSync(input.ca)})}
 return actualConnect.call(this)
})
let now = 1000, tick: (()=>void)|undefined
spyOn(Date,'now').mockImplementation(()=>now)
const realInterval = globalThis.setInterval, realClear = globalThis.clearInterval
;(globalThis as any).setInterval = (fn:any, delay:number, ...args:any[]) => {
 if(delay===250){tick=fn;return {unref(){return this}}}
 return realInterval(fn,delay,...args)
}
;(globalThis as any).clearInterval = (handle:any) => {if(typeof handle==='object' && handle?.unref && tick) return;return realClear(handle)}
let socketClosed!:()=>void
const closed = new Promise<void>(resolve=>{socketClosed=resolve})
const directory = path.join(home,'source-auth')
const pending = startWhatsAppLoginWithQr({authDir:directory,force:true,createSocket:async options=>{
 const socket=await createWhatsAppSocket(options)
 socket.ev.on('connection.update',update=>{if(update.connection==='close')socketClosed()})
 return socket
}})
try {
 await fetch(input.peer+'/barrier')
 if(!tick)throw new Error('Original waitForQr timer absent')
 now=21001;tick()
 const start=await pending
 const waiting=await pollWhatsAppLoginWithQr({sessionKey:start.sessionKey})
 await fetch(input.peer+'/close')
 await closed
 // The protocol listener's synchronous failure prefix runs in this same emit.
 const terminal=await pollWhatsAppLoginWithQr({sessionKey:start.sessionKey})
 const expired=await pollWhatsAppLoginWithQr({sessionKey:start.sessionKey})
 process.stdout.write(JSON.stringify({start,waiting,terminal,expired,consumer:'actual protocol + createWhatsAppSocket + makeWASocket; real TLS/WSS/Noise client hello, low-level URL/CA remap only'})+'\n')
} finally {
 await logoutWhatsAppAuth(directory)
 globalThis.setInterval=realInterval;globalThis.clearInterval=realClear
}
process.exit(0)
