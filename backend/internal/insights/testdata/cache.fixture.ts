import {parse} from 'acorn'
import {join} from 'node:path'
import * as files from 'node:fs/promises'
import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
const home=assertDisposableFixtureHome(input.home,'Insights cache')
if(process.env.CC_HAHA_INSIGHTS_CACHE_FIXTURE!=='1')throw new Error('Unowned Insights cache fixture')
const source=new Bun.Transpiler({loader:'ts'}).transformSync(await Bun.file(new URL('../../../../src/commands/insights.ts',import.meta.url)).text())
const ast:any=parse(source,{ecmaVersion:'latest',sourceType:'module'})
const names=['getDataDir','getFacetsDir','getSessionMetaDir','loadCachedFacets','saveFacets','loadCachedSessionMeta','saveSessionMeta','isValidSessionFacets']
const functions=ast.body.map((n:any)=>n.type==='ExportNamedDeclaration'?n.declaration:n).filter((n:any)=>n?.type==='FunctionDeclaration')
const bodies=names.map(name=>{const node=functions.find((n:any)=>n.id.name===name);if(!node)throw new Error('Actual Insights cache function missing '+name);return source.slice(node.start,node.end)}).join('\n')
const original=new Function('join','getClaudeConfigHomeDir','readFile','writeFile','mkdir','unlink','jsonParse','jsonStringify',bodies+'\nreturn {loadCachedFacets,saveFacets,loadCachedSessionMeta,saveSessionMeta,isValidSessionFacets}')(join,()=>join(home,'.claude'),files.readFile,files.writeFile,files.mkdir,files.unlink,JSON.parse,JSON.stringify)
const rows=[]
for(const action of input.actions){
 const kind=action.kind==='facets'?'facets':'session-meta',path=join(home,'.claude','usage-data',kind,action.id+'.json')
 if('raw' in action){await files.mkdir(join(home,'.claude','usage-data',kind),{recursive:true});await files.writeFile(path,action.raw)}
 let value:any,error:any
 try{if(action.op==='load')value=await(kind==='facets'?original.loadCachedFacets(action.id):original.loadCachedSessionMeta(action.id));else await(kind==='facets'?original.saveFacets(action.value):original.saveSessionMeta(action.value))}catch(e:any){error=e.code}
 let disk:any=null,mode:any=null;try{disk=await files.readFile(path,'utf8');mode=(await files.stat(path)).mode&0o777}catch{}
 rows.push({value:value??null,error:error??null,disk,mode})
}
process.stdout.write(JSON.stringify({rows})+'\n')
