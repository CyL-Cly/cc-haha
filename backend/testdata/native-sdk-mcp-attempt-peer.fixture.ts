import fs from 'node:fs'
import {Server} from '../../node_modules/@modelcontextprotocol/sdk/dist/esm/server/index.js'
import {ListToolsRequestSchema,CallToolRequestSchema} from '../../node_modules/@modelcontextprotocol/sdk/dist/esm/types.js'
import {SdkControlServerTransport} from '../../src/services/mcp/SdkControlTransport.ts'
const name=process.argv[2]
const transport=new SdkControlServerTransport(m=>process.stdout.write(JSON.stringify(m)+'\n'))
let releaseHeld:any;const held=new Promise<void>(resolve=>releaseHeld=resolve)
const server=new Server({name:'physical-'+name,version:'1'},{capabilities:{tools:{}}})
server.setRequestHandler(ListToolsRequestSchema,async()=>({tools:[{name:'physical',inputSchema:{type:'object',properties:{text:{type:'string'}},required:['text']}}]}))
server.setRequestHandler(CallToolRequestSchema,async(r:any)=>{if(r.params.arguments.text==='HELD_PHYSICAL_WRITE'){process.stderr.write('HELD\n');await held}
fs.writeFileSync(process.env.HOME+'/'+name+'.txt',r.params.arguments.text,{mode:0o600});return {content:[{type:'text',text:fs.readFileSync(process.env.HOME+'/'+name+'.txt','utf8')}]}})
await server.connect(transport)
process.stderr.write('READY\n')
let buffer=''
for await(const chunk of Bun.stdin.stream()){buffer+=new TextDecoder().decode(chunk);while(buffer.includes('\n')){let i=buffer.indexOf('\n');const m=JSON.parse(buffer.slice(0,i));buffer=buffer.slice(i+1);if(m.fixtureRelease){releaseHeld();continue}transport.onmessage?.(m);if(m.id===undefined)process.stdout.write(JSON.stringify({jsonrpc:'2.0',method:'notifications/progress',params:{progressToken:'unused',progress:0}})+'\n')}}
await server.close();process.stderr.write('CLOSED\n')
