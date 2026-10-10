import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME,'QQ actual vendor gateway')
if(process.env.CC_HAHA_QQ_MESSAGE_LEAF!=='1')throw new Error('Unowned QQ gateway fixture')
const input=await Bun.stdin.json()
const {QQBot}=await import('../../adapters/node_modules/@tencent-connect/qqbot-nodejs/dist/QQBot.js')
const {extractQqPayload}=await import('../../adapters/qq/extract-payload.ts')
const originalFetch=fetch, originalTimeout=setTimeout, originalClear=clearTimeout, originalInterval=setInterval, originalClearInterval=clearInterval
const originalNow=Date.now,originalRandom=Math.random
const timers=new Map<number,{ms:number,f:()=>void}>(),intervals=new Map<number,{ms:number,f:()=>void}>()
let next=0
globalThis.setTimeout=((f:()=>void,ms:number,...args:any[])=>{const code=String(f);if(!code.includes('reconnectTimer')&&!code.includes('controller.abort')&&!code.includes('removeEventListener'))return originalTimeout(f,ms,...args);const id=++next;timers.set(id,{ms,f});notify();return id as any}) as typeof setTimeout
globalThis.clearTimeout=((id:any)=>{if(!timers.delete(id))originalClear(id)}) as typeof clearTimeout
globalThis.setInterval=((f:()=>void,ms:number,...args:any[])=>{if(ms!==1234)return originalInterval(f,ms,...args);const id=++next;intervals.set(id,{ms,f});notify();return id as any}) as typeof setInterval
globalThis.clearInterval=((id:any)=>{if(!intervals.delete(id))originalClearInterval(id)}) as typeof clearInterval
Date.now=()=>100000000
Math.random=()=>0
let changed: (()=>void)[]=[]
const notify=()=>{for(const f of changed.splice(0))f()}
async function until(predicate:()=>boolean){let timeout:any;await Promise.race([(async()=>{while(!predicate())await new Promise<void>(r=>changed.push(r))})(),new Promise<never>((_,r)=>{timeout=originalTimeout(()=>r(new Error(`QQ actual gateway barrier timeout opens=${opens} events=${events.length} timers=${JSON.stringify([...timers.values()].map(v=>v.ms))}`)),6000)})]).finally(()=>originalClear(timeout))}
const events:any[]=[],calls:any[]=[]
let opens=0
// The peer sockets/HTTP are hosted by the owning Go test, so this fixture
// does not ask Bun's server.stop to join its upgraded socket bookkeeping.
globalThis.fetch=(async(url:any,init:any)=>{const logical=String(url);if(logical!=='https://bots.qq.com/app/getAppAccessToken'&&logical!=='https://api.sgroup.qq.com/gateway')throw new Error('Unowned QQ gateway HTTP');const h=new Headers(init.headers);h.set('x-fixture-logical-url',logical);return originalFetch(input.httpURL+'/api',{...init,headers:h})}) as typeof fetch
const bot=new QQBot({appId:' app ',appSecret:'private-secret',accountId:' app ',transport:'websocket',tokenPrefetch:'sync',logger:{debug(){},info(m){if(m.includes('WebSocket connected')){process.stderr.write('QQ client open\n');opens++;void originalFetch(input.httpURL+'/release-hello?connection='+opens).then(()=>notify())}},warn(m){process.stderr.write('QQ warn '+m+'\n')},error(m){process.stderr.write('QQ error '+m+'\n')}}})
bot.on('ready',d=>{events.push({ready:d});notify()})
bot.on('resumed',d=>{events.push({resumed:d});notify()})
bot.on('message',(_ctx,m)=>{const p=extractQqPayload(m);if(p){events.push({message:p});notify()}})
const controller=new AbortController();const run=bot.start(controller.signal)
await until(()=>events.some(e=>e.message))
process.stderr.write('QQ first message\n')
function heartbeat(){const interval=[...intervals.values()].find(v=>v.ms===1234);if(!interval)throw new Error('Original heartbeat absent');interval.f()}
async function reconnect(ms:number){await until(()=>[...timers.values()].some(v=>v.ms===ms));const entry=[...timers.entries()].find(([,v])=>v.ms===ms)!;timers.delete(entry[0]);entry[1].f()}
heartbeat();await reconnect(1000);await until(()=>events.some(e=>e.resumed));heartbeat();await reconnect(3000);await until(()=>events.filter(e=>e.ready).length===2)
process.stderr.write('QQ third ready\n')
controller.abort();await run
process.stderr.write('QQ run joined\n')
globalThis.setTimeout=originalTimeout;globalThis.clearTimeout=originalClear;globalThis.setInterval=originalInterval;globalThis.clearInterval=originalClearInterval
Date.now=originalNow;Math.random=originalRandom
process.stdout.write(JSON.stringify({events})+'\n')
