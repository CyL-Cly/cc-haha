import fs from 'node:fs'
import * as physical from 'node:fs/promises'
import path from 'node:path'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'
const input=JSON.parse(await Bun.stdin.text())
const home=assertDisposableFixtureHome(input.home,'WhatsApp actual promotion')
if(process.env.CC_HAHA_WHATSAPP_REGISTRATION_FIXTURE!=='1')throw new Error('private only')
const source=fs.readFileSync(new URL('../../../../src/server/api/adapters.ts',import.meta.url),'utf8')
const capabilities=await installClientFixtureCapabilities(home,{absentPaths:absentManagedSettingsFixturePaths()})
await import('../../../../preload')
const begin=source.indexOf('function getManagedWhatsAppRoot()'),end=source.indexOf('function isRecord(',begin)
if(begin<0||end<0)throw new Error('original directory consumers moved')
const code=new Bun.Transpiler({loader:'ts'}).transformSync(source.slice(begin,end)).replace(/^export /gm,'')
const {ApiError}=await import('../../../../src/server/middleware/errorHandler')
const config=path.join(home,'.claude')
const run=new Function('path','fs','crypto','ApiError','getAdapterConfigDir','WHATSAPP_STAGING_TTL_MS','whatsappLoginDirs',code+';return {managed:isManagedWhatsAppAuthDir,remove:removeManagedWhatsAppDir,promote:promoteWhatsAppAuth,stale:cleanupStaleWhatsAppLoginDirectories}')
const rows=[]
for(const row of input.rows){
 const scope=path.join(config,'whatsapp-auth');fs.mkdirSync(scope,{recursive:true,mode:0o700})
 const stage=path.join(scope,'.login-'+row.id),target=path.join(scope,'target-'+row.id),external=path.join(home,'external-'+row.id)
 fs.mkdirSync(external,{recursive:true});fs.writeFileSync(path.join(external,'protected'),'KEEP')
 if(row.stage!=='missing'){fs.mkdirSync(stage,{recursive:true});fs.writeFileSync(path.join(stage,'new'),'NEW')}
 if(row.target==='dir'){fs.mkdirSync(target);fs.writeFileSync(path.join(target,'old'),'OLD')}
 if(row.target==='symlink')fs.symlinkSync(external,target)
 const actualFs={...physical}
 if(row.fail==='rename')actualFs.rename=async(from,to)=>{if(from===stage)throw new Error('fixture rename failure');return physical.rename(from,to)}
 if(row.fail==='remove-backup')actualFs.rm=async(file,opts)=>{if(String(file).includes('.backup-'))throw new Error('fixture backup delete failure');return physical.rm(file,opts)}
 const api=run(path,actualFs,{randomUUID:()=>row.id},ApiError,()=>config,180000,new Map())
 if(row.readonly)fs.chmodSync(scope,0o500)
 let error=null
 try{await api.promote(row.outside?external:stage,target)}catch(e){error=row.readonly ? {errno:e.code} : {status:e.statusCode,message:e.message}}
 if(row.readonly)fs.chmodSync(scope,0o700)
 const read=(dir,name)=>{try{return fs.readFileSync(path.join(dir,name),'utf8')}catch{return null}}
 rows.push({error,new:read(target,'new'),old:read(target,'old'),protected:read(external,'protected'),stagingExists:fs.existsSync(stage),targetSymlink:(()=>{try{return fs.lstatSync(target).isSymbolicLink()}catch{return false}})()})
}
process.stdout.write(JSON.stringify({rows,capabilities,consumer:'verbatim original adapters.ts directory helpers, actual filesystem + only indicated syscall failure'}))
