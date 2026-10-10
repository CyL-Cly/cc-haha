import {parse} from 'acorn'
import {extname,join} from 'node:path'
import {mkdir,readFile,writeFile,unlink} from 'node:fs/promises'
import {diffLines} from 'diff'
import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.home,'Insights models/report')
if(process.env.CC_HAHA_INSIGHTS_REPORT_FIXTURE!=='1')throw new Error('Unowned Insights model/report fixture')
const code=new Bun.Transpiler({loader:'ts'}).transformSync(await Bun.file(new URL('../../../../src/commands/insights.ts',import.meta.url)).text()),ast:any=parse(code,{ecmaVersion:'latest',sourceType:'module'})
const functions=ast.body.map((n:any)=>n.type==='ExportNamedDeclaration'?n.declaration:n).filter((n:any)=>n?.type==='FunctionDeclaration')
const names=['getDataDir','getFacetsDir','getSessionMetaDir','hasValidDates','safeKeys','safeEntries','loadCachedFacets','saveFacets','loadCachedSessionMeta','saveSessionMeta','detectMultiClauding','aggregateData','generateUsageReport','getLanguageFromPath','extractToolStats','logToSessionMeta','formatTranscriptForFacets','summarizeTranscriptChunk','formatTranscriptWithSummarization','extractFacetsFromAPI','isValidSessionFacets','generateSectionInsight','generateParallelInsights','escapeHtmlWithBold','generateBarChart','generateResponseTimeHistogram','generateTimeOfDayChart','getHourCountsJson','generateHtmlReport']
const bodies=names.map(name=>{const node=functions.find((n:any)=>n.id.name===name);if(!node)throw new Error('Missing original '+name);return code.slice(node.start,node.end)}).join('\n')
const declarations=['EXTENSION_TO_LANGUAGE','FACET_EXTRACTION_PROMPT','SUMMARIZE_CHUNK_PROMPT','INSIGHT_SECTIONS','LABEL_MAP','OUTCOME_ORDER','SATISFACTION_ORDER'].map(name=>{const node=ast.body.find((n:any)=>n.type==='VariableDeclaration'&&n.declarations.some((d:any)=>d.id.name===name));if(!node)throw new Error('Missing original '+name);return code.slice(node.start,node.end)}).join('\n')
const escapeHtml=(s:string)=>s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;').replace(/'/g,'&apos;')
const queries:any[]=[], errors:string[]=[]
let responseIndex=0
const queryWithModel=async(r:any)=>{queries.push({prompt:r.userPrompt,maxTokens:r.options.maxOutputTokensOverride,model:r.options.model});const value=input.responses?.[responseIndex++];if(value?.error)throw new Error(value.error);return {message:{content:[{type:'text',text:value?.text??''}]}}}
const command=ast.body.find((n:any)=>n.type==='VariableDeclaration'&&n.declarations.some((d:any)=>d.id.name==='usageReport'));if(!command)throw new Error('Missing original report command')
const factory=new Function('queryWithModel','extname','diffLines','countCharInString','AGENT_TOOL_NAME','LEGACY_AGENT_TOOL_NAME','getSessionIdFromLog','asSystemPrompt','getAnalysisModel','getInsightsModel','extractTextContent','jsonParse','jsonStringify','logError','toError','escapeHtml','process','getClaudeConfigHomeDir','join','mkdir','readFile','writeFile','unlink','scanAllSessions','loadAllLogsFromSessionFile',declarations+'\n'+bodies+'\n'+code.slice(command.start,command.end)+'\nreturn {formatTranscriptWithSummarization,extractFacetsFromAPI,generateParallelInsights,generateHtmlReport,generateUsageReport,usageReport}')
const original=factory(queryWithModel,extname,diffLines,(t:string,c:string)=>t.split(c).length-1,'Agent','Task',(log:any)=>log.sessionId,(x:any)=>x,()=> 'source-opus',()=> 'source-opus',(blocks:any[])=>blocks.filter(b=>b.type==='text').map(b=>b.text).join(''),JSON.parse,JSON.stringify,(e:any)=>errors.push(e.message),(e:any)=>e,escapeHtml,{env:{}},()=>join(input.home,'.claude'),join,mkdir,readFile,writeFile,unlink,async()=>input.scanned||[],async(path:string)=>(input.files?.[path]||[]).map((log:any)=>({...log,created:new Date(log.createdMillis),modified:new Date(log.modifiedMillis)})))
const logs=(input.logs||[]).map((log:any)=>({...log,created:new Date(log.createdMillis),modified:new Date(log.modifiedMillis)}))
try{
 let value:any
 if(input.action==='transcript')value=await original.formatTranscriptWithSummarization(logs[0])
 if(input.action==='facets')value=await Promise.all(logs.map((log:any)=>original.extractFacetsFromAPI(log,log.sessionId)))
 if(input.action==='narrative')value=await original.generateParallelInsights(input.data,new Map((input.facets||[]).map((f:any)=>[f.session_id,f])))
 if(input.action==='html')value=original.generateHtmlReport(input.data,input.insights)
 if(input.action==='command')value=await original.usageReport.getPromptForCommand(input.args||'')
 if(input.action==='report'){const report=await original.generateUsageReport();value={...report,facets:Array.from(report.facets.values())}}
 process.stdout.write(JSON.stringify({value,queries,errors})+'\n')
}catch(e:any){process.stdout.write(JSON.stringify({value:null,queries,errors,error:e.name+": "+e.message})+'\n')}
