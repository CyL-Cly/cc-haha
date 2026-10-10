import { assertDisposableFixtureHome } from './fixture-guard'
import {mkdirSync,readFileSync} from 'node:fs'
import {join} from 'node:path'
assertDisposableFixtureHome(process.env.HOME,'Native Slack file failure')
if(process.env.CC_HAHA_SLACK_ROUTE_FAILURE_FIXTURE!=='1')throw new Error('Unowned Slack failure')
Bun.plugin({name:'actual-slack-failed-write-export',setup(builder){builder.onLoad({filter:/\/server\/api\/adapters\.ts$/},args=>({loader:'ts',contents:readFileSync(args.path,'utf8')+'\nexport {handleSlackAdaptersApi}\n'}))}})
const {handleSlackAdaptersApi}:any=await import('../../../src/server/api/adapters')
console.error=()=>{}
mkdirSync(join(process.env.CLAUDE_CONFIG_DIR!,'adapters.json'),{recursive:true})
try{await handleSlackAdaptersApi(new Request('http://private.test/unbind',{method:'POST'}),['unbind']);throw new Error('write failure unexpectedly passed')}catch(e:any){process.stdout.write(JSON.stringify({status:e.statusCode,code:e.code,message:e.message}))}
