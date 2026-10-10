import {createRequire} from 'node:module'
import {dirname,join} from 'node:path'
const adapterRequire=createRequire(new URL('../../adapters/package.json',import.meta.url))
const baileysLeaf=(leaf:string)=>join(dirname(adapterRequire.resolve('@whiskeysockets/baileys')),leaf)
const {BufferJSON}=await import(baileysLeaf('Utils/generics.js'))
const {getMediaKeys,hkdfInfoKey}=await import(baileysLeaf('Utils/messages-media.js'))
import {WhatsAppMediaService} from '../../adapters/whatsapp/media.ts'
import {AttachmentStore} from '../../adapters/common/attachment/attachment-store.ts'
import * as fs from 'node:fs/promises'
import * as path from 'node:path'
import {assertDisposableFixtureHome} from '../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME,'WhatsApp original encrypted media')
if(process.env.CC_HAHA_WHATSAPP_LEAF!=='1')throw new Error('Unowned WhatsApp media fixture')
const input=await Bun.stdin.json(), originalFetch=fetch
const calls:any[]=[],sent:any[]=[]
globalThis.fetch=(async(url:any,init:any)=>{const logical=String(url);if(!logical.startsWith('https://mmg.whatsapp.net'))throw new Error('Unowned media HTTP');const h=new Headers(init?.headers);h.set('x-fixture-logical-url',logical);calls.push({url:logical,method:init?.method,origin:h.get('origin')});return originalFetch(input.peerURL+new URL(logical).search,{...init,headers:h})})as typeof fetch
const sock={updateMediaMessage:async()=>{throw new Error('Original unexpected reupload')},logger:{info(){}},sendMessage:async(jid:any,payload:any)=>{sent.push({jid,payload})}}as any
const store=new AttachmentStore(),service=new WhatsAppMediaService(sock,store)
const values=[]
for(const action of input.actions){try{switch(action.op){case'keys':{const k=await getMediaKeys(action.key,action.type);values.push({value:{info:hkdfInfoKey(action.type),iv:k.iv.toString('base64'),cipherKey:k.cipherKey.toString('base64'),macKey:k.macKey.toString('base64')}});break}case'download':{const m=JSON.parse(JSON.stringify(action.message),BufferJSON.reviver);const local=await service.downloadMessageMedia(m,'private-session');values.push({value:local?{kind:local.kind,name:local.name,path:path.relative(process.env.HOME!,local.path),size:local.size,mimeType:local.mimeType,data:local.buffer.toString('base64'),disk:(await fs.readFile(local.path)).toString('base64')}:null});break}case'send':await service.sendMedia(action.jid,Buffer.from(action.data,'base64'),action.mime,action.caption,action.fileName);break;default:throw new Error('Unknown media operation')}}catch(e:any){values.push({error:e.message})}}
process.stdout.write(JSON.stringify({values,calls,sent},BufferJSON.replacer)+'\n')
