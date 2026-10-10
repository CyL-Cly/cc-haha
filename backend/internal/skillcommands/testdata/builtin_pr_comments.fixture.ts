import {assertDisposableFixtureHome} from '../../../../scripts/quality-gate/go-parity/fixture-guard'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.home,'Builtin PR comments')
if(process.env.CC_HAHA_BUILTIN_PR_COMMENTS_FIXTURE!=='1')throw new Error('Unowned PR comments fixture')
const {default:command}=await import('../../../../src/commands/pr_comments/index')
const rows=[]
for(const row of input.rows){
 process.env.USER_TYPE=row.userType
 rows.push({metadata:command,prompt:await command.getPromptForCommand(row.args,{} as any)})
}
process.stdout.write(JSON.stringify({rows})+'\n')
