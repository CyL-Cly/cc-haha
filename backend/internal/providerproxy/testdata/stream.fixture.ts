import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.home,'Responses stream transformer')
if(process.env.CC_HAHA_RESPONSES_FIXTURE!=='1')throw new Error('Unowned Responses stream fixture')
Date.now=()=>1720000000123
const {openaiResponsesStreamToAnthropic}=await import('../../../../src/server/proxy/streaming/openaiResponsesStreamToAnthropic')
const {openaiResponsesStreamToAnthropicResponse}=await import('../../../../src/server/proxy/streaming/openaiResponsesStreamToAnthropicResponse')
const rows=[]
for(const row of input.rows){
 const make=(cancels:any[])=>new ReadableStream<Uint8Array>({start(controller){const bytes=row.bytes?Uint8Array.from(row.bytes):new TextEncoder().encode(row.input);const size=row.chunkSize||7;for(let start=0;start<bytes.length;start+=size)controller.enqueue(bytes.slice(start,start+size));controller.close()},cancel(reason){cancels.push(reason)}})
 const frames:string[]=[],terminals:string[]=[],cancels:any[]=[],signals:any[]=[];let settled=0,error:any=null
 const reader=openaiResponsesStreamToAnthropic(make(cancels),'fixture-model',{openAICodexOAuth:row.oauth,onTerminal:event=>terminals.push(event),onCancel:reason=>signals.push(reason),onSettled:()=>settled++}).getReader()
 try {while(true){const next=await reader.read();if(next.done)break;frames.push(new TextDecoder().decode(next.value))}}
 catch(e:any){error={name:e.name,message:e.message,...(e.type!==undefined?{type:e.type}:{}),...(e.code!==undefined?{code:e.code}:{}),...(e.status!==undefined?{status:e.status}:{})}}
 await new Promise(resolve=>setTimeout(resolve,0))
 let collected:any=null,collectError:any=null
 try{collected=await openaiResponsesStreamToAnthropicResponse(make([]),'fixture-model',{openAICodexOAuth:row.oauth})}catch(e:any){collectError={name:e.name,message:e.message,...(e.type!==undefined?{type:e.type}:{}),...(e.code!==undefined?{code:e.code}:{}),...(e.status!==undefined?{status:e.status}:{})}}
 rows.push({frames,terminals,cancels,signals,settled,error,collected,collectError})
}
process.stdout.write(JSON.stringify({rows})+'\n')
