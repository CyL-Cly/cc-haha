import { mkdirSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import * as crypto from 'node:crypto'
import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Scheduler HTTP')
if (process.env.CC_HAHA_SCHEDULER_HTTP_FIXTURE !== '1') throw new Error('Missing scheduler HTTP fixture')
const absent = [...absentManagedSettingsFixturePaths()]
for (let directory = dirname(input.homeDir);; directory = dirname(directory)) {absent.push(join(directory, '.git'));if (directory === dirname(directory)) break}
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absent })
await import('../../../preload')
spyOn(Date,'now').mockImplementation(()=>input.now)
let random=0
spyOn(crypto,'randomBytes').mockImplementation((count: number)=>Buffer.alloc(count,++random) as any)
process.env.CC_HAHA_LOCAL_INDEX='off'
const { handleScheduledTasksApi }=await import('../../../src/server/api/scheduled-tasks')
mkdirSync(process.env.CLAUDE_CONFIG_DIR!,{recursive:true})
writeFileSync(join(process.env.CLAUDE_CONFIG_DIR!,'scheduled_tasks_log.json'),input.log)
const rows=[]
for (const action of input.actions) {
 const req=new Request('http://fixture.invalid'+action.path,{method:action.method,...(action.body===undefined?{}:{body:action.body,headers:{'Content-Type':'application/json'}})})
 const response=await handleScheduledTasksApi(req,new URL(req.url),new URL(req.url).pathname.split('/').filter(Boolean))
 rows.push({status:response.status,body:await response.json()})
}
process.stdout.write(JSON.stringify({ rows, capabilities }))
