import {parse} from 'acorn'
import {extname} from 'node:path'
import {diffLines} from 'diff'
import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.home,'Insights statistics')
if(process.env.CC_HAHA_INSIGHTS_STATS_FIXTURE!=='1')throw new Error('Unowned Insights stats fixture')
const code=new Bun.Transpiler({loader:'ts'}).transformSync(await Bun.file(new URL('../../../../src/commands/insights.ts',import.meta.url)).text()),ast:any=parse(code,{ecmaVersion:'latest',sourceType:'module'})
const functions=ast.body.map((n:any)=>n.type==='ExportNamedDeclaration'?n.declaration:n).filter((n:any)=>n?.type==='FunctionDeclaration')
const names=['getLanguageFromPath','extractToolStats','logToSessionMeta']
const bodies=names.map(name=>{const node=functions.find((n:any)=>n.id.name===name);if(!node)throw new Error('Missing original stats '+name);return code.slice(node.start,node.end)}).join('\n')
const mapping=ast.body.find((n:any)=>n.type==='VariableDeclaration'&&n.declarations.some((d:any)=>d.id.name==='EXTENSION_TO_LANGUAGE'))
if(!mapping)throw new Error('Missing original language map')
const sessionCode=new Bun.Transpiler({loader:'ts'}).transformSync(await Bun.file(new URL('../../../../src/utils/sessionStorage.ts',import.meta.url)).text()),sessionAst:any=parse(sessionCode,{ecmaVersion:'latest',sourceType:'module'})
const getter=sessionAst.body.map((n:any)=>n.type==='ExportNamedDeclaration'?n.declaration:n).find((n:any)=>n?.type==='FunctionDeclaration'&&n.id.name==='getSessionIdFromLog')
if(!getter)throw new Error('Missing original session id getter')
const original=new Function('extname','diffLines','countCharInString','AGENT_TOOL_NAME','LEGACY_AGENT_TOOL_NAME',code.slice(mapping.start,mapping.end)+'\n'+sessionCode.slice(getter.start,getter.end)+'\n'+bodies+'\nreturn logToSessionMeta')(extname,diffLines,(text:string,char:string)=>text.split(char).length-1,'Agent','Task')
const rows=input.logs.map((log:any)=>{try{return {value:original({...log,created:new Date(log.createdMillis),modified:new Date(log.modifiedMillis)}),error:null}}catch(e:any){return {value:null,error:e.name}}})
process.stdout.write(JSON.stringify({rows})+'\n')
