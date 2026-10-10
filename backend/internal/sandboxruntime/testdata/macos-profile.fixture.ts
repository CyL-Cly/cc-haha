import fs from 'node:fs'
import {parse} from 'acorn'
import * as path from 'node:path'
import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import * as helpers from '@anthropic-ai/sandbox-runtime/dist/sandbox/sandbox-utils.js'
const input=JSON.parse(await Bun.stdin.text()),home=assertDisposableFixtureHome(input.home,'actual Seatbelt profile producer')
if(Bun.version!=='1.3.14')throw new Error('pinned Source required')
process.chdir(input.cwd)
const code=fs.readFileSync(new URL('../../../../node_modules/@anthropic-ai/sandbox-runtime/dist/sandbox/macos-sandbox-utils.js',import.meta.url),'utf8')
const ast:any=parse(code,{ecmaVersion:'latest',sourceType:'module'})
const nodes=ast.body.map((n:any)=>n.type==='ExportNamedDeclaration'?n.declaration:n)
const extract=(name:string)=>{const n=nodes.find((n:any)=>n?.type==='FunctionDeclaration'&&n.id.name===name);if(!n)throw new Error('missing actual '+name);return code.slice(n.start,n.end)}
const dependencies:any={...helpers,path,process}
const profile=new Function('dependencies',`with(dependencies){${['macGetMandatoryDenyPatterns','getAncestorDirectories','generateMoveBlockingRules','generateReadRules','generateWriteRules','generateSandboxProfile','escapePath'].map(extract).join('\n')};return generateSandboxProfile}`)(dependencies)
const results=[]
for(const row of input.rows){
 results.push({profile:profile({...row,readConfig:row.read,writeConfig:row.write,logTag:'CMD64_OWNED_END_PRIVATE_SBX'}),normalized:(row.paths??[]).map(helpers.normalizePathForSandbox),regex:(row.paths??[]).map(helpers.globToRegex),mandatory:profile?new Function('dependencies',`with(dependencies){${extract('macGetMandatoryDenyPatterns')};return macGetMandatoryDenyPatterns}`)(dependencies)(!!row.allowGitConfig):[]})
}
process.stdout.write(JSON.stringify(results)+'\n')
