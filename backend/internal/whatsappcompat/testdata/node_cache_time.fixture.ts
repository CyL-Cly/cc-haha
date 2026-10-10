import {createRequire} from 'node:module'
import {dirname,join} from 'node:path'
const adapterRequire=createRequire(new URL('../../../../adapters/package.json',import.meta.url))
const baileysLeaf=(leaf:string)=>join(dirname(adapterRequire.resolve('@whiskeysockets/baileys')),leaf)
const signalLeaf=(leaf:string)=>join(dirname(createRequire(adapterRequire.resolve('@whiskeysockets/baileys')).resolve('libsignal/package.json')),'src',leaf)
import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME,'WhatsApp original cache lifetime')
if(process.env.CC_HAHA_WHATSAPP_LEAF!=='1')throw new Error('Unowned original cache lifetime')
const {makeCacheableSignalKeyStore}=await import(baileysLeaf('Utils/auth-utils.js'))
let now=1000000;Date.now=()=>now
let reads=0;let fail=false;let stored:any={value:1}
const cache=makeCacheableSignalKeyStore({async get(type:string,ids:string[]){reads++;return{key:stored}},async set(values:any){if(fail)throw new Error('private physical write failure');stored=values.session.key}} as any)
const snapshots:any[]=[]
const capture=async()=>snapshots.push(JSON.parse(JSON.stringify(await cache.get('session',['key']))))
await capture();stored={value:2};now+=299999;await capture();now+=2;await capture()
fail=true;let error='';try{await cache.set({session:{key:{value:3}}})}catch(e:any){error=e.message};await capture()
process.stdout.write(JSON.stringify({snapshots,reads,error})+'\n')
