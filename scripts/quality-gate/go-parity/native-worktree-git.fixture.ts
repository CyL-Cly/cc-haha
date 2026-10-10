import { mkdirSync, writeFileSync, readFileSync, existsSync, readlinkSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { mock, spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir,'Worktree git')
if(process.env.CC_HAHA_WORKTREE_GIT_FIXTURE!=='1')throw new Error('Missing Worktree git fixture')
const privateSpawn=Bun.spawn.bind(Bun)
const home=input.homeDir,root=join(home,'repo'),commands:any[]=[]
const env={...process.env,HOME:home,USERPROFILE:home,XDG_CONFIG_HOME:home,GIT_CONFIG_NOSYSTEM:'1',GIT_CONFIG_GLOBAL:join(home,'absent-gitconfig'),GIT_AUTHOR_NAME:'fixture',GIT_AUTHOR_EMAIL:'fixture@example.invalid',GIT_COMMITTER_NAME:'fixture',GIT_COMMITTER_EMAIL:'fixture@example.invalid',GIT_AUTHOR_DATE:'2026-10-01T00:00:00Z',GIT_COMMITTER_DATE:'2026-10-01T00:00:00Z',GIT_TERMINAL_PROMPT:'0',GIT_ASKPASS:'',PATH:'/usr/bin:/bin'}
function lowerGit(args:string[],cwd=root,childEnv:any=env){
 if(!cwd.startsWith(home)||args.some(value=>value.includes('https://')||value.includes('ssh://')))throw new Error('Worktree fixture denied external git operation')
 const child=privateSpawn(['/usr/bin/git',...args],{cwd,env:childEnv,stdin:'ignore',stdout:'pipe',stderr:'pipe'})
 return Promise.all([child.exited,new Response(child.stdout).text(),new Response(child.stderr).text()]).then(([code,stdout,stderr])=>({code,stdout,stderr}))
}
const absent=[...absentManagedSettingsFixturePaths()]
for(let directory=dirname(home);;directory=dirname(directory)){absent.push(join(directory,'.git'));if(directory===dirname(directory))break}
const capabilities=await installClientFixtureCapabilities(home,{absentPaths:absent})
await import('../../../preload')
mkdirSync(root,{recursive:true});await lowerGit(['init','-b','main']);writeFileSync(join(root,'tracked.txt'),'private tracked file');writeFileSync(join(root,'.gitignore'),'node_modules\n.claude/settings.local.json\n');await lowerGit(['add','.']);await lowerGit(['commit','-m','initial private fixture'])
mkdirSync(join(root,'.claude'),{recursive:true});writeFileSync(join(root,'.claude/settings.local.json'),JSON.stringify({future:'keep',env:{fake:'value'}}));mkdirSync(join(root,'node_modules'),{recursive:true})
writeFileSync(join(process.env.CLAUDE_CONFIG_DIR!,'settings.json'),JSON.stringify({worktree:{symlinkDirectories:['node_modules']}}))
const sourceGit=await import('../../../src/utils/git')
spyOn(sourceGit,'gitExe').mockImplementation(()=>'/usr/bin/git')
const sourceExec=await import('../../../src/utils/execFileNoThrow')
const bridge=async(_file:string,args:string[],options:any={})=>{commands.push({args,cwd:options.cwd??root,noPrompt:options.env?.GIT_TERMINAL_PROMPT==='0'&&options.env?.GIT_ASKPASS==='' });return lowerGit(args,options.cwd??root,{...env,...options.env})}
mock.module('../../../src/utils/execFileNoThrow',()=>({...sourceExec,execFileNoThrow:bridge,execFileNoThrowWithCwd:bridge}))
const state=await import('../../../src/bootstrap/state')
state.setCwdState(root);state.setOriginalCwd(root);state.setProjectRoot(root);state.setIsInteractive(false)
const config=await import('../../../src/utils/config');config.enableConfigs()
spyOn(Date,'now').mockImplementation(()=>1000)
const worktree=await import('../../../src/utils/worktree')
const session=await worktree.createWorktreeForSession('11111111-1111-4111-8111-111111111111','user/feature')
const creationCommands=commands.splice(0)
const resumed=await worktree.createWorktreeForSession('11111111-1111-4111-8111-111111111111','user/feature')
const resumeCommands=commands.splice(0)
const copied=readFileSync(join(session.worktreePath,'.claude/settings.local.json'),'utf8'),link=readlinkSync(join(session.worktreePath,'node_modules'))
await worktree.cleanupWorktree()
const removed=!existsSync(session.worktreePath),branch=(await lowerGit(['show-ref','--verify','refs/heads/'+session.worktreeBranch])).code!==0
const result={session,resumed,creationCommands,resumeCommands,removeCommands:commands,copied,link,removed,branch,home,capabilities}
process.stdout.write(JSON.stringify(result))
process.exit(0)
