import {createRequire} from 'node:module'
import {dirname,join} from 'node:path'
const adapterRequire=createRequire(new URL('../../../../adapters/package.json',import.meta.url))
const baileysLeaf=(leaf:string)=>join(dirname(adapterRequire.resolve('@whiskeysockets/baileys')),leaf)
const signalLeaf=(leaf:string)=>join(dirname(createRequire(adapterRequire.resolve('@whiskeysockets/baileys')).resolve('libsignal/package.json')),'src',leaf)
import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME,'WhatsApp actual media SDK')
if(process.env.CC_HAHA_WHATSAPP_LEAF!=='1')throw new Error('Unowned media SDK')
import {WhatsAppMediaService} from '../../../../adapters/whatsapp/media.ts'
import {AttachmentStore} from '../../../../adapters/common/attachment/attachment-store.ts'
const {prepareWAMessageMedia,assertMediaContent}=await import(baileysLeaf('Utils/messages.js'))
const {getMediaKeys,encryptMediaRetryRequest,decryptMediaRetryData}=await import(baileysLeaf('Utils/messages-media.js'))
const {aesEncryptGCM,aesDecryptGCM,hkdf}=await import(baileysLeaf('Utils/crypto.js'))
const {proto}=await import(baileysLeaf('../WAProto/index.js'))
import {readFile} from 'node:fs/promises'
import Crypto from 'node:crypto'
const input=await Bun.stdin.json(), warnings:string[]=[]
if(typeof input.now==='number'){const OriginalDate=Date;globalThis.Date=class extends OriginalDate{constructor(...args:any[]){super(...(args.length?args:[input.now])as any)}static now(){return input.now}}as DateConstructor}
const logger={trace(){},debug(){},info(){},warn(value:any){warnings.push(value?.trace??'failed to obtain extra info')},error(){}}
const buf=(s:string)=>Buffer.from(s,'base64')
// The real upload producer first awaits its media-connection IQ. This low-level
// fixture instead waits for the SDK's own encrypted-file digest to be physical;
// encryptedStream currently returns before its WriteStream finish event.
const readPreparedUpload=async(file:string,expected:string)=>{const deadline=performance.now()+5000;for(;;){try{const data=await readFile(file);if(Crypto.createHash('sha256').update(data).digest('base64')===expected)return data}catch(e:any){if(e.code!=='ENOENT')throw e}if(performance.now()>=deadline)throw new Error('SDK encrypted file did not finish with its own digest');await Bun.sleep(1)}}
import {installWhatsAppImageFileProducer} from '../../../testdata/whatsapp-image-file-producer'
const restoreFileProducer=input.op==='prepare' && input.mime?.startsWith('image/') ? installWhatsAppImageFileProducer(adapterRequire.resolve('@whiskeysockets/baileys')) : undefined
let result:any
if(input.op==='prepare'){
 let message:any, encrypted:any
 const sock={logger,async sendMessage(jid:string,payload:any){message=await prepareWAMessageMedia(payload,{logger,upload:async(file:string,upload:any)=>{encrypted=(await readPreparedUpload(file,upload.fileEncSha256B64)).toString('base64');return{mediaUrl:'https://mmg.whatsapp.net/source-media',directPath:'/source-media'}}})}}as any
 await new WhatsAppMediaService(sock,new AttachmentStore()).sendMedia(input.jid??'111@s.whatsapp.net',buf(input.data),input.mime,input.caption,input.name)
 result={message:proto.Message.toObject(message,{bytes:String,longs:String}),encrypted,warnings}
}else if(input.op==='retryContent'){
 const content=assertMediaContent(input.message);content.directPath=input.path;content.url='https://mmg.whatsapp.net'+input.path;result=input.message
}else if(input.op==='decodeMessage'){
 result=proto.Message.toObject(proto.Message.decode(buf(input.data)),{bytes:String,longs:String})
}else if(input.op==='decrypt'){
 const k=await getMediaKeys(input.key,input.type),cipher=buf(input.data), plain=Crypto.createDecipheriv('aes-256-cbc',k.cipherKey,k.iv)
 result={data:Buffer.concat([plain.update(cipher.subarray(0,-10)),plain.final()]).toString('base64')}
}else if(input.op==='encrypt'){
 const k=await getMediaKeys(input.key,input.type),plain=buf(input.data),cipher=Crypto.createCipheriv('aes-256-cbc',k.cipherKey,k.iv)
 const enc=Buffer.concat([cipher.update(plain),cipher.final()]),mac=Crypto.createHmac('sha256',k.macKey).update(k.iv).update(enc).digest().subarray(0,10)
 result={data:Buffer.concat([enc,mac]).toString('base64')}
}else if(input.op==='retryNotification'){
 const key=await hkdf(buf(input.key),32,{info:'WhatsApp Media Retry Notification'}),iv=Crypto.randomBytes(12)
 const plain=proto.MediaRetryNotification.encode(proto.MediaRetryNotification.fromObject({stanzaId:input.id,directPath:input.path,result:input.result??1})).finish()
 result={ciphertext:aesEncryptGCM(plain,key,iv,Buffer.from(input.id)).toString('base64'),iv:iv.toString('base64')}
}else if(input.op==='decodeReceipt'){
 const key=await hkdf(buf(input.key),32,{info:'WhatsApp Media Retry Notification'})
 result=proto.ServerErrorReceipt.toObject(proto.ServerErrorReceipt.decode(aesDecryptGCM(buf(input.ciphertext),key,buf(input.iv),Buffer.from(input.id))))
}else if(input.op==='sourceReceipt'){
 result=await encryptMediaRetryRequest(input.messageKey,buf(input.key),'222:3@s.whatsapp.net')
}else if(input.op==='decryptRetry'){
 result=proto.MediaRetryNotification.toObject(await decryptMediaRetryData({ciphertext:buf(input.ciphertext),iv:buf(input.iv)},buf(input.key),input.id))
}else throw new Error('Unknown actual media operation')
restoreFileProducer?.()
process.stdout.write(JSON.stringify(result,(_k,v)=>v?.type==='Buffer'?Buffer.from(v.data).toString('base64'):v)+'\n')
