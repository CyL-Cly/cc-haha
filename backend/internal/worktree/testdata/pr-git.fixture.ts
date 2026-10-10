import {mkdirSync,writeFileSync,readFileSync} from 'node:fs'
import {dirname,join} from 'node:path'
import {mock,spyOn} from 'bun:test'
import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import {absentManagedSettingsFixturePaths,installClientFixtureCapabilities} from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'
const input=JSON.parse(await Bun.stdin.text()),home=assertDisposableFixtureHome(input.home,'large PR original physical git'),root=join(home,'repo')
const spawn=Bun.spawn.bind(Bun)
const env={HOME:home,USERPROFILE:home,XDG_CONFIG_HOME:home,GIT_CONFIG_NOSYSTEM:'1',GIT_CONFIG_GLOBAL:join(home,'absent'),GIT_AUTHOR_NAME:'fixture',GIT_AUTHOR_EMAIL:'fixture@example.invalid',GIT_COMMITTER_NAME:'fixture',GIT_COMMITTER_EMAIL:'fixture@example.invalid',GIT_AUTHOR_DATE:'2026-10-01T00:00:00Z',GIT_COMMITTER_DATE:'2026-10-01T00:00:00Z',GIT_TERMINAL_PROMPT:'0',GIT_ASKPASS:'',PATH:'/usr/bin:/bin'}
async function git(args:string[],cwd=root,environment=env){if(!cwd.startsWith(home)||args.some(x=>x.includes('https://')||x.includes('ssh://')))throw new Error('private git boundary');const p=spawn(['/usr/bin/git',...args],{cwd,env:environment,stdin:'ignore',stdout:'pipe',stderr:'pipe'});const [code,stdout,stderr]=await Promise.all([p.exited,new Response(p.stdout).text(),new Response(p.stderr).text()]);return{code,stdout,stderr}}
const absent=absentManagedSettingsFixturePaths();for(let dir=dirname(home);;dir=dirname(dir)){absent.push(join(dir,'.git'));if(dir===dirname(dir))break}
const capabilities=await installClientFixtureCapabilities(home,{absentPaths:absent})
await import('../../../../preload')
mkdirSync(root,{recursive:true});await git(['init','-b','main']);writeFileSync(join(root,'tracked.txt'),'private tracked file');await git(['add','.']);await git(['commit','-m','initial private fixture'])
const refNumber=Number(input.refNumber)
if(Number.isFinite(refNumber))await git(['update-ref',`refs/pull/${refNumber}/head`,'HEAD'])
const origin=join(home,'origin.git');await git(['clone','--bare',root,origin]);if(Number.isFinite(refNumber))await git(['--git-dir',origin,'update-ref',`refs/pull/${refNumber}/head`,'refs/heads/main']);await git(['remote','add','origin',origin])
const calls:any[]=[]
const sourceGit=await import('../../../../src/utils/git');spyOn(sourceGit,'gitExe').mockImplementation(()=>'/usr/bin/git')
const originalExec=await import('../../../../src/utils/execFileNoThrow')
const run=async(_file:string,args:string[],options:any={})=>{calls.push({args,noPrompt:options.env?.GIT_TERMINAL_PROMPT==='0'&&options.env?.GIT_ASKPASS===''});return git(args,options.cwd??root,{...env,...options.env})}
mock.module('../../../../src/utils/execFileNoThrow',()=>({...originalExec,execFileNoThrow:run,execFileNoThrowWithCwd:run}))
const state=await import('../../../../src/bootstrap/state');state.setCwdState(root);state.setOriginalCwd(root);state.setProjectRoot(root);state.setIsInteractive(false)
const config=await import('../../../../src/utils/config');config.enableConfigs()
const worktree=await import('../../../../src/utils/worktree')
const number=worktree.parsePRReference(input.reference)
if(number===null)throw new Error('expected Source PR')
const slug=`pr-${number}`
let error:string|null=null,bytes:string|null=null
try{const result=await worktree.createWorktreeForSession('11111111-1111-4111-8111-111111111111',slug,undefined,{prNumber:number});bytes=readFileSync(join(result.worktreePath,'tracked.txt'),'utf8')}catch(e:any){error=e.message}
process.stdout.write(JSON.stringify({number:String(number),slug,error,bytes,calls,capabilities})+'\n')
process.exit(0)
