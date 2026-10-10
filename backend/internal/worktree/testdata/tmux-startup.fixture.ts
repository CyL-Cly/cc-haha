import fs from 'node:fs'
import { parse } from 'acorn'
import path from 'node:path'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
const input=JSON.parse(await Bun.stdin.text())
assertDisposableFixtureHome(input.home,'actual execIntoTmuxWorktree')
if(Bun.version!=='1.3.14'||process.env.CC_HAHA_TMUX_FAST_FIXTURE!=='1')throw new Error('private original function only')
const source=fs.readFileSync(new URL('../../../../src/utils/worktree.ts',import.meta.url),'utf8')
const compiled=new Bun.Transpiler({loader:'ts'}).transformSync(source)
const ast:any=parse(compiled,{ecmaVersion:'latest',sourceType:'module'})
const declarations=ast.body.map((n:any)=>n.type==='ExportNamedDeclaration'?n.declaration:n)
const extract=(name:string)=>{const n=declarations.find((n:any)=>n?.type==='FunctionDeclaration'&&n.id.name===name);if(!n)throw new Error('Missing Source '+name);return compiled.slice(n.start,n.end)}
const constants=declarations.filter((n:any)=>n?.type==='VariableDeclaration'&&n.declarations.some((d:any)=>['VALID_WORKTREE_SLUG_SEGMENT','MAX_WORKTREE_SLUG_LENGTH'].includes(d.id.name))).map((n:any)=>compiled.slice(n.start,n.end)).join('\n')
const validator=new Function(constants+'\n'+extract('validateWorktreeSlug')+';return validateWorktreeSlug')()
const pr=new Function(extract('parsePRReference')+';return parsePRReference')()
const code=extract('execIntoTmuxWorktree')
const result=[]
for(const row of input.rows){
 const calls:any[]=[],output:string[]=[];let random=0
 const fn=new Function('process','spawnSync','parsePRReference','validateWorktreeSlug','hasWorktreeCreateHook','executeWorktreeCreateHook','basename','findCanonicalGitRoot','getCwd','errorMessage','worktreePathFor','getOrCreateWorktree','performPostCreationSetup','worktreeBranchName','isInITerm2','chalk','console','Math',code+';return execIntoTmuxWorktree')
 const p={platform:row.platform??'darwin',env:row.env??{},execPath:'/actual/go-binary'}
 const run=fn(p,(command,args,opts={})=>{calls.push({command,args,...opts.cwd?{cwd:opts.cwd}:{},...opts.env?{env:opts.env}:{},...opts.stdio?{inherit:opts.stdio==='inherit'}:{}});if(args[0]==='-V')return{status:row.missing?1:0};if(args[0]==='show-options')return{status:0,stdout:row.prefix??'prefix C-b\n'};if(args[0]==='has-session')return{status:row.exists?0:1};return{status:row.spawnExit??0}},pr,validator,()=>!!row.hook,async()=>{if(row.hookError)throw new Error(row.hookError);return{worktreePath:row.hookPath??'/actual/hook'}},path.basename,()=>row.root===false?null:(row.root??'/actual/repo.name'),()=>'/actual/original',error=>error.message,(root,name)=>path.join(root,'.claude/worktrees',name.replaceAll('/','+')),async(root,name,opts)=>{if(row.createError)throw new Error(row.createError);return{existed:!!row.worktreeExists,baseBranch:'HEAD'}},async(root,name)=>{},name=>'worktree-'+name.replaceAll('/','+'),()=>!!row.iterm,{yellow:x=>x},{log(...args){output.push(args.join(' ')+'\n')}},{floor:Math.floor,random:()=>[0.1,0.4,0.125][random++]})
 const value=await run(row.args)
 result.push({value,calls,output:output.join('')})
}
process.stdout.write(JSON.stringify(result)+'\n')
