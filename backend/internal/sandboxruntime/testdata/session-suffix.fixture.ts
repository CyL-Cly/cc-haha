import fs from 'node:fs'
import {parse} from 'acorn'
const input=JSON.parse(await Bun.stdin.text())
const source=fs.readFileSync(new URL('../../../../node_modules/@anthropic-ai/sandbox-runtime/dist/sandbox/macos-sandbox-utils.js',import.meta.url),'utf8')
const ast:any=parse(source,{ecmaVersion:'latest',sourceType:'module'})
const declaration=ast.body.flatMap((n:any)=>n.type==='VariableDeclaration'?n.declarations:[]).find((n:any)=>n.id?.name==='sessionSuffix')
if(!declaration)throw new Error('missing Source sessionSuffix')
const expression=source.slice(declaration.init.start,declaration.init.end)
const result=input.rows.map((value:number)=>new Function('Math','return '+expression)({random:()=>value}))
process.stdout.write(JSON.stringify(result)+'\n')
