import fs from 'node:fs'
import {parse} from 'acorn'
import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
const input=JSON.parse(await Bun.stdin.text());assertDisposableFixtureHome(input.home,'actual terminal detector')
function compile(relative:string){const source=new Bun.Transpiler({loader:'ts'}).transformSync(fs.readFileSync(new URL(relative,import.meta.url),'utf8'));const ast:any=parse(source,{ecmaVersion:'latest',sourceType:'module'});const nodes=ast.body.map(n=>n.type==='ExportNamedDeclaration'?n.declaration:n);return {source,nodes,fn(name){const node=nodes.find(n=>n?.type==='FunctionDeclaration'&&n.id.name===name);return source.slice(node.start,node.end)}}}
const terminal=compile('../../../../src/utils/env.ts'),detection=compile('../../../../src/utils/swarm/backends/detection.ts')
const constant=terminal.nodes.find(n=>n?.type==='VariableDeclaration'&&n.declarations.some(d=>d.id.name==='JETBRAINS_IDES'))
const detectorCode=terminal.source.slice(constant.start,constant.end)+'\n'+terminal.fn('detectTerminal')+'\n'+terminal.fn('isSSHSession')
const out=input.rows.map(env=>{const process={env,platform:'darwin',stdout:{isTTY:true}};const detect=new Function('process',detectorCode+';return detectTerminal')(process);const code='let isInITerm2Cached=null;'+detection.fn('isInITerm2')+';return isInITerm2';return new Function('process','env',code)(process,{terminal:detect()})()})
process.stdout.write(JSON.stringify(out))
