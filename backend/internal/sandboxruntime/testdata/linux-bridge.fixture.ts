import fs from 'node:fs'
import {once} from 'node:events'
import {initializeLinuxNetworkBridge} from '@anthropic-ai/sandbox-runtime/dist/sandbox/linux-sandbox-utils.js'
const input=JSON.parse(await Bun.stdin.text())
const bridge=await initializeLinuxNetworkBridge(input.port,input.port)
fs.writeFileSync(input.ready,JSON.stringify({http:bridge.httpSocketPath,socks:bridge.socksSocketPath,pids:[bridge.httpBridgeProcess.pid,bridge.socksBridgeProcess.pid],args:[bridge.httpBridgeProcess.spawnargs.slice(1),bridge.socksBridgeProcess.spawnargs.slice(1)]}))
while(!fs.existsSync(input.release))await Bun.sleep(1)
const processes=[bridge.httpBridgeProcess,bridge.socksBridgeProcess]
const joined=processes.map(p=>once(p,'exit'))
for(const p of processes)p.kill('SIGTERM')
await Promise.all(joined)
for(const path of [bridge.httpSocketPath,bridge.socksSocketPath])try{fs.unlinkSync(path)}catch{}
process.stdout.write(JSON.stringify({joined:true})+'\n')
