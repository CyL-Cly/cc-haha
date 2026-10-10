import fs from 'node:fs'
import {parse} from 'acorn'
const input=JSON.parse(await Bun.stdin.text())
const source=fs.readFileSync(new URL('../../../../node_modules/@anthropic-ai/sandbox-runtime/dist/sandbox/linux-sandbox-utils.js',import.meta.url),'utf8')
const ast:any=parse(source,{ecmaVersion:'latest',sourceType:'module'})
const functions=ast.body.map((n:any)=>n.type==='ExportNamedDeclaration'?n.declaration:n)
const definition=functions.find((n:any)=>n?.type==='FunctionDeclaration'&&n.id.name==='checkLinuxDependencies')
if(!definition)throw new Error('missing Source checkLinuxDependencies')
const body=source.slice(definition.start,definition.end)
const output=input.rows.map((row:any)=>{
 const deps={whichSync:(command:string)=>(command==='bwrap'?row.hasBwrap:row.hasSocat)?'/native/'+command:null,getPreGeneratedBpfPath:()=>row.hasSeccompBpf?'/native/bpf':null,getApplySeccompBinaryPath:()=>row.hasSeccompApply?'/native/apply':null}
 return new Function('dependencies',`with(dependencies){${body};return checkLinuxDependencies()}`)(deps)
})
process.stdout.write(JSON.stringify(output)+'\n')
