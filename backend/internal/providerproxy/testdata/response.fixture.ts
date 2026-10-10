import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.home,'OpenAI Responses transformer')
if(process.env.CC_HAHA_RESPONSES_FIXTURE!=='1')throw new Error('Unowned Responses transformer fixture')
Date.now=()=>1720000000123
const {openaiResponsesToAnthropic}=await import('../../../../src/server/proxy/transform/openaiResponsesToAnthropic')
const {openaiUsageToAnthropic}=await import('../../../../src/server/proxy/transform/usage')
const {parseResponsesToolArguments,responsesTerminalStop}=await import('../../../../src/server/proxy/transform/openaiResponsesTerminal')
const rows=input.rows.map((row:any)=>{try{
 let value:any
 if(row.action==='response')value=openaiResponsesToAnthropic(row.value,row.model||'fixture-model',{preserveOpenAIReasoning:row.reasoning})
 if(row.action==='usage')value=openaiUsageToAnthropic(row.value)
 if(row.action==='arguments')value=parseResponsesToolArguments(row.value)
 if(row.action==='terminal')value=responsesTerminalStop(row.value,row.event)
 return {value,error:null}
}catch(e:any){return {value:null,error:{name:e.name,message:e.message,...(e.type!==undefined?{type:e.type}:{}),...(e.code!==undefined?{code:e.code}:{}),...(e.status!==undefined?{status:e.status}:{})}}}})
process.stdout.write(JSON.stringify({rows})+'\n')
