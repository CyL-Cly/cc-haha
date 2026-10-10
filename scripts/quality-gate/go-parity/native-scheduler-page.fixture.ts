import { mkdirSync, readFileSync } from 'node:fs'
import { Database } from 'bun:sqlite'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Scheduler page')
if (process.env.CC_HAHA_SCHEDULER_PAGE_FIXTURE !== '1') throw new Error('Missing scheduler page fixture')
const absent = [...absentManagedSettingsFixturePaths()]
for (let directory = dirname(input.homeDir);; directory = dirname(directory)) {absent.push(join(directory, '.git'));if (directory === dirname(directory)) break}
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absent })
await import('../../../preload')
const { paginateScheduledRunRecords, openScheduledRunIndex } = await import('../../../src/server/services/localIndex/scheduledRunIndex')
const rows = input.rows.map((row: any) => paginateScheduledRunRecords(row.runs, row.options, row.revision))
const index = openScheduledRunIndex()
const indexRows=[]
try {
 for (const item of input.indexRows ?? []) {
  try {
   if (item.replace) index.replaceAll(item.replace)
   indexRows.push({ value: item.options ? index.list(item.options) : index.getStatus() })
  } catch (error) { indexRows.push({ error: (error as Error).message }) }
 }
} finally {index.close()}
const migrations=[]
for (const version of input.migrations ?? []) {
 const scope=join(input.homeDir, 'migration-'+version), path=join(scope, 'cc-haha/db/scheduled-runs-v1.sqlite')
 mkdirSync(dirname(path),{recursive:true})
 const old=new Database(path)
 old.exec("CREATE TABLE scheduled_run_source(singleton INTEGER PRIMARY KEY);CREATE TABLE scheduled_runs(run_id TEXT PRIMARY KEY,run_json TEXT);INSERT INTO scheduled_runs VALUES('old','private-legacy-body');PRAGMA user_version="+version)
 old.close(true)
 const before=readFileSync(path)
 try {
  const target=openScheduledRunIndex({path,scope})
  const status=target.getStatus();target.close()
  const db=new Database(path)
  const columns=db.query('PRAGMA table_info(scheduled_runs)').all().map((row:any)=>row.name),newVersion=(db.query('PRAGMA user_version').get() as any).user_version
  db.close(true)
  migrations.push({status,columns,version:newVersion,hasPrivateBody:readFileSync(path).includes(Buffer.from('private-legacy-body'))})
 } catch(error){migrations.push({error:{message:(error as Error).message,code:(error as any).code},unchanged:before.equals(readFileSync(path))})}
}
process.stdout.write(JSON.stringify({ rows, indexRows, migrations, capabilities }))
