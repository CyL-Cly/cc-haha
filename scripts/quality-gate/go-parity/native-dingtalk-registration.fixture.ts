import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'DingTalk registration next implementation')
if (process.env.CC_HAHA_DINGTALK_REGISTRATION_FIXTURE !== '1') throw new Error('Unowned registration fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
let row: any, index = 0
let requests: any[] = []
let controller: AbortController
const upstream = Bun.serve({hostname: '127.0.0.1', port: 0, async fetch(req) {
  const rawBody = await req.text()
  requests.push({path:new URL(req.url).pathname, method:req.method, body:JSON.parse(rawBody), ...(row.recordRaw ? {rawBody} : {})})
  if (row.abortDuringRequest) {
    controller.abort()
    await Bun.sleep(5)
  }
  const response = row.replies[index++] ?? {status:500,body:{errcode:77,errmsg:'unexpected request'}}
  return new Response(response.raw ?? JSON.stringify(response.body), {status:response.status??200,headers:{'content-type':'application/json'}})
}})
process.env.DINGTALK_REGISTRATION_BASE_URL = `http://127.0.0.1:${upstream.port}`
process.env.DINGTALK_REGISTRATION_SOURCE = ' PRIVATE_NEXT_SOURCE '
const { handleAdaptersApi } = await import('../../../src/server/api/adapters')
const config = process.env.CLAUDE_CONFIG_DIR!
await mkdir(config,{recursive:true})
const file = join(config,'adapters.json')
const output = []
for (row of input.rows) {
  index=0;requests=[]
  controller=new AbortController()
  await writeFile(file,JSON.stringify(row.seed),{mode:0o600})
  const url=new URL(`http://private.test/api/adapters/dingtalk/registration/${row.action}`)
  let response
  try {
    response=await handleAdaptersApi(new Request(url,{method:'POST',body:row.rawInput??JSON.stringify(row.input??{}),signal:controller.signal}),url,['api','adapters','dingtalk','registration',row.action])
  } catch {
    // Original server/index.ts outer API boundary handles async poll rejection.
    response=Response.json({error:'Internal server error'},{status:500})
  }
  const body=await response.json()
  const qr=typeof body.qrDataUrl==='string'?{prefix:body.qrDataUrl.startsWith('data:image/png;base64,'),bytes:Buffer.from(body.qrDataUrl.split(',')[1],'base64').length}:undefined
  delete body.qrDataUrl
  output.push({status:response.status,body,requests,raw:JSON.parse(await readFile(file,'utf8')),qr,...(row.abortDuringRequest?{aborted:controller.signal.aborted}:{})})
}
await upstream.stop(true)
process.stdout.write(JSON.stringify(output))
