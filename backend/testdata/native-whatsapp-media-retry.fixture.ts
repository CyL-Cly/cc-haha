import {assertDisposableFixtureHome} from '../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME,'WhatsApp original media retry')
if(process.env.CC_HAHA_WHATSAPP_LEAF!=='1')throw new Error('Unowned retry media')
import {WhatsAppMediaService} from '../../adapters/whatsapp/media.ts'
import {AttachmentStore} from '../../adapters/common/attachment/attachment-store.ts'
const input=await Bun.stdin.json(),originalFetch=globalThis.fetch
let retries=0,calls=0
const store=new AttachmentStore()
globalThis.fetch=(async(url:any,init:any)=>{if(!String(url).startsWith('https://mmg.whatsapp.net/'))throw new Error('Unowned URL');calls++;const response=await originalFetch(input.peerURL+new URL(String(url)).pathname,{...init});if(input.mode==='fetch-status'&&calls===1){await response.arrayBuffer();throw Object.assign(new Error('owned fetch status rejection'),{status:input.status})}return response})as typeof fetch
const sock={logger:{info(){}},updateMediaMessage:async(message:any)=>{retries++;message.message.imageMessage.url='https://mmg.whatsapp.net/fresh';return message}}as any
const message={key:{id:'PRIVATE_MEDIA_RETRY',remoteJid:'111@s.whatsapp.net',fromMe:false},message:{imageMessage:{url:'https://mmg.whatsapp.net/expired',mediaKey:input.key,mimetype:'image/png'}}}
let value:any
try{const local=await new WhatsAppMediaService(sock,store).downloadMessageMedia(message as any,'private');value={data:local?.buffer.toString('base64')}}catch(e:any){value={error:e.message,status:e.status,outputStatus:e.output?.statusCode}}
process.stdout.write(JSON.stringify({retries,calls,value})+'\n')
