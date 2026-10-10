import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.home,'Builtin Agent command')
if(process.env.CC_HAHA_BUILTIN_AGENT_FIXTURE!=='1')throw new Error('Unowned Agent command fixture')
const {default:agent,parseAgentCommandArgs}=await import('../../../../src/commands/agent')
const rows=[]
for(const args of input.arguments){
 try{rows.push({parsed:parseAgentCommandArgs(args),prompt:await agent.getPromptForCommand(args,{} as any)})}
 catch(error:any){rows.push({parsed:parseAgentCommandArgs(args),error:error.message})}
}
process.stdout.write(JSON.stringify({metadata:agent,rows})+'\n')
