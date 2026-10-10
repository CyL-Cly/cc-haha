import { assertDisposableFixtureHome } from './fixture-guard'
import { mkdirSync, readFileSync, writeFileSync, appendFileSync, unlinkSync, rmSync, utimesSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { parse } from 'acorn'
import { Database } from 'bun:sqlite'

const home = assertDisposableFixtureHome(process.env.HOME, 'Local index Source comparison')
if (process.env.CC_HAHA_LOCAL_INDEX_FIXTURE !== '1') throw new Error('Missing local index fixture marker')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const config = join(home, '.claude')
mkdirSync(config, { recursive: true })
const collect = (relative: string) => {
    const source = readFileSync(new URL(relative, import.meta.url), 'utf8')
    const executable = new Bun.Transpiler({ loader: 'ts' }).transformSync(source)
    const ast = parse(executable, { ecmaVersion: 'latest', sourceType: 'module' }) as any
    const sql: Record<string, string> = {}
    for (const statement of ast.body) {
      if (statement.type !== 'VariableDeclaration') continue
      for (const declaration of statement.declarations) {
        if (!/^SCHEMA_V\d+$/.test(declaration.id?.name ?? '')) continue
        if (declaration.init?.type !== 'TemplateLiteral' || declaration.init.expressions.length) throw new Error('Schema must be a static declaration')
        sql[declaration.id.name] = declaration.init.quasis[0].value.cooked
      }
    }
    return sql
}
if (input.action === 'recovery') {
  const module = await import('../../../src/server/services/localIndex/recovery')
  const databasePath = join(config, 'cc-haha', 'db', 'index-v1.sqlite')
  mkdirSync(dirname(databasePath), { recursive: true })
  const observations: any[] = []
  const { readdirSync } = await import('node:fs')
  const { rename } = await import('node:fs/promises')
  const tree = (path: string): any => Object.fromEntries(readdirSync(path, { withFileTypes: true }).map(entry => [entry.name.replace(/[0-9a-f]{8}$/i, '$TOKEN'), entry.isDirectory() ? tree(join(path,entry.name)) : readFileSync(join(path,entry.name),'utf8')]))
  for (const [n,row] of input.operations.entries()) {
    for (const [suffix,data] of Object.entries(row.files ?? {})) writeFileSync(databasePath + suffix, String(data))
    let calls = 0
    try {
      const result = await module.backupLocalIndexDatabaseFamily({ scope: config, databasePath, reason: row.reason ?? 'MANUAL_REBUILD', now: () => n * 1000, ...(row.failAt ? {renameFile: async (a: string,b: string) => {if (++calls === row.failAt) throw new Error('Injected rename failure'); await rename(a,b)}} : {}) })
      observations.push({ result: {...result, backupPath: result.backupPath.replaceAll(home,'$HOME').replace(/[0-9a-f]{8}$/i,'$TOKEN')}, tree: tree(dirname(databasePath)) })
    } catch (error: any) { observations.push({error: error.message,code: error.code ?? null,tree: tree(dirname(databasePath))}) }
  }
  process.stdout.write(JSON.stringify({observations}))
} else if (input.action === 'primaryCoordinator') {
  await import('../../../preload')
  const { createLocalIndexCoordinator } = await import('../../../src/server/services/localIndex/coordinator')
  const { createSessionProjector } = await import('../../../src/server/services/localIndex/sessionProjector')
  const { readSessionEntriesByLocator } = await import('../../../src/server/services/localIndex/sessionEntries')
  const { stat } = await import('node:fs/promises')
  let onBatch: any
  let mode = input.mode ?? 'on'
  // Only timing/event delivery is controlled. Discovery, projection, SQLite,
  // canonical file reads, migrations and backup are the actual Source code.
  const service = createLocalIndexCoordinator({resolveMode: () => ({mode,warningCode:null}), resolveScope: () => config, resolveDatabasePath: () => join(config,'cc-haha','db','index-v1.sqlite'), now: () => 42, storageLimitBytes: input.storageLimit, schedule: () => {}, createWatcher: (options: any) => {onBatch = options.onBatch; return {start:async()=>{},stop:async()=>{},queueFullSweep(){},queueTranscriptPath(){}} as any}, createProjector: (options: any) => createSessionProjector({...options, now:()=>42, sourceMetadataStat: async (path: string) => ({...await stat(path),birthtime:new Date('2026-10-01T00:00:00Z'),mtime:new Date('2026-10-06T00:00:00Z')})})})
  const observations: any[] = []
  const reduce = (value: any): any => {
    if (Array.isArray(value)) return value.map(reduce)
    if (value && typeof value === 'object') return Object.fromEntries(Object.entries(value).filter(([k]) => !['databaseBytes','walBytes','fingerprint','mtimeMs','fileIdentity','createdAt','modifiedAt','modifiedAtMs'].includes(k)).map(([k,v])=>[k,reduce(v)]))
    return typeof value === 'string' ? value.replaceAll(home,'$HOME') : value
  }
  try {
    for (const row of input.operations) {
      const path = row.path ? join(config,row.path) : ''
      if (row.write !== undefined) {mkdirSync(dirname(path),{recursive:true});writeFileSync(path,row.write)}
      if (row.append !== undefined) appendFileSync(path,row.append)
      if (row.remove) rmSync(path,{recursive:true,force:true})
      if (row.method === 'start') {await service.start();if(mode !== 'off') await onBatch({fullSweep:true,paths:[]})}
      if (row.method === 'sweep') await onBatch({fullSweep:true,paths:[]})
      if (row.method === 'notify') await onBatch({fullSweep:false,paths:[path]})
      if (row.method === 'stop') await service.stop()
      if (row.method === 'rebuild') {await service.rebuild();if(mode !== 'off') await onBatch({fullSweep:true,paths:[]})}
      let result: any = service.getPublicStatus()
      if (row.method === 'list') result = service.listSessions(row.options)
      if (row.method === 'get') result = service.getSession(row.id)
      if (row.method === 'find') result = service.findSessionFiles(row.id)
      if (row.method === 'metadata') result = service.searchSessionMetadata(row.query,row.options)
      if (row.method === 'title') result = service.updateSessionTitle(row.id,row.title)
      if (row.method === 'locators') result = service.getSessionEntryLocators(path,row.types)
      if (row.method === 'readEntries') {const page=service.getSessionEntryLocators(path,row.types);result=page?await readSessionEntriesByLocator({transcriptPath:path,projectsRoot:join(config,'projects'),expectedProjectDir:row.project,page}):null}
      if (row.method === 'stats') result=service.getActivityStats(row.range ?? 'all',new Date('2026-10-07T00:00:00Z'))
      if (row.method === 'readiness') result={sessions:service.isSessionScopeReady(),activity:service.isActivityScopeReady(),failures:service.getSourceScopedFailurePaths()}
      observations.push(reduce(result))
    }
    process.stdout.write(JSON.stringify({observations}))
  } finally {await service.stop()}
} else if (input.action === 'sessionConsumer') {
  await import('../../../preload')
  const {createLocalIndexCoordinator} = await import('../../../src/server/services/localIndex/coordinator')
  const {SessionService} = await import('../../../src/server/services/sessionService')
  let clock=1791331200000
  let onBatch:any
  const primary=createLocalIndexCoordinator({resolveMode:()=>({mode:input.mode??'on',warningCode:null}),resolveScope:()=>config,resolveDatabasePath:()=>join(config,'cc-haha','db','index-v1.sqlite'),now:()=>clock,schedule:()=>{},createWatcher:(options:any)=>{onBatch=options.onBatch;return {start:async()=>{},stop:async()=>{},queueFullSweep(){},queueTranscriptPath(){}} as any}})
  const session=new SessionService(primary,{now:()=>clock})
  const projectHistoryCursors=new Map<string,string>()
  const projectHistorySnapshotIds=new Map<string,number>()
  const workspace=join(home,'workspace');mkdirSync(workspace,{recursive:true})
  const expand=(value:string)=>value.replaceAll('$HOME',home).replaceAll('$CONFIG',config).replaceAll('$WORKSPACE',workspace)
  const observations:any[]=[]
  const reduce=(value:any)=>JSON.parse(JSON.stringify(value).replaceAll(home,'$HOME'))
  try {
    for (const row of input.operations) {
      clock+=1000
      const path=row.path?expand(row.path):''
      if (row.mkdir) mkdirSync(expand(row.mkdir),{recursive:true})
      if (row.write!==undefined){mkdirSync(dirname(path),{recursive:true});writeFileSync(path,expand(row.write));utimesSync(path, row.mtime??1791244800,row.mtime??1791244800)}
      if (row.append!==undefined){appendFileSync(path,expand(row.append));utimesSync(path,row.mtime??1791331200,row.mtime??1791331200)}
      if (row.remove) rmSync(path,{recursive:true,force:true})
      if (row.mutation) (session as any).invalidateSessionListCache()
      if (row.patchTitle) (session as any).syncIndexedSessionTitle(row.id,row.patchTitle)
      if (row.method==='start'){await primary.start();if(onBatch)await onBatch({fullSweep:true,paths:[]})}
      if (row.method==='sweep' && onBatch) await onBatch({fullSweep:true,paths:[]})
      if (row.method==='notify' && onBatch) await onBatch({fullSweep:false,paths:[path]})
      if (row.method==='stop') await primary.stop()
      if (row.method==='rebuild'){await primary.rebuild();if(onBatch)await onBatch({fullSweep:true,paths:[]})}
      if (row.sql) {
        const {openLocalIndexDatabase}=await import('../../../src/server/services/localIndex/database')
        const database=openLocalIndexDatabase({path:join(config,'cc-haha','db','index-v1.sqlite')})
        try {database.write(op=>op.run(row.sql.query,...(row.sql.bindings??[]).map((value:any)=>typeof value==='string'?expand(value):value)))} finally {database.close()}
      }
      if (row.method==='list') observations.push({result:reduce(await session.listSessions(row.options?JSON.parse(expand(JSON.stringify(row.options))):undefined))})
      if (row.method==='summary') observations.push({result:reduce(await session.getSessionSummary(row.id))})
      if (row.method==='find') observations.push({result:reduce(await session.findSessionFile(row.id))})
      if (row.method==='sidebar') observations.push({result:reduce(await session.listProjectPreviews(row.limit??6))})
      if (row.method==='projectHistory') {
        const options=JSON.parse(expand(JSON.stringify(row.options)))
        if (options.cursor?.startsWith('$')) options.cursor=projectHistoryCursors.get(options.cursor.slice(1))
        try {
          const result=await session.listProjectHistory(options)
          if(row.save && result.nextCursor) projectHistoryCursors.set(row.save,result.nextCursor)
          if(result.nextCursor) {
            const value=JSON.parse(Buffer.from(result.nextCursor,'base64url').toString())
            if(!projectHistorySnapshotIds.has(value.snapshot)) projectHistorySnapshotIds.set(value.snapshot,projectHistorySnapshotIds.size+1)
            result.nextCursor=`${projectHistorySnapshotIds.get(value.snapshot)}:${value.offset}`
          }
          observations.push({result:reduce(result)})
        } catch(error) {
          const e=error as any
          observations.push({error:{status:e.statusCode,code:e.code,message:e.message}})
        }
      }
    }
    process.stdout.write(JSON.stringify({observations}))
  } finally {await primary.stop()}
} else if (input.action === 'schema') {
  process.stdout.write(JSON.stringify({ primary: collect('../../../src/server/services/localIndex/migrations.ts'), search: collect('../../../src/server/services/localIndex/searchContentMigrations.ts') }))
} else if (input.action === 'database') {
  await import('../../../preload')
  const path = join(home, input.name ?? 'fixture.sqlite')
  if (input.seedVersion !== undefined) {
    const original = collect(input.kind === 'primary' ? '../../../src/server/services/localIndex/migrations.ts' : '../../../src/server/services/localIndex/searchContentMigrations.ts')
    const seed = new Database(path)
    for (let v = 1; v <= input.seedVersion && original[`SCHEMA_V${v}`]; v++) seed.exec(original[`SCHEMA_V${v}`])
    seed.exec(`PRAGMA user_version = ${input.seedVersion}`)
    for (const statement of input.seed ?? []) seed.exec(statement)
    seed.close()
  }
  let db: any
  try {
    if (input.kind === 'primary') db = (await import('../../../src/server/services/localIndex/database')).openLocalIndexDatabase({ path })
    else db = (await import('../../../src/server/services/localIndex/searchContentDatabase')).openSearchContentDatabase({ path })
    const observations: any[] = []
    for (const action of input.operations ?? []) {
      try {
        const run = (op: any) => {
          const results = (action.statements ?? []).map((statement: any) => op[statement.method ?? 'all'](statement.sql, ...(statement.bindings ?? [])))
          if (action.throw) throw new Error(action.throw)
          return results
        }
        observations.push({ results: db[action.mode ?? 'read'](run) })
      } catch (error: any) { observations.push({ error: error.message, code: error.code ?? null }) }
    }
    const schema = db.read((op: any) => op.all("SELECT type,name,tbl_name,sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY type,name"))
    process.stdout.write(JSON.stringify({ observations, schema, version: db.read((op: any) => op.get('PRAGMA user_version')), journal: db.read((op: any) => op.get('PRAGMA journal_mode')) }))
  } catch (error: any) { process.stdout.write(JSON.stringify({ error: error.message, code: error.code ?? null })) }
  finally { db?.close() }
} else if (input.action === 'content') {
  await import('../../../preload')
  const { openSearchContentDatabase } = await import('../../../src/server/services/localIndex/searchContentDatabase')
  const { createSearchContentIndex, normalizeSearchContent } = await import('../../../src/server/services/localIndex/searchContentIndex')
  const db = openSearchContentDatabase({ path: join(home, input.name ?? 'content.sqlite') })
  const index = createSearchContentIndex(db, { scope: 'fixture', now: () => 42 })
  const observations: any[] = []
  try {
    for (const row of input.operations) {
      try {
        const documents = (row.documents ?? []).map((document: any) => ({ ...document, normalizedBody: normalizeSearchContent(document.body) }))
        let result: any
        if (row.method === 'replace') result = index.replaceSource(row.source, documents)
        else if (row.method === 'append') result = index.appendSource(row.source, documents)
        else if (row.method === 'delete') result = index.deleteSource(row.path)
        else if (row.method === 'get') result = index.getSource(row.path)
        else if (row.method === 'list') result = index.listSources()
        else if (row.method === 'count') result = index.countSources()
        else if (row.method === 'readiness') result = index.getReadiness()
        else if (row.method === 'setReadiness') result = index.setReadiness(row.readiness)
        else if (row.method === 'query') result = index.query(row.query, row.options)
        else if (row.method === 'suggest') result = index.querySessionSuggestions(row.query, row.limit)
        else if (row.method === 'normalize') result = normalizeSearchContent(row.value)
        else throw new Error('Unsupported content fixture operation')
        observations.push({ result: result === undefined ? '$undefined' : result })
      } catch (error: any) { observations.push({ error: error.message }) }
    }
    process.stdout.write(JSON.stringify({ observations }))
  } finally { db.close() }
} else if (input.action === 'sessionProject') {
  await import('../../../preload')
  const { openLocalIndexDatabase } = await import('../../../src/server/services/localIndex/database')
  const { createSessionIndex } = await import('../../../src/server/services/localIndex/sessionIndex')
  const { createSessionProjector } = await import('../../../src/server/services/localIndex/sessionProjector')
  const { stat } = await import('node:fs/promises')
  const database = openLocalIndexDatabase({ path: join(home, 'session-project.sqlite') })
  const index = createSessionIndex(database)
  const controller = new AbortController()
  const projector = createSessionProjector({ database, index, scope: 'fixture', now: () => 42, parserVersion: input.parserVersion, recordStreamingThresholdBytes: input.threshold, signal: controller.signal, ...(input.fixedMetadata ? { sourceMetadataStat: async (path: string) => ({ ...await stat(path), birthtime: new Date('2026-10-01T00:00:00Z'), mtime: new Date('2026-10-06T00:00:00Z') }) } : {}) })
  const observations: any[] = []
  try {
    for (const row of input.operations) {
      const path = join(home, row.path ?? 'source.jsonl')
      if (row.write !== undefined) writeFileSync(path, row.write)
      if (row.append !== undefined) appendFileSync(path, row.append)
      if (row.remove) rmSync(path, { force: true })
      if (row.aborted) controller.abort()
      const candidate = { path, sessionId: 'session', projectPath: 'project', fallbackCreatedAt: '2026-10-01T00:00:00Z', fallbackModifiedAt: '2026-10-06T00:00:00Z', fallbackWorkDir: '/fallback', modifiedAtMs: 42 }
      try {
        const result = row.activity ? await projector.projectActivitySource(candidate) : row.delete ? await projector.deleteSource(path) : await projector.projectSource(candidate)
        const tables = database.read(op => Object.fromEntries(['source_files','sessions','session_entries','backfill_state','activity_sources','activity_sessions','activity_daily','activity_daily_models','activity_daily_tools','activity_daily_skills'].map(table => [table, op.all(`SELECT * FROM ${table} ORDER BY 1,2`)])))
        observations.push({ result, tables })
      } catch (error: any) { observations.push({ error: error.message, code: error.code ?? null }) }
    }
    const reduce = (value: any): any => {
      if (Array.isArray(value)) return value.map(reduce)
      if (value && typeof value === 'object') return Object.fromEntries(Object.entries(value).filter(([k]) => !['fingerprint','prefix_hash','file_identity','mtime_ms','sourceFingerprint'].includes(k)).map(([k,v]) => [k,reduce(v)]))
      return typeof value === 'string' ? value.replaceAll(home, '$HOME') : value
    }
    process.stdout.write(JSON.stringify({ observations: reduce(observations) }))
  } finally { database.close() }
} else if (input.action === 'activityIndex') {
  await import('../../../preload')
  const { openLocalIndexDatabase } = await import('../../../src/server/services/localIndex/database')
  const { createActivityIndex, writeActivityProjection, writeActivityBackfillState } = await import('../../../src/server/services/localIndex/activityIndex')
  const database = openLocalIndexDatabase({ path: join(home, 'activity.sqlite') })
  const index = createActivityIndex(database, { shotStatsEnabled: input.shots ?? false })
  try {
    const observations: any[] = []
    for (const row of input.operations) {
      let result: any
      try {
        if (row.method === 'write') result = database.transaction(op => writeActivityProjection(op, row.source, row.activity))
        else if (row.method === 'backfillWrite') result = database.transaction(op => writeActivityBackfillState(op, row.state))
        else if (row.method === 'source') result = index.getActivitySource(row.path)
        else if (row.method === 'sources') result = index.listActivitySources()
        else if (row.method === 'count') result = index.countActivitySources()
        else if (row.method === 'backfill') result = index.getActivityBackfillState(row.scope)
        else if (row.method === 'aggregate') result = index.aggregateActivity(row.range, new Date(row.now))
        else if (row.method === 'snapshot') result = database.read(op => Object.fromEntries(['activity_sources','activity_sessions','activity_daily','activity_daily_models','activity_daily_tools','activity_daily_skills','activity_backfill_state'].map(table => [table, op.all(`SELECT * FROM ${table} ORDER BY 1,2`)])))
        else throw new Error('Unsupported activity index fixture operation')
        observations.push({ result: result === undefined ? '$undefined' : result })
      } catch (error: any) { observations.push({ error: error.message }) }
    }
    process.stdout.write(JSON.stringify({ observations }))
  } finally { database.close() }
} else if (input.action === 'sessionIndex') {
  await import('../../../preload')
  const { openLocalIndexDatabase } = await import('../../../src/server/services/localIndex/database')
  const { createSessionIndex } = await import('../../../src/server/services/localIndex/sessionIndex')
  const database = openLocalIndexDatabase({ path: join(home, 'sessions.sqlite') })
  const index = createSessionIndex(database)
  try {
    database.transaction(op => { for (const sql of input.seed ?? []) op.exec(sql) })
    const observations: any[] = []
    for (const row of input.operations) {
      let result: any
      try {
        if (row.method === 'list') result = index.listSessions(row.options)
        else if (row.method === 'get') result = index.getSession(row.id)
        else if (row.method === 'suggest') result = index.getSessionSuggestionMetadata!(row.ids)
        else if (row.method === 'search') result = index.searchSessionMetadata!(row.query, row.options)
        else if (row.method === 'find') result = index.findSessionFiles(row.id)
        else if (row.method === 'updateTitle') result = index.updateSessionTitle!(row.id, row.title)
        else if (row.method === 'candidates') result = index.findSearchCandidates!(row.options)
        else if (row.method === 'source') result = index.getSource(row.path)
        else if (row.method === 'sources') result = index.listSources()
        else if (row.method === 'count') result = index.countSources()
        else if (row.method === 'backfill') result = index.getBackfillState(row.scope)
        else if (row.method === 'locators') result = index.getSessionEntryLocators(row.path, row.types)
        else if (row.method === 'projection') result = index.getProjectionSeed(row.path)
        else throw new Error('Unsupported session index fixture operation')
        observations.push({ result })
      } catch (error: any) { observations.push({ error: error.message }) }
    }
    process.stdout.write(JSON.stringify({ observations }))
  } finally { database.close() }
} else if (input.action === 'reduce') {
  await import('../../../preload')
  const { reduceTranscriptWithLocators } = await import('../../../src/server/services/localIndex/transcriptReducer')
  let seed = input.seed
  const observations: any[] = []
  for (const row of input.operations) {
    try {
      if (row.seed !== undefined) seed = row.seed
      const result = reduceTranscriptWithLocators(row.chunks, seed, row.options)
      seed = result.projection
      observations.push({ result })
    } catch (error: any) { observations.push({ error: error.message, code: error.code ?? null }) }
  }
  process.stdout.write(JSON.stringify({ observations }))
} else if (input.action === 'coordinator') {
  await import('../../../preload')
  const { createSearchContentCoordinator } = await import('../../../src/server/services/localIndex/searchContentCoordinator')
  const { createReconciliationWatcher } = await import('../../../src/server/services/localIndex/reconciliationWatcher')
  let watcher: any
  const service = createSearchContentCoordinator({ resolveScope: () => config, resolveDatabasePath: () => join(config, 'cc-haha', 'db', 'search-index-v1.sqlite'), storageLimitBytes: input.storageLimit, createWatcher: (options: any) => {
    watcher = createReconciliationWatcher({ ...options, listWatchDirectories: async () => [], debounceMs: 5, maxWaitMs: 20, safetySweepMs: 60_000 })
    return watcher
  } })
  const result: any[] = []
  const reduce = (value: any): any => {
    if (Array.isArray(value)) return value.map(reduce)
    if (value && typeof value === 'object') return Object.fromEntries(Object.entries(value).filter(([key]) => !['databaseBytes','walBytes','sourceFingerprint','sourceMtimeMs','sourceFileIdentity','modifiedAtMs'].includes(key)).map(([key, v]) => [key, reduce(v)]))
    return typeof value === 'string' ? value.replaceAll(home, '$HOME') : value
  }
  try {
    for (const row of input.operations) {
      const path = row.path ? join(config, row.path) : ''
      if (row.write !== undefined) { mkdirSync(dirname(path), { recursive: true }); writeFileSync(path, row.write); utimesSync(path, 1791244800, 1791244800) }
      if (row.append !== undefined) appendFileSync(path, row.append)
      if (row.remove) rmSync(path, { recursive: true, force: true })
      if (row.method === 'start') await service.start()
      else if (row.method === 'stop') await service.stop()
      else if (row.method === 'notify') watcher.queueTranscriptPath(path)
      else if (row.method === 'sweep') watcher.queueFullSweep()
      if (row.state) {
        const deadline = Date.now() + 5000
        while (service.getStatus().state !== row.state && Date.now() < deadline) await Bun.sleep(5)
        if (service.getStatus().state !== row.state) throw new Error(`Index state did not settle: ${JSON.stringify(service.getStatus())}`)
      }
      if (row.method === 'query') result.push(reduce(service.search(row.query, row.options)))
      else if (row.method === 'suggest') result.push(reduce(service.suggestSessions(row.query, row.options)))
      else result.push(reduce(service.getStatus()))
    }
    process.stdout.write(JSON.stringify({ observations: result }))
  } finally { await service.stop() }
} else if (input.action === 'segments') {
  await import('../../../preload')
  const { extractSearchableSegments } = await import('../../../src/server/services/localIndex/searchContentProjector')
  process.stdout.write(JSON.stringify({ observations: input.entries.map((entry: any) => extractSearchableSegments(entry)) }))
} else if (input.action === 'project') {
  await import('../../../preload')
  const { openSearchContentDatabase } = await import('../../../src/server/services/localIndex/searchContentDatabase')
  const { createSearchContentIndex } = await import('../../../src/server/services/localIndex/searchContentIndex')
  const { createSearchContentProjector } = await import('../../../src/server/services/localIndex/searchContentProjector')
  const database = openSearchContentDatabase({ path: join(home, 'project.sqlite') })
  const index = createSearchContentIndex(database, { scope: 'fixture', now: () => 42 })
  const projector = createSearchContentProjector({ database, index, parserVersion: input.parserVersion, maxJsonlLineBytes: input.maxLineBytes, now: () => 42 })
  try {
    const candidate = { ...input.candidate, path: join(home, input.candidate.path), ownerTranscriptPath: join(home, input.candidate.ownerTranscriptPath) }
    const result = await projector.projectSource(candidate)
    const sources = index.listSources()
    const documents = database.read(op => op.all('SELECT source_path,jsonl_line,byte_start,byte_length,segment_index,role,message_id,timestamp,body,normalized_body FROM search_documents ORDER BY source_path,jsonl_line,segment_index'))
    process.stdout.write(JSON.stringify({ result, sources, documents }))
  } finally { database.close() }
} else if (input.action === 'fingerprint') {
  const module = await import('../../../src/server/services/localIndex/sourceFingerprint')
  const path = join(home, 'source.jsonl')
  const observations: any[] = []
  let previous: any = input.previous
  for (const row of input.operations) {
    if (row.write !== undefined) writeFileSync(path, row.write)
    if (row.append !== undefined) appendFileSync(path, row.append)
    if (row.remove) unlinkSync(path)
    try {
      if (row.method === 'capture') {
        previous = await module.captureSourceFingerprint({ path, indexedBytes: row.indexedBytes, parserVersion: row.parserVersion })
        observations.push({ fingerprint: previous, serialized: module.serializeSourceFingerprint(previous) })
      } else if (row.method === 'detect') observations.push(await module.detectSourceChange({ path, previous: row.previous ?? previous, parserVersion: row.parserVersion }))
      else if (row.method === 'verify') observations.push(await module.verifySourceFingerprint({ path, expected: row.previous ?? previous }))
      else if (row.method === 'deserialize') observations.push({ fingerprint: module.deserializeSourceFingerprint(row.value) })
      else throw new Error('Unsupported fingerprint fixture operation')
    } catch (error: any) { observations.push({ error: error.message }) }
  }
  process.stdout.write(JSON.stringify({ observations }))
} else throw new Error('Unsupported local index fixture action')
