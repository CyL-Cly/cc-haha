import fs from 'node:fs'
import {parse} from 'acorn'
import {isIP} from 'node:net'
import {isValidHost,canonicalizeHost,stripBrackets} from '@anthropic-ai/sandbox-runtime/dist/sandbox/parent-proxy.js'
const input=JSON.parse(await Bun.stdin.text())
if(Bun.version!=='1.3.14')throw new Error('pinned actual sandbox policy required')
const source=fs.readFileSync(new URL('../../../../node_modules/@anthropic-ai/sandbox-runtime/dist/sandbox/sandbox-manager.js',import.meta.url),'utf8')
const ast:any=parse(source,{ecmaVersion:'latest',sourceType:'module'})
const functions=ast.body.filter((n:any)=>n.type==='FunctionDeclaration')
const extract=(name:string)=>{const n=functions.find((n:any)=>n.id.name===name);if(!n)throw new Error('missing actual '+name);return source.slice(n.start,n.end)}
const results=[]
for(const row of input.rows){
 const calls:any[]=[]
 const config=row.config===null?undefined:{network:row.config}
 const factory=new Function('config','isValidHost','canonicalizeHost','stripBrackets','isIP','logForDebugging',extract('matchesDomainPattern')+'\n'+extract('filterNetworkRequest')+';return filterNetworkRequest')
 const fn=factory(config,isValidHost,canonicalizeHost,stripBrackets,isIP,()=>{})
 const ask=row.ask===undefined?undefined:async(value:any)=>{calls.push(value);if(row.ask==='error')throw new Error('actual callback error');return row.ask}
 results.push({allowed:await fn(row.port,row.host,ask),calls,valid:isValidHost(row.host),canonical:canonicalizeHost(row.host)??null})
}
process.stdout.write(JSON.stringify(results)+'\n')
