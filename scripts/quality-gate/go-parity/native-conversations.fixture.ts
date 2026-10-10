import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths,installClientFixtureCapabilities } from './client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir,'Conversations REST')
if(process.env.CC_HAHA_CONVERSATIONS_FIXTURE!=='1')throw new Error('Missing conversations fixture')
const capabilities=await installClientFixtureCapabilities(input.homeDir,{absentPaths:absentManagedSettingsFixturePaths()})
await import('../../../preload')
spyOn(console,'log').mockImplementation(()=>{})
const sessionService=await import('../../../src/server/services/sessionService')
spyOn(sessionService.sessionService,'getSessionSummary').mockImplementation(async(id:string)=>id==='existing'?{id}:null)
let waiting=false
const conversation=await import('../../../src/server/services/conversationService')
spyOn(conversation.conversationService,'getPendingPermissionRequests').mockImplementation(()=>waiting?[{requestId:'actual-fixture-permission'}]:[])
const ws=await import('../../../src/server/ws/handler')
const source=await import('../../../src/server/api/conversations')
const rows=[]
let wasRunning=false
spyOn(crypto,'randomUUID').mockImplementation(()=> '11111111-1111-4111-8111-111111111111')
for(const row of input.cases){
 waiting=row.waiting??false
 const id=row.path.split('/')[3]
 if(row.running)ws.__markActiveTurnForTests(id)
 else if(wasRunning)ws.__settleActiveTurnForTests(id,{type:'result',is_error:false})
 wasRunning=row.running??false
 if(row.interrupted)await ws.handleWebSocket.message({data:{sessionId:id,channel:'client',clientKind:'full',serverHost:'127.0.0.1',serverPort:1},send:()=>{},close:()=>{}} as any,JSON.stringify({type:'stop_generation'}))
 const response=await source.handleConversationsApi(new Request('http://localhost'+row.path,{method:row.method,...(row.method==='POST'?{body:row.body??''}:{})}),new URL('http://localhost'+row.path),row.path.split('/').filter(Boolean))
 rows.push({status:response.status,body:await response.json()})
}
process.stdout.write(JSON.stringify({rows,capabilities}))
process.exit(0)
