import {createRequire} from 'node:module'
import {dirname,join} from 'node:path'
const adapterRequire=createRequire(new URL('../../adapters/package.json',import.meta.url))
const baileysLeaf=(leaf:string)=>join(dirname(adapterRequire.resolve('@whiskeysockets/baileys')),leaf)
import * as fs from 'node:fs'
import * as path from 'node:path'
import {readFileSync} from 'node:fs'
import {assertDisposableFixtureHome} from '../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME,'WhatsApp original auth files')
if(process.env.CC_HAHA_WHATSAPP_LEAF!=='1')throw new Error('Unowned WhatsApp auth fixture')
const input=await Bun.stdin.json()
const {useMultiFileAuthState}=await import(baileysLeaf('Utils/use-multi-file-auth-state.js'))
const {BufferJSON}=await import(baileysLeaf('Utils/generics.js'))
const source=readFileSync(new URL('../../adapters/whatsapp/session.ts',import.meta.url),'utf8')
const start=source.indexOf('function resolveCredsPath'),end=source.indexOf('function makeBaileysLogger',start)
const transpiled=new Bun.Transpiler({loader:'ts'}).transformSync(source.slice(start,end))
const queues=new Map<string,Promise<void>>()
const local=new Function('fs','path','CREDS_FILE','CREDS_BACKUP_FILE','credsSaveQueues',transpiled+';return {maybeRestoreCredsFromBackup,enqueueSaveCreds,isValidJsonFile}') (fs,path,'creds.json','creds.json.bak',queues)
const dir=path.join(process.env.HOME!,'auth');fs.mkdirSync(dir,{recursive:true,mode:0o700})
for(const [file,data]of Object.entries(input.files??{}))fs.writeFileSync(path.join(dir,file),data as string,{mode:0o600})
local.maybeRestoreCredsFromBackup(dir)
const {state,saveCreds}=await useMultiFileAuthState(dir)
const values=[]
for(const action of input.actions){switch(action.op){case'get':try{values.push({value:await state.keys.get(action.category,action.ids)})}catch(e:any){values.push({error:e.message})}break;case'set':await state.keys.set(JSON.parse(JSON.stringify(action.value),BufferJSON.reviver));break;case'update':Object.assign(state.creds,JSON.parse(JSON.stringify(action.value),BufferJSON.reviver));break;case'save':local.enqueueSaveCreds(dir,saveCreds);await(queues.get(path.resolve(dir))??Promise.resolve());break;case'buffer':values.push({value:JSON.parse(JSON.stringify(action.value),BufferJSON.reviver)});break;default:throw new Error('Unknown auth operation')}}
const files:any={};for(const file of fs.readdirSync(dir).sort()){const p=path.join(dir,file);files[file]={data:fs.readFileSync(p,'utf8'),mode:fs.statSync(p).mode&0o777}}
process.stdout.write(JSON.stringify({creds:state.creds,values,files},BufferJSON.replacer)+'\n')
