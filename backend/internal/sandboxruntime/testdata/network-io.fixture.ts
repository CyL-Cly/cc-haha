import fs from 'node:fs'
import {createInterface} from 'node:readline'
import {SandboxManager} from '@anthropic-ai/sandbox-runtime'
const input=JSON.parse(await new Promise<string>(resolve=>{const r=createInterface({input:process.stdin});r.once('line',line=>{r.close();resolve(line)})}))
if(Bun.version!=='1.3.14')throw new Error('pinned native Source required')
const asked:any[]=[]
await SandboxManager.initialize({network:input.config,filesystem:{allowWrite:[],denyWrite:[],denyRead:[]},ripgrep:{command:'/usr/bin/true'}},async value=>{asked.push(value);return input.ask===true})
const [http,socks]=[SandboxManager.getProxyPort(),SandboxManager.getSocksProxyPort()]
fs.writeFileSync(input.ready,JSON.stringify({http,socks}))
while(!fs.existsSync(input.release))await new Promise(resolve=>setTimeout(resolve,5))
if(input.update)SandboxManager.updateConfig({network:input.update,filesystem:{allowWrite:[],denyWrite:[],denyRead:[]},ripgrep:{command:'/usr/bin/true'}})
if(input.updated){fs.writeFileSync(input.updated,'ready');while(!fs.existsSync(input.finish))await new Promise(resolve=>setTimeout(resolve,5))}
await SandboxManager.reset()
process.stdout.write(JSON.stringify({asked})+'\n')
