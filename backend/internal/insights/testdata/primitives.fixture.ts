import {parse} from 'acorn'
import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.home,'Insights primitives')
if(process.env.CC_HAHA_INSIGHTS_PRIMITIVES_FIXTURE!=='1')throw new Error('Unowned Insights primitives fixture')
const code=new Bun.Transpiler({loader:'ts'}).transformSync(await Bun.file(new URL('../../../../src/commands/insights.ts',import.meta.url)).text()),ast:any=parse(code,{ecmaVersion:'latest',sourceType:'module'})
const functions=ast.body.map((node:any)=>node.type==='ExportNamedDeclaration'?node.declaration:node).filter((node:any)=>node?.type==='FunctionDeclaration')
const names=['detectMultiClauding','deduplicateSessionBranches','isValidSessionFacets']
const bodies=names.map(name=>{const node=functions.find((node:any)=>node.id.name===name);if(!node)throw new Error('Original Insights function missing '+name);return code.slice(node.start,node.end)}).join('\n')
const original=new Function(bodies+'\nreturn {detectMultiClauding,deduplicateSessionBranches,isValidSessionFacets}')()
process.stdout.write(JSON.stringify({multi:input.sessions.map(original.detectMultiClauding),dedup:input.metas.map((metas:any[])=>original.deduplicateSessionBranches(metas.map((meta,index)=>({meta,log:index}))).map((entry:any)=>entry.log)),valid:input.facets.map(original.isValidSessionFacets)})+'\n')
