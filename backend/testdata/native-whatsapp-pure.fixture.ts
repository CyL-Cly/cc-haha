import {createRequire} from 'node:module'
import {dirname,join} from 'node:path'
const adapterRequire=createRequire(new URL('../../adapters/package.json',import.meta.url))
const baileysLeaf=(leaf:string)=>join(dirname(adapterRequire.resolve('@whiskeysockets/baileys')),leaf)
import {readFileSync} from 'node:fs'
import {assertDisposableFixtureHome} from '../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME,'WhatsApp original pure boundary')
if(process.env.CC_HAHA_WHATSAPP_LEAF!=='1')throw new Error('Unowned WhatsApp fixture')
const rows=await Bun.stdin.json()
const {normalizeMessageContent}=await import(baileysLeaf('Utils/messages.js'))
const format=await import('../../adapters/whatsapp/format.ts')
const {getWhatsAppDisconnectStatus}=await import('../../adapters/whatsapp/session.ts')
const text=readFileSync(new URL('../../adapters/whatsapp/index.ts',import.meta.url),'utf8')
const start=text.indexOf('function extractText('),end=text.indexOf('async function handleIncomingMessage',start)
if(start<0||end<0)throw new Error('Original WhatsApp extract functions missing')
const transformed=new Bun.Transpiler({loader:'ts'}).transformSync(text.slice(start,end))
const functions=new Function('normalizeMessageContent',transformed+';return {extractText,isDirectChat}') (normalizeMessageContent)
const incomingStart=text.indexOf('async function handleIncomingMessage'),incomingEnd=text.indexOf('export function useWhatsAppSocket',incomingStart)
const incomingText=new Bun.Transpiler({loader:'ts'}).transformSync(text.slice(incomingStart,incomingEnd))
const out=[]
for(const r of rows){try{switch(r.op){case'inbound':{let route:any,seen:any;const run=new Function('normalizeMessageContent','dedup','collectAttachments','sendWhatsAppText','sock','routeUserMessage',transformed+incomingText+';return handleIncomingMessage')(normalizeMessageContent,{tryRecord(key:any){seen=key;return true}},async()=>({attachments:[{type:'file',path:'/private/fixture'}],rejections:[]}),async()=>{}, {readMessages:async()=>{},sendPresenceUpdate:async()=>{}},async(chatId:any,userId:any,displayName:any,text:any)=>{route={chatId,userId,displayName,text,dedupKey:seen,content:normalizeMessageContent(r.message.message)}});await run(r.message);out.push({value:route??null});break}case'normalize':out.push({value:normalizeMessageContent(r.content)});break;case'text':out.push({value:functions.extractText(r.content)});break;case'direct':out.push({value:functions.isDirectChat(r.jid)});break;case'format':out.push({value:format.formatWhatsAppOutboundText(r.text)});break;case'split':out.push({value:format.splitWhatsAppText(r.text,r.limit)});break;case'preview':out.push({value:format.buildWhatsAppThinkingPreview(r.current,r.delta,r.limit)});break;case'disconnect':out.push({value:getWhatsAppDisconnectStatus(r.error)});break;default:throw new Error('Unknown pure operation')}}catch(e:any){out.push({error:e.message})}}
process.stdout.write(JSON.stringify(out)+'\n')
