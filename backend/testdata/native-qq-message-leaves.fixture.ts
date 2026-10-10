import { readFile } from 'node:fs/promises'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'QQ message leaves')
if (process.env.CC_HAHA_QQ_MESSAGE_LEAF !== '1') throw new Error('Unowned QQ leaf')
const input = await Bun.stdin.json()
const { QQBot } = await import('../../adapters/node_modules/@tencent-connect/qqbot-nodejs/dist/QQBot.js')
const { extractQqPayload } = await import('../../adapters/qq/extract-payload.ts')
const { normalizeQqAttachmentUrl } = await import('../../adapters/qq/media.ts')
const { dispatchEvent } = await import('../../adapters/node_modules/@tencent-connect/qqbot-nodejs/dist/protocol/gateway/event-dispatcher.js')
const { ReconnectState } = await import('../../adapters/node_modules/@tencent-connect/qqbot-nodejs/dist/protocol/gateway/reconnect.js')
const originalText = await readFile(new URL('../../adapters/qq/index.ts',import.meta.url),'utf8')
const responseText = originalText.slice(originalText.indexOf('class QqResponse'),originalText.indexOf('\nconst port: ChatPort'))
if (!responseText.startsWith('class QqResponse') || !responseText.includes('this.accumulated.slice(this.streamed).trim()')) throw new Error('Original response extraction changed')
const responseCode = new Bun.Transpiler({loader:'ts'}).transformSync(responseText+'\nreturn QqResponse')
const realTimeout = globalThis.setTimeout, realClear = globalThis.clearTimeout
const realFetch = globalThis.fetch
let now = 100000000, row:any, at=0, calls:any[]=[],cache:any[]=[]
let timerId=0, timers=new Map<number,{ms:number,callback:()=>void}>()
globalThis.setTimeout = ((callback:()=>void,ms:number) => {const id=++timerId;timers.set(id,{ms,callback});return id as any}) as typeof setTimeout
globalThis.clearTimeout = ((id:any) => {timers.delete(id)}) as typeof clearTimeout
Date.now=()=>now
Math.random=()=>0
const peer=Bun.serve({hostname:'127.0.0.1',port:0,async fetch(req){
  const logical=req.headers.get('x-fixture-logical-url')!
  const text=await req.text()
  calls.push({url:logical,method:req.method,authorization:req.headers.get('authorization'),contentType:req.headers.get('content-type'),userAgent:req.headers.get('user-agent'),body:text?JSON.parse(text):null})
  const reply=row.replies[at++]??{status:500,body:{message:'unexpected request'}}
  return new Response(reply.raw??JSON.stringify(reply.body),{status:reply.status??200,headers:{'content-type':reply.contentType??'application/json'}})
}})
globalThis.fetch=(async (url:any,init:any)=>{
  const logical=String(url)
  if (!logical.startsWith('https://bots.qq.com/app/getAppAccessToken')&&!logical.startsWith('https://api.sgroup.qq.com/')&&!logical.startsWith('https://cos.qq.private/'))throw new Error('Unowned QQ logical request')
  const headers=new Headers(init.headers);headers.set('x-fixture-logical-url',logical)
  return realFetch(`http://127.0.0.1:${peer.port}/qq`,{...init,headers})
}) as typeof fetch
const pure:any[]=[]
for(const p of input.pure??[]){
  try{if(p.kind==='url')pure.push({value:normalizeQqAttachmentUrl(p.value)?.href??null})
    else if(p.kind==='dispatch')pure.push({value:dispatchEvent(p.event,p.value,'account')})
    else if(p.kind==='reconnect'){const s=new ReconnectState('account');const values=[];for(const a of p.actions){if(a.now!==undefined)now=a.now;if(a.kind==='connected'){s.onConnected();values.push(null)}else if(a.kind==='delay')values.push(s.getNextDelay(a.delay));else values.push(s.handleClose(a.code,a.aborted))};pure.push({value:values})}
    else pure.push({value:extractQqPayload(p.value)})
  }catch(e:any){pure.push({error:e.message})}
}
const rows=[]
for(row of input.rows??[]){
  at=0;calls=[];now=100000000;timers=new Map()
  const bot=new QQBot({appId:' app ',appSecret:'private-secret',...row.credentials})
  let target=row.target??{scope:'c2c',targetId:'private-user',msgId:'private-inbound'}
  const notices=async (_:string,text:string)=>{const {splitMessage}=await import('../../adapters/common/format.ts');for(const chunk of splitMessage(text,1500))await bot.sendText(target,chunk)}
  const ResponseCtor=new Function('targetFor','bot','sendPlainText','QQ_STREAM_THROTTLE_MS',responseCode)(()=>target,bot,notices,600)
  let response:any
  const values=[]
  for(const a of row.actions){if(a.now!==undefined)now=a.now
    try{let value:any
      if(a.kind==='token')value=await bot.api.getToken()
      else if(a.kind==='clearToken'){bot.tokenManager.clearCache(' app ');value=null}
      else if(a.kind==='text')value=await bot.sendText(target,a.text)
      else if(a.kind==='typing')value=await bot.sendTyping(target,30)
      else if(a.kind==='api')value=await bot.api.post(a.path,a.body)
      else if(a.kind==='image'){const r=await bot.sendMedia({target,fileType:1,buffer:Buffer.from(a.base64,'base64'),content:a.alt});value=r.message}
      else if(a.kind==='notice'){await notices('private-chat',a.text);value=null}
      else if(a.kind==='response'){response=new ResponseCtor('private-chat');value=null}
      else if(a.kind==='append'){await response.append(a.text);value=null}
      else if(a.kind==='finish'){await response.finish();value=null}
      else if(a.kind==='retarget'){target=a.target;value=null}
      else if(a.kind==='timer'){const entry=[...timers.entries()].find(([,v])=>v.ms===a.duration);if(!entry)throw new Error('Missing actual SDK timer');timers.delete(entry[0]);entry[1].callback();value=null}
      else throw new Error('Unknown QQ fixture action')
      values.push({value:value??null})
    }catch(e:any){values.push({error:e.message,...(e.name==='ApiError'?{httpStatus:e.httpStatus,path:e.path,bizCode:e.bizCode,bizMessage:e.bizMessage}:{})})}
  }
  rows.push({values,calls});bot.stop()
}
globalThis.setTimeout=realTimeout;globalThis.clearTimeout=realClear
await peer.stop(true)
process.stdout.write(JSON.stringify({pure,rows})+'\n')
