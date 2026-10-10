import { mkdirSync,writeFileSync,existsSync,statSync,utimesSync,unlinkSync } from 'node:fs'
import { dirname,join } from 'node:path'
import { mock,spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths,installClientFixtureCapabilities } from './client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir,'Agent worktree')
if(process.env.CC_HAHA_AGENT_WORKTREE_FIXTURE!=='1')throw new Error('Missing Agent worktree fixture')
const privateSpawn=Bun.spawn.bind(Bun)
const home=input.homeDir,root=join(home,'repo'),commands:any[]=[]
const env={...process.env,HOME:home,USERPROFILE:home,XDG_CONFIG_HOME:home,GIT_CONFIG_NOSYSTEM:'1',GIT_CONFIG_GLOBAL:join(home,'absent-gitconfig'),GIT_AUTHOR_NAME:'fixture',GIT_AUTHOR_EMAIL:'fixture@example.invalid',GIT_COMMITTER_NAME:'fixture',GIT_COMMITTER_EMAIL:'fixture@example.invalid',GIT_AUTHOR_DATE:'2026-10-01T00:00:00Z',GIT_COMMITTER_DATE:'2026-10-01T00:00:00Z',GIT_TERMINAL_PROMPT:'0',GIT_ASKPASS:'',PATH:'/usr/bin:/bin'}
async function lowerGit(args:string[],cwd=root,childEnv:any=env){
 if(!cwd.startsWith(home)||args.some(value=>value.includes('https://')||value.includes('ssh://')))throw new Error('External git operation denied')
 const child=privateSpawn(['/usr/bin/git',...args],{cwd,env:childEnv,stdin:'ignore',stdout:'pipe',stderr:'pipe'})
 const [code,stdout,stderr]=await Promise.all([child.exited,new Response(child.stdout).text(),new Response(child.stderr).text()])
 return {code,stdout,stderr}
}
const absent=[...absentManagedSettingsFixturePaths()]
for(let directory=dirname(home);;directory=dirname(directory)){absent.push(join(directory,'.git'));if(directory===dirname(directory))break}
const capabilities=await installClientFixtureCapabilities(home,{absentPaths:absent})
await import('../../../preload')
mkdirSync(root,{recursive:true});await lowerGit(['init','-b','main']);writeFileSync(join(root,'tracked.txt'),'private tracked file');await lowerGit(['add','tracked.txt']);await lowerGit(['commit','-m','initial private fixture'])
const git=await import('../../../src/utils/git');spyOn(git,'gitExe').mockImplementation(()=>'/usr/bin/git')
const sourceExec=await import('../../../src/utils/execFileNoThrow')
const bridge=async(_file:string,args:string[],options:any={})=>{commands.push({args,cwd:options.cwd??root,noPrompt:options.env?.GIT_TERMINAL_PROMPT==='0'&&options.env?.GIT_ASKPASS==='' });return lowerGit(args,options.cwd??root,{...env,...options.env})}
mock.module('../../../src/utils/execFileNoThrow',()=>({...sourceExec,execFileNoThrow:bridge,execFileNoThrowWithCwd:bridge}))
const state=await import('../../../src/bootstrap/state');state.setCwdState(root);state.setOriginalCwd(root);state.setProjectRoot(root);state.setIsInteractive(false)
const config=await import('../../../src/utils/config');config.enableConfigs()
const worktree=await import('../../../src/utils/worktree')
const attempt=await worktree.createAgentWorktreeIfSupported('agent-a1234567')
if(!attempt.worktree)throw new Error('Source worktree unavailable')
const created=attempt.worktree,creationCommands=commands.splice(0)
utimesSync(created.worktreePath,0,0)
const resumed=await worktree.createAgentWorktreeIfSupported('agent-a1234567'),resumeCommands=commands.splice(0)
const touched=statSync(created.worktreePath).mtimeMs>0
const clean=await worktree.hasWorktreeChanges(created.worktreePath,created.headCommit!)
writeFileSync(join(created.worktreePath,'output.txt'),'agent output')
const dirty=await worktree.hasWorktreeChanges(created.worktreePath,created.headCommit!)
unlinkSync(join(created.worktreePath,'output.txt'))
writeFileSync(join(created.worktreePath,'tracked.txt'),'agent commit');await lowerGit(['add','tracked.txt'],created.worktreePath);await lowerGit(['commit','-m','agent commit'],created.worktreePath)
const committed=await worktree.hasWorktreeChanges(created.worktreePath,created.headCommit!)
const changesCommands=commands.splice(0)
const removed=await worktree.removeAgentWorktree(created.worktreePath,created.worktreeBranch,created.gitRoot),removeCommands=commands.splice(0)
const removedPhysically=!existsSync(created.worktreePath),branchRemoved=(await lowerGit(['show-ref','--verify','refs/heads/'+created.worktreeBranch])).code!==0
mkdirSync(join(home,'plain'));state.setCwdState(join(home,'plain'))
const unavailable=await worktree.createAgentWorktreeIfSupported('invalid/../slug')
const current=worktree.getCurrentWorktreeSession()
process.stdout.write(JSON.stringify({attempt,resumed,touched,clean,dirty,committed,removed,removedPhysically,branchRemoved,creationCommands,resumeCommands,changesCommands,removeCommands,unavailable,current,home,capabilities}))
process.exit(0)
