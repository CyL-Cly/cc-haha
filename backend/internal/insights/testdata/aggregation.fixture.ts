import {parse} from 'acorn'
import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.home,'Insights aggregate')
if(process.env.CC_HAHA_INSIGHTS_AGGREGATION_FIXTURE!=='1')throw new Error('Unowned Insights aggregate fixture')
const code=new Bun.Transpiler({loader:'ts'}).transformSync(await Bun.file(new URL('../../../../src/commands/insights.ts',import.meta.url)).text()),ast:any=parse(code,{ecmaVersion:'latest',sourceType:'module'})
const functions=ast.body.map((n:any)=>n.type==='ExportNamedDeclaration'?n.declaration:n).filter((n:any)=>n?.type==='FunctionDeclaration')
const names=['detectMultiClauding','aggregateData','safeEntries','safeKeys']
const bodies=names.map(name=>{const node=functions.find((n:any)=>n.id.name===name);if(!node)throw new Error('Missing original aggregate '+name);return code.slice(node.start,node.end)}).join('\n')
const original=new Function(bodies+'\nreturn aggregateData')()
const rows=input.rows.map((row:any)=>{try{return {value:original(row.sessions,new Map(Object.entries(row.facets))),error:null}}catch(e:any){return {value:null,error:e.name}}})
process.stdout.write(JSON.stringify({rows})+'\n')
