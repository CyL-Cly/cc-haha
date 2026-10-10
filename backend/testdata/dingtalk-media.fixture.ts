import { readFileSync } from 'node:fs'
import { relative } from 'node:path'
if (process.env.CC_HAHA_DINGTALK_PRIVATE_FIXTURE !== '1') throw new Error('private fixture only')
const root=new URL('../../',import.meta.url)
const input=JSON.parse(await Bun.stdin.text())
if(!/^http:\/\/127\.0\.0\.1:\d+$/.test(input.base))throw new Error('loopback only')
Date.now=()=>1770000000123;Math.random=()=>0.25
const nativeFetch=globalThis.fetch
globalThis.fetch=(url,opts)=>nativeFetch(input.base+new URL(String(url)).pathname,opts)
const {AttachmentStore}=await import(new URL('adapters/common/attachment/attachment-store.ts',root).href)
const {DingTalkMediaService}=await import(new URL('adapters/dingtalk/media.ts',root).href)
const service=new DingTalkMediaService(new AttachmentStore({root:input.store}))
const values=[]
for(const candidate of input.candidates){
 try{const a=await service.downloadCandidate(candidate,'session',input.options);values.push({value:{kind:a.kind,name:a.name,path:relative(input.store,a.path),mime:a.mimeType,size:a.size,bytes:a.buffer.toString('base64'),disk:readFileSync(a.path).toString('base64')}})}
 catch(error){values.push({error:error instanceof Error?error.message:error})}
}
process.stdout.write(JSON.stringify({values}))
