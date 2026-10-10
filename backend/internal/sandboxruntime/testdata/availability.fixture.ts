import fs from 'node:fs'
import {parse} from 'acorn'
const input=JSON.parse(await Bun.stdin.text())
const text=new Bun.Transpiler({loader:'ts'}).transformSync(fs.readFileSync(new URL('../../../../src/utils/sandbox/sandbox-adapter.ts',import.meta.url),'utf8'))
const ast:any=parse(text,{ecmaVersion:'latest',sourceType:'module'})
const nodes=ast.body.map((n:any)=>n.type==='ExportNamedDeclaration'?n.declaration:n)
const extract=(name:string)=>{const n=nodes.find((n:any)=>n?.type==='FunctionDeclaration'&&n.id.name===name);if(!n)throw new Error('Missing Source '+name);return text.slice(n.start,n.end)}
const output=[]
for(const row of input.rows){
 const dependencies={getSettings_DEPRECATED:()=>row.settings,getInitialSettings:()=>row.initial,getPlatform:()=>row.platform,isSupportedPlatform:()=>row.supported,checkDependencies:()=>({errors:row.errors,warnings:[]}),logForDebugging:()=>{}}
 const fn=new Function('dependencies',`with(dependencies){${['getSandboxEnabledSetting','isPlatformInEnabledList','isSandboxingEnabled','isSandboxRequired','getSandboxUnavailableReason'].map(extract).join('\n')};return {enabled:isSandboxingEnabled(),required:isSandboxRequired(),reason:getSandboxUnavailableReason()??''}}`)
 output.push(fn(dependencies))
}
process.stdout.write(JSON.stringify(output)+'\n')
