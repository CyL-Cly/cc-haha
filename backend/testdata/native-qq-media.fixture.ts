import { readFile,stat } from 'node:fs/promises'
import { relative,join } from 'node:path'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME,'QQ actual media')
if(process.env.CC_HAHA_QQ_MESSAGE_LEAF!=='1')throw new Error('Unowned QQ media fixture')
const input=await Bun.stdin.json()
const {QqMediaService}=await import('../../adapters/qq/media.ts')
const {AttachmentStore}=await import('../../adapters/common/attachment/attachment-store.ts')
let row:any,calls:any[]=[]
const peer=Bun.serve({hostname:'127.0.0.1',port:0,fetch(req){calls.push({method:req.method,url:req.headers.get('x-fixture-logical-url')});return new Response(Buffer.from(row.data,'base64'),{status:row.status??200,headers:{'content-type':row.mime??'application/octet-stream'}})}})
const root=join(process.env.HOME!,'private-downloads')
const media=new QqMediaService(new AttachmentStore({root}),async(url:any,init:any)=>{const logical=String(url);if(!logical.startsWith('https://cdn.qq.private/'))throw new Error('Unowned QQ media logical URL');return fetch(`http://127.0.0.1:${peer.port}/media`,{...init,headers:{'x-fixture-logical-url':logical}})})
const output=[]
for(const [i,value]of input.rows.entries()){row=value;calls=[];try{const result=await media.downloadAttachment(row.attachment,'private-session',String(i));output.push({kind:result.kind,name:result.name,path:relative(root,result.path),mime:result.mimeType,size:result.size,data:(await readFile(result.path)).toString('base64'),mode:(await stat(result.path)).mode&0o777,calls})}catch(e:any){output.push({error:e.message,calls})}}
await peer.stop(true)
process.stdout.write(JSON.stringify(output)+'\n')
