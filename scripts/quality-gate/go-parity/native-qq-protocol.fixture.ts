import { createRequire } from 'node:module'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
const input=await Bun.stdin.json()
assertDisposableFixtureHome(process.env.HOME,'QQ protocol')
if(process.env.CC_HAHA_NATIVE_QQ_PROTOCOL_FIXTURE!=='1')throw new Error('Unowned QQ protocol fixture')
const vendor=await import(join(dirname(createRequire(new URL('../../../adapters/package.json', import.meta.url)).resolve('@tencent-connect/qqbot-connector')), '../esm/qqbot-session.js'))
const observe=(run:()=>unknown)=>{try{return {value:run()}}catch(error:any){return {error:error.message,name:error.name,code:error.code}}}
process.stdout.write(JSON.stringify({urls:input.urls.map((row:any)=>observe(()=>vendor.buildConnectUrl(row.task,row.source))),decrypt:input.decrypt.map((row:any)=>observe(()=>vendor.decryptSecret(row.encrypted,row.key)))})+'\n')
