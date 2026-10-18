import {expect,test} from 'bun:test'
import {mkdir,mkdtemp,realpath,rm,writeFile} from 'node:fs/promises'
import {tmpdir} from 'node:os'
import {dirname,join} from 'node:path'
import {fileURLToPath} from 'node:url'
import {createSandboxedTestEnvironment} from '../../pr/test-environment'
const root=fileURLToPath(new URL('../../../',import.meta.url))
test('actual full Source internal remote model aliases and queryHaiku reveal the frozen function boundary',async()=>{
 const home=await realpath(await mkdtemp(join(tmpdir(),'cc-haha-remote-model-boundary-')))
 let requests=0
 const peer=Bun.serve({hostname:'127.0.0.1',port:0,fetch(){requests++;return new Response('Unexpected model HTTP',{status:500})}})
 try{
 await mkdir(join(home,".claude"),{recursive:true});await writeFile(join(home,".claude/.credentials.json"),JSON.stringify({claudeAiOauth:{accessToken:"owned-remote-model-credential",refreshToken:null,expiresAt:9999999999999,subscriptionType:"max",scopes:["user:inference","user:profile"]}}))
 const env=createSandboxedTestEnvironment(home,{CC_HAHA_REMOTE_MODEL_BOUNDARY_SOURCE:'1',CC_HAHA_FIXTURE_TEMP_ROOT:dirname(home),USER_TYPE:'ant',NODE_ENV:'production',ANTHROPIC_API_KEY:'owned-no-quota-model-boundary',ANTHROPIC_BASE_URL:peer.url.origin,ANTHROPIC_DEFAULT_SONNET_MODEL:'owned-sonnet-alias',ANTHROPIC_DEFAULT_HAIKU_MODEL:'claude-haiku-4-5-20251001',CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC:'1',CLAUDE_CODE_DISABLE_FAST_MODE:'1',CLAUDE_CODE_MAX_RETRIES:'0',PATH:'/usr/bin:/bin'})
 const child=Bun.spawn([process.execPath,'--no-env-file',join(root,'scripts/quality-gate/go-parity/native-remote-model-boundary.fixture.ts')],{cwd:home,env,stdin:new Blob([JSON.stringify({home,peer:peer.url.origin})]),stdout:'pipe',stderr:'pipe'})
 const timer=setTimeout(()=>child.kill('SIGKILL'),20000)
 try{
 const[code,stdout,stderr]=await Promise.all([child.exited,new Response(child.stdout).text(),new Response(child.stderr).text()])
 const artifact=join(root,'artifacts/go-migration-20261013/cli-flags');await mkdir(artifact,{recursive:true});await writeFile(join(artifact,'native-remote-model-boundary-source-raw.json'),JSON.stringify({code,stdout,stderr,requests},null,2))
 expect(code,stderr).toBe(0);const result=JSON.parse(stdout);expect(result.blocked).toEqual([]);expect(result.rows.find((r:any)=>r.name==='sonnet').value).toBe('owned-sonnet-alias')
 expect(result.rows.find((r:any)=>r.name==='default').error).toEqual({name:'ReferenceError',message:'getAntModelOverrideConfig is not defined'})
 for(const name of ['custom','maxOutput'])expect(result.rows.find((r:any)=>r.name===name).error).toEqual({name:'ReferenceError',message:'resolveAntModel is not defined'})
 expect(result.rows.find((r:any)=>r.name==='queryHaiku').error).toEqual({name:'ReferenceError',message:'getAntModelOverrideConfig is not defined'})
 expect(result.rows.find((r:any)=>r.name==='generatedTitle').value).toBeNull();expect(requests).toBe(0)
 }finally{clearTimeout(timer);child.kill();await child.exited}
 }finally{peer.stop(true);await rm(home,{recursive:true,force:true})}
},25000)
