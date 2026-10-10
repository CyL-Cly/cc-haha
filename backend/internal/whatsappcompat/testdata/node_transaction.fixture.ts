import {createRequire} from 'node:module'
import {dirname,join} from 'node:path'
const adapterRequire=createRequire(new URL('../../../../adapters/package.json',import.meta.url))
const baileysLeaf=(leaf:string)=>join(dirname(adapterRequire.resolve('@whiskeysockets/baileys')),leaf)
const signalLeaf=(leaf:string)=>join(dirname(createRequire(adapterRequire.resolve('@whiskeysockets/baileys')).resolve('libsignal/package.json')),'src',leaf)
import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME,'WhatsApp original transaction')
if(process.env.CC_HAHA_WHATSAPP_LEAF!=='1')throw new Error('Unowned transaction fixture')
const {addTransactionCapability}=await import(baileysLeaf('Utils/auth-utils.js'))
const events:string[]=[],writes:any[]=[]
const native={get:async()=>({}),set:async(data:any)=>{writes.push(data)}}
const store=addTransactionCapability(native as any,{trace(){},debug(){},info(){},warn(){},error(){}}as any,{maxCommitRetries:1,delayBetweenTriesMs:0})
let release:any,entered:any;const wait=new Promise<void>(r=>{release=r}),start=new Promise<void>(r=>{entered=r})
const a=store.transaction(async()=>{events.push('A1 begin');entered();await wait;await store.set({session:{A1:{value:1}}});events.push('A1 end')},'A')
await start
const a2=store.transaction(async()=>{events.push('A2 begin');await store.set({session:{A2:{value:2}}});events.push('A2 end')},'A')
await store.transaction(async()=>{events.push('B begin');await store.set({session:{B:{value:3}}});events.push('B end')},'B')
release();await a;await a2
process.stdout.write(JSON.stringify({events,writes})+'\n')
