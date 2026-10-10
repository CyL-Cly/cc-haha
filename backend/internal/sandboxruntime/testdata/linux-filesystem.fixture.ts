import fs from 'node:fs'
import path from 'node:path'
import os from 'node:os'
import {parse} from 'acorn'
import {ripGrep} from '@anthropic-ai/sandbox-runtime/dist/utils/ripgrep.js'
import {normalizePathForSandbox,normalizeCaseForComparison,isSymlinkOutsideBoundary,DANGEROUS_FILES,getDangerousDirectories} from '@anthropic-ai/sandbox-runtime/dist/sandbox/sandbox-utils.js'
const input=JSON.parse(await Bun.stdin.text())
process.chdir(input.cwd)
const source=fs.readFileSync(new URL('../../../../node_modules/@anthropic-ai/sandbox-runtime/dist/sandbox/linux-sandbox-utils.js',import.meta.url),'utf8')
const ast:any=parse(source,{ecmaVersion:'latest',sourceType:'module'})
const nodes=ast.body.map((n:any)=>n.type==='ExportNamedDeclaration'?n.declaration:n)
const extract=(name:string)=>{const n=nodes.find((n:any)=>n?.type==='FunctionDeclaration'&&n.id.name===name);if(!n)throw new Error('Missing Source '+name);return source.slice(n.start,n.end)}
const output=[]
for(const row of input.rows){
 const bwrapMountPoints=new Set<string>()
 const dependencies={fs,path,tmpdir:()=>input.tmp,normalizePathForSandbox,normalizeCaseForComparison,isSymlinkOutsideBoundary,DANGEROUS_FILES,getDangerousDirectories,ripGrep,logForDebugging:()=>{},DEFAULT_MANDATORY_DENY_SEARCH_DEPTH:3,bwrapMountPoints,registerExitCleanupHandler:()=>{}}
 const api=new Function('dependencies',`with(dependencies){${['findSymlinkInPath','hasFileAncestor','findFirstNonExistentComponent','linuxGetMandatoryDenyPaths','generateFilesystemArgs'].map(extract).join('\n')};return generateFilesystemArgs}`)(dependencies)
 const args=await api(row.read,row.write,{command:input.rg},row.depth??3,row.allowGitConfig??false)
 output.push({args,mountPoints:[...bwrapMountPoints]})
}
process.stdout.write(JSON.stringify(output)+'\n')
