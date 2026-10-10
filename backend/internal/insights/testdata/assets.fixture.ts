import {parse} from 'acorn'
const code=new Bun.Transpiler({loader:'ts'}).transformSync(await Bun.file(new URL('../../../../src/commands/insights.ts',import.meta.url)).text())
const ast:any=parse(code,{ecmaVersion:'latest',sourceType:'module'})
const out:any={}
for(const name of ['FACET_EXTRACTION_PROMPT','SUMMARIZE_CHUNK_PROMPT','INSIGHT_SECTIONS','LABEL_MAP','OUTCOME_ORDER','SATISFACTION_ORDER']){
 const node=ast.body.find((n:any)=>n.type==='VariableDeclaration'&&n.declarations.some((d:any)=>d.id.name===name))
 if(!node)throw new Error('missing original declaration '+name)
 out[name]=new Function('process',code.slice(node.start,node.end)+';return '+name)({env:{}})
}
const functions=ast.body.map((n:any)=>n.type==='ExportNamedDeclaration'?n.declaration:n).filter((n:any)=>n?.type==='FunctionDeclaration')
const gather=(node:any,action:(n:any)=>void)=>{if(!node||typeof node!=='object')return;action(node);for(const [key,value]of Object.entries(node)){if(['start','end'].includes(key))continue;if(Array.isArray(value))value.forEach(n=>gather(n,action));else gather(value,action)}}
for(const name of ['generateParallelInsights','generateHtmlReport','generateBarChart','generateResponseTimeHistogram','generateTimeOfDayChart']){
 const node=functions.find((n:any)=>n.id.name===name);if(!node)throw new Error('missing '+name)
 const literals:any[]=[]
 gather(node,(n:any)=>{if(n.type==='TemplateLiteral')literals.push({parts:n.quasis.map((q:any)=>q.value.cooked),expressions:n.expressions.map((e:any)=>code.slice(e.start,e.end))})})
 out[name]=literals
}
process.stdout.write(JSON.stringify(out,null,2)+'\n')
