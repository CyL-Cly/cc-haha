import {resolveParentProxy,shouldBypassParentProxy,selectParentProxyUrl,proxyAuthHeader} from '@anthropic-ai/sandbox-runtime/dist/sandbox/parent-proxy.js'
const input=JSON.parse(await Bun.stdin.text())
if(Bun.version!=='1.3.14')throw new Error('pinned parent proxy Source required')
const keys=['HTTP_PROXY','http_proxy','HTTPS_PROXY','https_proxy','NO_PROXY','no_proxy']
const out=[]
for(const row of input.rows){
 for(const key of keys)delete process.env[key]
 Object.assign(process.env,row.environment)
 const resolved=resolveParentProxy(undefined)
 out.push({http:resolved?.httpUrl?.href??null,https:resolved?.httpsUrl?.href??null,
  calls:row.hosts.map((host:string)=>({host,bypass:resolved?shouldBypassParentProxy(resolved,host):null,
   http:resolved?selectParentProxyUrl(resolved,{isHttps:false})?.href??null:null,
   https:resolved?selectParentProxyUrl(resolved,{isHttps:true})?.href??null:null})),
  httpAuth:resolved?.httpUrl?proxyAuthHeader(resolved.httpUrl)??null:null,
  httpsAuth:resolved?.httpsUrl?proxyAuthHeader(resolved.httpsUrl)??null:null})
}
process.stdout.write(JSON.stringify(out)+'\n')
