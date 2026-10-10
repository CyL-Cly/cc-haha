import { assertDisposableFixtureHome } from './fixture-guard'
import { mkdirSync,writeFileSync,readFileSync,statSync } from 'node:fs'
import { join } from 'node:path'
assertDisposableFixtureHome(process.env.HOME,'Native Slack config routes')
if(process.env.CC_HAHA_SLACK_CONFIG_ROUTES_FIXTURE!=='1')throw new Error('Unowned Slack routes')
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
Bun.plugin({name:'actual-source-slack-route-export',setup(builder){builder.onLoad({filter:/\/server\/api\/adapters\.ts$/},args=>({loader:'ts',contents:readFileSync(args.path,'utf8')+'\nexport { handleSlackAdaptersApi }\n'}))}})
const {handleSlackAdaptersApi}:any=await import('../../../src/server/api/adapters')
mkdirSync(process.env.CLAUDE_CONFIG_DIR!,{recursive:true})
const file=join(process.env.CLAUDE_CONFIG_DIR!,'adapters.json'),rows=[]
for(const action of input.actions){writeFileSync(file,JSON.stringify(input.seed),{mode:0o600});try{const r=await handleSlackAdaptersApi(new Request('http://private.test/api/adapters/slack/'+action.tail.join('/'),{method:action.method}),action.tail);rows.push({status:r.status,body:await r.json(),raw:JSON.parse(readFileSync(file,'utf8')),mode:statSync(file).mode&0o777})}catch(e:any){rows.push({status:e.statusCode,code:e.code,message:e.message})}}
process.stdout.write(JSON.stringify(rows))
