import { mkdirSync, writeFileSync, existsSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir,'Repository launch')
if(process.env.CC_HAHA_REPOSITORY_LAUNCH_FIXTURE!=='1')throw new Error('Missing Repository launch fixture')
const home=input.homeDir,root=join(home,'repo'),privateSpawn=Bun.spawn.bind(Bun)
const env={...process.env,HOME:home,USERPROFILE:home,GIT_CONFIG_NOSYSTEM:'1',GIT_CONFIG_GLOBAL:join(home,'absent-gitconfig'),GIT_AUTHOR_NAME:'fixture',GIT_AUTHOR_EMAIL:'fixture@example.invalid',GIT_COMMITTER_NAME:'fixture',GIT_COMMITTER_EMAIL:'fixture@example.invalid',GIT_AUTHOR_DATE:'2026-10-01T00:00:00Z',GIT_COMMITTER_DATE:'2026-10-01T00:00:00Z',PATH:'/usr/bin:/bin',GIT_TERMINAL_PROMPT:'0',GIT_ASKPASS:''}
Object.assign(process.env,env)
async function git(args:string[],cwd=root,childEnv:any=env){
 if(!cwd.startsWith(home)||args.some(value=>value.includes('https://')||value.includes('ssh://')))throw new Error('External git denied')
 const child=privateSpawn(['/usr/bin/git',...args],{cwd,env:childEnv,stdin:'ignore',stdout:'pipe',stderr:'pipe'})
 const [code,stdout,stderr]=await Promise.all([child.exited,new Response(child.stdout).text(),new Response(child.stderr).text()])
 return {code,stdout,stderr}
}
const absent=absentManagedSettingsFixturePaths()
for(let directory=dirname(home);;directory=dirname(directory)){absent.push(join(directory,'.git'));if(directory===dirname(directory))break}
const capabilities=await installClientFixtureCapabilities(home,{absentPaths:absent})
await import('../../../preload')
const cp=await import('node:child_process')
const {promisify}=await import('node:util')
const bridge:any=(_file:string,_args:string[],_options:any,_callback:any)=>{throw new Error('Original promisified path required')}
bridge[promisify.custom]=async(file:string,args:string[],options:any)=>{
 if(file!=='git')throw new Error('Only actual private git')
 const result=await git(args,options.cwd,{...env,...options.env})
 if(result.code!==0)throw Object.assign(new Error('git failed'),{code:result.code,stdout:result.stdout,stderr:result.stderr})
 return {stdout:result.stdout,stderr:result.stderr}
}
mock.module('node:child_process',()=>({...cp,execFile:bridge}))
mkdirSync(root,{recursive:true});await git(['init','-b','main']);writeFileSync(join(root,'tracked.txt'),'private repository');await git(['add','tracked.txt']);await git(['commit','-m','initial private repository']);await git(['branch','feature']);await git(['update-ref','refs/remotes/origin/remote-only','HEAD'])
const state=await import('../../../src/bootstrap/state');state.setCwdState(root);state.setOriginalCwd(root);state.setProjectRoot(root);state.setIsInteractive(false)
const config=await import('../../../src/utils/config');config.enableConfigs()
const service=await import('../../../src/server/services/repositoryLaunchService')
const sid='11111111-1111-4111-8111-111111111111'
const rows=[]
async function observe(fn:()=>Promise<any>){try{return {value:await fn()}}catch(error:any){return {error:{code:error.code,message:error.message,status:error.statusCode}}}}
for(const options of [undefined,{}, {branch:'feature'}, {branch:'feature',worktree:true},{branch:null,worktree:true},{branch:'remote-only',worktree:true},{branch:'missing'}, {branch:'   '},{branch:'feature',worktree:false}])rows.push(await observe(()=>service.resolveSessionWorkspaceLaunch(root,options,sid)))
const branchRows=[]
for(const options of [{name:'topic',from:'remote-only'},{name:'topic'},{name:'worktree-desktop-reserved'},{name:'-flag'},{name:'bad\nname'},{name:'a'.repeat(201)},{name:'',from:'missing'},{name:'new',from:'missing'}])branchRows.push(await observe(()=>service.createRepositoryBranch(root,options)))
const context=await service.getRepositoryContext(root)
const planned=rows[3]!.value
if(!planned)throw new Error(JSON.stringify({rows,capabilities}))
const launchStates=[{workDir:root,repository:planned.repository,transcriptMessageCount:0},{workDir:planned.repository.worktreePath,repository:planned.repository,transcriptMessageCount:0},{workDir:root,repository:planned.repository,transcriptMessageCount:1},{workDir:root,repository:planned.repository,worktreeSession:{worktreePath:planned.repository.worktreePath},transcriptMessageCount:0}].map(value=>({materialized:service.isMaterializedWorktreeLaunch(value),create:service.shouldCreateWorktreeForSessionLaunch(value)}))
const switched=await observe(()=>service.prepareSessionWorkspace(root,{branch:'feature'},sid))
const actualBranch=(await git(['branch','--show-current'])).stdout.trim()
process.stdout.write(JSON.stringify({rows,branchRows,context,launchStates,switched,actualBranch,notCreated:!existsSync(planned.repository.worktreePath),home,capabilities}))
process.exit(0)
