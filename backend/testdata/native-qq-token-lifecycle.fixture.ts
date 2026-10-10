import {assertDisposableFixtureHome}from'../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME,'QQ token lifecycle')
if(process.env.CC_HAHA_QQ_MESSAGE_LEAF!=='1')throw new Error('Unowned QQ token lifecycle')
const {TokenManager}=await import('../../adapters/node_modules/@tencent-connect/qqbot-nodejs/dist/protocol/api/token.js')
const realTimeout=setTimeout,realClear=clearTimeout,realFetch=fetch
let now=100000000,next=0,calls=0,release:(()=>void)|undefined
const timers=new Map<number,{ms:number,f:()=>void}>();let watchers:(()=>void)[]=[]
const changed=()=>{for(const f of watchers.splice(0))f()}
globalThis.setTimeout=((f:()=>void,ms:number,...args:any[])=>{if(![10000,60000,5000,6900000].includes(ms))return realTimeout(f,ms,...args);const id=++next;timers.set(id,{ms,f});changed();return id as any})as typeof setTimeout
globalThis.clearTimeout=((id:any)=>{if(!timers.delete(id))realClear(id)})as typeof clearTimeout
Date.now=()=>now;Math.random=()=>0
async function until(p:()=>boolean){let timer:any;await Promise.race([(async()=>{while(!p())await new Promise<void>(r=>watchers.push(r))})(),new Promise<never>((_,r)=>{timer=realTimeout(()=>r(new Error('QQ token barrier')),4000)})]).finally(()=>realClear(timer))}
const peer=Bun.serve({hostname:'127.0.0.1',port:0,async fetch(){calls++;changed();if(calls===1)await new Promise<void>(r=>{release=r;changed()});return Response.json({access_token:`private-token-${calls}`,expires_in:7200})}})
globalThis.fetch=(async(url:any,init:any)=>{if(String(url)!=='https://bots.qq.com/app/getAppAccessToken')throw new Error('Unowned token URL');return realFetch(`http://127.0.0.1:${peer.port}/token`,init)})as typeof fetch
const manager=new TokenManager({userAgent:'qqbot-nodejs/0.1.0 (Node/24.3.0)'})
const pending=Array.from({length:12},()=>manager.getAccessToken('app','private-secret'))
await until(()=>release!==undefined);const pendingCalls=calls;release!();const values=await Promise.all(pending)
manager.startBackgroundRefresh('app','private-secret');await until(()=>[...timers.values()].some(t=>t.ms===6900000));const first=[...timers.values()].map(t=>t.ms)
const entry=[...timers.entries()].find(([,t])=>t.ms===6900000)!;now+=6900000;timers.delete(entry[0]);entry[1].f();await until(()=>[...timers.values()].some(t=>t.ms===60000));const beforeExpiry=calls
manager.stopBackgroundRefresh('app');await until(()=>!manager.isBackgroundRefreshRunning('app'))
now=107200000;await manager.getAccessToken('app','private-secret');const afterExpiry=calls
manager.startBackgroundRefresh(' app ','private-secret');await until(()=>[...timers.values()].some(t=>t.ms===60000));const untrimmed=[...timers.values()].map(t=>t.ms);manager.stopBackgroundRefresh()
globalThis.setTimeout=realTimeout;globalThis.clearTimeout=realClear
await peer.stop(true)
process.stdout.write(JSON.stringify({pendingCalls,values,first,beforeExpiry,afterExpiry,untrimmed})+'\n')
