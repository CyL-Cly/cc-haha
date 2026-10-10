import {assertDisposableFixtureHome}from'../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME,'QQ real stream lifecycle')
if(process.env.CC_HAHA_QQ_MESSAGE_LEAF!=='1')throw new Error('Unowned stream fixture')
const input=await Bun.stdin.json()
const {QQBot}=await import('../../adapters/node_modules/@tencent-connect/qqbot-nodejs/dist/QQBot.js')
const realFetch=fetch,realTimer=setTimeout,realClear=clearTimeout
let now=100000000
Date.now=()=>now;Math.random=()=>0
const timers=new Map<number,()=>void>();let n=0
globalThis.setTimeout=((f:any,ms:number,...args:any[])=>{if(ms===600){const id=++n;timers.set(id,f);return id as any}if([1000,2000,4000].includes(ms)){now+=ms;return realTimer(f,0,...args)}return realTimer(f,ms,...args)})as typeof setTimeout
globalThis.clearTimeout=((id:any)=>{if(!timers.delete(id))realClear(id)})as typeof clearTimeout
globalThis.fetch=(async(url:any,init:any)=>{const logical=String(url);if(!logical.startsWith('https://bots.qq.com/')&&!logical.startsWith('https://api.sgroup.qq.com/'))throw new Error('Unowned stream API');const h=new Headers(init.headers);h.set('x-fixture-logical-url',logical);return realFetch(input.httpURL+'/api',{...init,headers:h})})as typeof fetch
const control=(action:string)=>realFetch(input.httpURL+'/'+action)
const bot=new QQBot({appId:'app',appSecret:'private-secret'})
const stream=bot.openStream({target:{scope:'c2c',targetId:'private-user',msgId:'private-inbound'},throttleMs:600})
const values=[]
if(input.mode==='concurrent-complete'||input.mode==='concurrent-cancel'){
 const first=stream.update('A');await control('await-first');await stream.update('AB')
 if(input.mode==='concurrent-complete'){const done=stream.complete();await control('release-first');await first;await done}
 else{stream.cancel();await control('release-first');await first;await stream.complete();await stream.update('ignored')}
}else if(input.mode==='throttle'||input.mode==='cancel-timer'){
 await stream.update('A');await stream.update('AB');values.push({timers:timers.size})
 if(input.mode==='cancel-timer'){stream.cancel();await stream.complete();values.push({timers:timers.size})}
 else{now+=600;const timer=timers.entries().next().value!;timers.delete(timer[0]);timer[1]();await control('await-second');await stream.complete()}
}else{try{await stream.update('A');values.push({success:true})}catch(e:any){values.push({error:e.message})};stream.cancel()}
bot.stop();globalThis.setTimeout=realTimer;globalThis.clearTimeout=realClear
process.stdout.write(JSON.stringify({values})+'\n')
