import {createRequire} from 'node:module'
import {dirname,join} from 'node:path'
const adapterRequire=createRequire(new URL('../../adapters/package.json',import.meta.url))
const baileysLeaf=(leaf:string)=>join(dirname(adapterRequire.resolve('@whiskeysockets/baileys')),leaf)
const signalLeaf=(leaf:string)=>join(dirname(createRequire(adapterRequire.resolve('@whiskeysockets/baileys')).resolve('libsignal/package.json')),'src',leaf)
import {assertDisposableFixtureHome} from '../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME,'WhatsApp native thumbnail bytes')
if(process.env.CC_HAHA_WHATSAPP_LEAF!=='1')throw new Error('Unowned WhatsApp thumbnail')
import {WhatsAppMediaService} from '../../adapters/whatsapp/media.ts'
const {prepareWAMessageMedia}=await import(baileysLeaf('Utils/messages.js'))
import {AttachmentStore} from '../../adapters/common/attachment/attachment-store.ts'
const {proto}=await import(baileysLeaf('../WAProto/index.js'))
import {installWhatsAppImageFileProducer} from './whatsapp-image-file-producer'
const restoreFileProducer=installWhatsAppImageFileProducer(adapterRequire.resolve('@whiskeysockets/baileys'))
const input=await Bun.stdin.json()
let result:any
const sock={sendMessage:async(_jid:string,payload:any)=>{const message=await prepareWAMessageMedia(payload,{upload:async()=>({mediaUrl:'https://mmg.whatsapp.net/media',directPath:'/media'})});result=proto.Message.toObject(message,{bytes:String,longs:String}).imageMessage}}as any
await new WhatsAppMediaService(sock,new AttachmentStore()).sendMedia('111@s.whatsapp.net',Buffer.from(input.data,'base64'),input.mime)
restoreFileProducer()
process.stdout.write(JSON.stringify(result)+'\n')
