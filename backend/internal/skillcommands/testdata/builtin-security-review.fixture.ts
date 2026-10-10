import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import {absentManagedSettingsFixturePaths,installClientFixtureCapabilities} from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
const home=assertDisposableFixtureHome(input.home,'Security review')
if(process.env.CC_HAHA_SECURITY_REVIEW_FIXTURE!=='1')throw new Error('Unowned security review fixture')
const capabilities=await installClientFixtureCapabilities(home,{absentPaths:absentManagedSettingsFixturePaths()})
await import('../../../../preload')
const source=await import('../../../../src/commands/security-review')
const rows=[]
for(const row of input.rows){
 process.env.USER_TYPE=row.userType
 let request:any
 const context:any={getAppState:()=>({future:'preserved',toolPermissionContext:{mode:'default',alwaysAllowRules:{session:['Read(private)']}}})}
 let prompt:any,error:any
 try{
  if(row.userType==='ant')prompt=await source.default.getPromptForCommand(row.args,context)
  else prompt=await source.buildSecurityReviewPrompt(context,{resolveShell:()=>row.shell,execute:async(text,context,command,shell)=>{request={text,command,shell,allowedTools:context.getAppState().toolPermissionContext.alwaysAllowRules.command};return text.replaceAll('!`git status`','PHYSICAL_STATUS_PLACEHOLDER')}})
 }catch(e:any){error=e.message}
 rows.push({metadata:source.default,prompt,request,error})
}
if(capabilities.blocked.length)throw new Error('Blocked original security review:'+capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({rows})+'\n')
