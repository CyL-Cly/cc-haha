import {readFileSync} from 'node:fs'
import * as fs from 'node:fs'
import * as path from 'node:path'
import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME,'WhatsApp original queued save failure')
if(process.env.CC_HAHA_WHATSAPP_LEAF!=='1')throw new Error('Unowned original queued save')
const text=readFileSync(new URL('../../../../adapters/whatsapp/session.ts',import.meta.url),'utf8')
const start=text.indexOf('function enqueueSaveCreds'),end=text.indexOf('function makeBaileysLogger',start)
if(start<0||end<0)throw new Error('Original save queue missing')
const code=new Bun.Transpiler({loader:'ts'}).transformSync(text.slice(start,end))
const q=new Map();const warnings:string[]=[];const fn=new Function('fs','path','credsSaveQueues','CREDS_FILE','CREDS_BACKUP_FILE','resolveCredsPath','resolveCredsBackupPath','console',code+';return enqueueSaveCreds')
const run=fn(fs,path,q,'creds.json','creds.json.bak',(dir:string)=>path.join(dir,'creds.json'),(dir:string)=>path.join(dir,'creds.json.bak'),{warn(_prefix:string,error:any){warnings.push(typeof error)}})
const dir=path.join(process.env.HOME!,'auth');fs.mkdirSync(path.join(dir,'creds.json'),{recursive:true})
let returned=false;run(dir,()=>fs.writeFileSync(path.join(dir,'creds.json'),'{}'));returned=true
await q.get(path.resolve(dir))
process.stdout.write(JSON.stringify({returned,warnings:warnings.length,queueCleared:!q.has(path.resolve(dir)),fileStillDirectory:fs.statSync(path.join(dir,'creds.json')).isDirectory()})+'\n')
