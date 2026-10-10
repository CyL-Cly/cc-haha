import {createRequire} from 'node:module'
import {dirname,join} from 'node:path'
const adapterRequire=createRequire(new URL('../../../../adapters/package.json',import.meta.url))
const baileysLeaf=(leaf:string)=>join(dirname(adapterRequire.resolve('@whiskeysockets/baileys')),leaf)
const signalLeaf=(leaf:string)=>join(dirname(createRequire(adapterRequire.resolve('@whiskeysockets/baileys')).resolve('libsignal/package.json')),'src',leaf)
import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME,'WhatsApp original LID store')
if(process.env.CC_HAHA_WHATSAPP_LEAF!=='1')throw new Error('Unowned LID fixture')
const input=await Bun.stdin.json()
const {LIDMappingStore}=await import(baileysLeaf('Signal/lid-mapping.js'))
const {useMultiFileAuthState}=await import(baileysLeaf('Utils/use-multi-file-auth-state.js'))
const {mkdir,readdir,readFile}=await import('node:fs/promises')
const path=await import('node:path'),dir=path.join(process.env.HOME!,'auth');await mkdir(dir,{mode:0o700})
const {state}=await useMultiFileAuthState(dir),keys={...state.keys,transaction:async(f:any)=>await f()}
const service=new LIDMappingStore(keys as any,{trace(){},debug(){},warn(){}}as any)
const values=[]
for(const action of input.actions){switch(action.op){case'store':await service.storeLIDPNMappings(action.pairs);break;case'lid':values.push(await service.getLIDForPN(action.jid));break;case'pn':values.push(await service.getPNForLID(action.jid));break}}
const files:any={};for(const name of(await readdir(dir)).sort()){if(name.startsWith('lid-mapping-'))files[name]=JSON.parse(await readFile(path.join(dir,name),'utf8'))}
process.stdout.write(JSON.stringify({values,files})+'\n')
