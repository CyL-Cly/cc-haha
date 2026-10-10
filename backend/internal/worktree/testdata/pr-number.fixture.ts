import fs from 'node:fs'
import { parse } from 'acorn'
const input = JSON.parse(await Bun.stdin.text())
if (Bun.version !== '1.3.14') throw new Error('pinned Source runtime required')
const text = fs.readFileSync(new URL('../../../../src/utils/worktree.ts', import.meta.url), 'utf8')
const code = new Bun.Transpiler({loader:'ts'}).transformSync(text)
const ast:any = parse(code, {ecmaVersion:'latest',sourceType:'module'})
const declaration = ast.body.map((n:any)=>n.type==='ExportNamedDeclaration'?n.declaration:n).find((n:any)=>n?.type==='FunctionDeclaration'&&n.id.name==='parsePRReference')
if (!declaration) throw new Error('missing original parsePRReference')
const original = new Function(code.slice(declaration.start,declaration.end)+';return parsePRReference')()
process.stdout.write(JSON.stringify(input.values.map((input:string)=>{const number=original(input);return {input,number:number===null?null:String(number),truthy:!!number}}))+'\n')
