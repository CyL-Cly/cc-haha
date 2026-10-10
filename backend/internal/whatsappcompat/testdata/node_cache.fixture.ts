import {createRequire} from 'node:module'
import {dirname,join} from 'node:path'
const adapterRequire=createRequire(new URL('../../../../adapters/package.json',import.meta.url))
const baileysLeaf=(leaf:string)=>join(dirname(adapterRequire.resolve('@whiskeysockets/baileys')),leaf)
const signalLeaf=(leaf:string)=>join(dirname(createRequire(adapterRequire.resolve('@whiskeysockets/baileys')).resolve('libsignal/package.json')),'src',leaf)
import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME,'WhatsApp original key cache')
if(process.env.CC_HAHA_WHATSAPP_LEAF!=='1')throw new Error('Unowned key cache fixture')
const {makeCacheableSignalKeyStore}=await import(baileysLeaf('Utils/auth-utils.js'))
const reads:any[]=[],writes:any[]=[],data:any={session:{key:{value:1},missing:null,false:false,zero:0,empty:''}}
const source={get:async(type:string,ids:string[])=>{reads.push({type,ids});const out:any={};for(const id of ids)out[id]=data[type]?.[id]??null;return out},set:async(values:any)=>{writes.push(values);for(const type of Object.keys(values))Object.assign(data[type]??={},values[type])}}
const cache=makeCacheableSignalKeyStore(source as any)
const snapshots:any[]=[]
const capture=(v:any)=>snapshots.push(JSON.parse(JSON.stringify(v)))
const first=await cache.get('session',['key','missing','false','zero','empty']);capture(first);first.key.value=2;capture(await cache.get('session',['key','missing']));await cache.set({session:{key:{value:3},missing:null}});capture(await cache.get('session',['key','missing']));await cache.clear();capture(await cache.get('session',['key','missing']))
process.stdout.write(JSON.stringify({snapshots,reads,writes})+'\n')
