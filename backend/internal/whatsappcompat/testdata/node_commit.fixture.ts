import {createRequire} from 'node:module'
import {dirname,join} from 'node:path'
const adapterRequire=createRequire(new URL('../../../../adapters/package.json',import.meta.url))
const baileysLeaf=(leaf:string)=>join(dirname(adapterRequire.resolve('@whiskeysockets/baileys')),leaf)
const signalLeaf=(leaf:string)=>join(dirname(createRequire(adapterRequire.resolve('@whiskeysockets/baileys')).resolve('libsignal/package.json')),'src',leaf)
import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME,'WhatsApp original commit failure')
if(process.env.CC_HAHA_WHATSAPP_LEAF!=='1')throw new Error('Unowned original commit fixture')
const {addTransactionCapability}=await import(baileysLeaf('Utils/auth-utils.js'))
const input=await Bun.stdin.json();const out=[]
for(const failures of input){let attempts=0;let error='';const writes=[];const store=addTransactionCapability({async get(){return{}},async set(v:any){attempts++;writes.push(v);if(attempts<=failures)throw new Error('private commit failure')}}as any,{trace(){},debug(){},info(){},warn(){},error(){}}as any,{maxCommitRetries:10,delayBetweenTriesMs:0});try{await store.transaction(async()=>store.set({session:{'111.0':{value:1}}}),'peer')}catch(e:any){error=e.message};out.push({attempts,error,writes})}
process.stdout.write(JSON.stringify(out)+'\n')
