import { mkdirSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Scheduler read model')
if (process.env.CC_HAHA_SCHEDULER_READMODEL_FIXTURE !== '1') throw new Error('Missing scheduler read model fixture')
const absent = [...absentManagedSettingsFixturePaths()]
for (let directory = dirname(input.homeDir);; directory = dirname(directory)) {absent.push(join(directory, '.git'));if (directory === dirname(directory)) break}
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absent })
await import('../../../preload')
const model=await import('../../../src/server/services/localIndex/scheduledRunReadModel')
const {CronScheduler}=await import('../../../src/server/services/cronScheduler')
const scheduler=new CronScheduler()
mkdirSync(process.env.CLAUDE_CONFIG_DIR!,{recursive:true})
const source=join(process.env.CLAUDE_CONFIG_DIR!,'scheduled_tasks_log.json')
const rows=[]
for(const action of input.actions){
 if(action.write!==undefined)writeFileSync(source,action.write)
 if(action.mode!==undefined)process.env.CC_HAHA_LOCAL_INDEX=action.mode
 let value
 if(action.method==='full')value=await scheduler.getRecentRuns(action.limit)
 else if(action.method==='detail')value=await scheduler.getRunDetail(action.id)
 else value=await scheduler.getRunsPage(action.options??{})
 rows.push({value,metrics:model.getScheduledRunReadModelDiagnosticsForTests()})
}
await model.resetScheduledRunReadModelForTests()
process.stdout.write(JSON.stringify({rows,capabilities}))
