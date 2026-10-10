import {createRequire} from 'node:module'
import {dirname,join} from 'node:path'
const adapterRequire=createRequire(new URL('../../../../adapters/package.json',import.meta.url))
const baileysLeaf=(leaf:string)=>join(dirname(adapterRequire.resolve('@whiskeysockets/baileys')),leaf)
const signalLeaf=(leaf:string)=>join(dirname(createRequire(adapterRequire.resolve('@whiskeysockets/baileys')).resolve('libsignal/package.json')),'src',leaf)
import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME,'WhatsApp actual message Signal peer')
if(process.env.CC_HAHA_WHATSAPP_LEAF!=='1')throw new Error('Unowned original message peer')
const input=await Bun.stdin.json()
const {makeLibSignalRepository}=await import(baileysLeaf('Signal/libsignal.js'))
const {BufferJSON,unpadRandomMax16}=await import(baileysLeaf('Utils/generics.js'))
const {proto}=await import(baileysLeaf('../WAProto/index.js'))
console.info=()=>{};console.warn=()=>{};console.error=()=>{}
const a=JSON.parse(JSON.stringify(input.a),BufferJSON.reviver)
if(input.closedOnly)for(const entry of Object.values(a.values.session??{}) as any[])for(const state of Object.values(entry._sessions??{}) as any[])state.indexInfo.closed=3333
const keys={async get(type:string,ids:string[]){const result:any={};for(const id of ids)result[id]=a.values[type]?.[id]??null;return result},async set(updates:any){for(const type of Object.keys(updates)){a.values[type]??={};for(const [id,value]of Object.entries(updates[type])){if(value)a.values[type][id]=value;else delete a.values[type][id]}}},async transaction(f:any){return await f()}}
const repo=makeLibSignalRepository({creds:a.creds,keys}as any,{trace(){},debug(){},info(){},warn(){},error(){}}as any)
let packet:any, message:any
if(input.op==='encrypt'){
  const plain=proto.Message.encode(proto.Message.fromObject(input.message)).finish()
  const padded=Buffer.concat([Buffer.from(plain),Buffer.from([1])])
  packet=await repo.encryptMessage({jid:'222@s.whatsapp.net',data:padded})
}else{
  const plain=await repo.decryptMessage({jid:input.jid??'222@s.whatsapp.net',type:input.type,ciphertext:Buffer.from(input.ciphertext,'base64')})
  message=proto.Message.toObject(proto.Message.decode(unpadRandomMax16(plain)),{bytes:String,longs:String})
}
process.stdout.write(JSON.stringify({a,packet,message},BufferJSON.replacer)+'\n')
