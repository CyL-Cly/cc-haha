import fs from 'node:fs'
import {parse} from 'acorn'
import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
const input=JSON.parse(await Bun.stdin.text());assertDisposableFixtureHome(input.home,'actual tmux entrypoint')
const source=new Bun.Transpiler({loader:'ts'}).transformSync(fs.readFileSync(new URL('../../../../src/entrypoints/cli.tsx',import.meta.url),'utf8'))
const ast:any=parse(source,{ecmaVersion:'latest',sourceType:'module'})
const top=ast.body.filter(n=>n.type!=='ImportDeclaration'&&n.type==='ExpressionStatement'||n.type==='IfStatement').filter(n=>n.start<ast.body.find(n=>n.type==='FunctionDeclaration'&&n.id.name==='main').start).map(n=>source.slice(n.start,n.end)).join('\n')
const main=ast.body.find(n=>n.type==='FunctionDeclaration'&&n.id.name==='main')
const declaration=main.body.body.find(n=>n.type==='VariableDeclaration'&&n.declarations.some(d=>d.id.name==='hasTmuxFlag'))
const conditional=main.body.body.find(n=>n.type==='IfStatement'&&source.slice(n.test.start,n.test.end).startsWith('hasTmuxFlag'))
const gate=new Function('args',source.slice(declaration.start,declaration.end)+';return '+source.slice(conditional.test.start,conditional.test.end))
const environments=input.environments.map(env=>{const process={env:{...env}};new Function('process','feature',top)(process,name=>name==='TRANSCRIPT_CLASSIFIER');return process.env})
process.stdout.write(JSON.stringify({environments,gates:input.args.map(gate)}))
