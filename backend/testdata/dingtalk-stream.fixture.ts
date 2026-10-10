import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
if (process.env.CC_HAHA_DINGTALK_PRIVATE_FIXTURE !== '1') throw new Error('private fixture only')
const input = JSON.parse(await Bun.stdin.text())
const root = new URL('../../', import.meta.url)
const { DWClient } = await import(new URL('adapters/node_modules/dingtalk-stream/dist/client.mjs',root).href)
const { TOPIC_ROBOT,TOPIC_CARD } = await import(new URL('adapters/node_modules/dingtalk-stream/dist/constants.mjs',root).href)
const { default:axios } = await import(new URL('adapters/node_modules/axios/index.js',root).href)
const { MessageDedup } = await import(new URL('adapters/common/message-dedup.ts',root).href)
const { parseDingTalkPayload } = await import(new URL('adapters/dingtalk/helpers.ts',root).href)
const adapter = axios.getAdapter('http')
if (input.base && !/^http:\/\/127\.0\.0\.1:\d+$/.test(input.base)) throw new Error('loopback only')
if(input.base) axios.defaults.adapter = config => adapter({...config,url:input.base+new URL(config.url).pathname})
console.info=()=>{};console.error=()=>{};console.warn=()=>{}
const source=readFileSync(fileURLToPath(new URL('adapters/dingtalk/index.ts',root)),'utf8')
const start=source.indexOf('  const client = new DWClient(',source.indexOf('async function start()'))
const end=source.indexOf('\n  await client.connect()',start)
if(start<0||end<0) throw new Error('Source listener boundary changed')
const code=new Bun.Transpiler({loader:'ts'}).transformSync(source.slice(start,end))
const robots=[],cards=[]
const dedup=new MessageDedup()
const client=new Function('DWClient','config','TOPIC_ROBOT','TOPIC_CARD','dedup','parseDingTalkPayload','handleRobotMessage','handleCardCallback',code+';return client')(
 DWClient,{dingtalk:input.credentials},TOPIC_ROBOT,TOPIC_CARD,dedup,parseDingTalkPayload,
 async data=>{robots.push(data)},async data=>{cards.push(data)},
)
if(input.gateway){
 const capture=async()=>{try{await client.getEndpoint();return{value:{url:client.dw_url,endpoint:client.config.endpoint}}}catch(error){return{error:error instanceof Error?error.message:error}}}
 const result=await capture();dedup.destroy();process.stdout.write(JSON.stringify(result))
}else if(input.frames){
 const wires=[];client.socket={send(w){wires.push(JSON.parse(w))}}
 for(const f of input.frames)client.onDownStream(JSON.stringify(f))
 dedup.destroy();process.stdout.write(JSON.stringify({wires,robots,cards,state:{connected:client.connected,registered:client.registered,reconnecting:client.reconnecting,isAlive:client.isAlive}}))
}else{
 const initial={heartbeat:client.heartbeat_interval,reconnect:client.reconnectInterval}
 client.heartbeat_interval=100;client.reconnectInterval=20
 const connected=[]
 const native=client._connect.bind(client)
 client._connect=async()=>{await native();client.socket.once('open',()=>connected.push(true))}
 await client.connect()
 const deadline=Date.now()+5000
 while(robots.length<(input.expectRobots??1)&&Date.now()<deadline)await new Promise(r=>setTimeout(r,1))
 if(robots.length<(input.expectRobots??1))throw new Error('private actual socket callback not observed')
 const result={initial,robots,cards,connections:connected.length}
 client.disconnect();dedup.destroy();process.stdout.write(JSON.stringify(result));process.exit(0)
}
