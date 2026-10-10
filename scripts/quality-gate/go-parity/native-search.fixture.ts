import { assertDisposableFixtureHome } from './fixture-guard'
import { mkdirSync, writeFileSync, utimesSync, realpathSync, rmSync, appendFileSync } from 'node:fs'
import { join, dirname } from 'node:path'

const home = assertDisposableFixtureHome(process.env.HOME, 'Search Source comparison')
if (process.env.CC_HAHA_SEARCH_FIXTURE !== '1') throw new Error('Missing search fixture marker')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const config = join(home, '.claude')
const workspace = join(home, 'workspace')
mkdirSync(config, { recursive: true })
mkdirSync(workspace, { recursive: true })
process.chdir(workspace)
process.env.USE_BUILTIN_RIPGREP = '0'
if (!input.rg && !input.grep) process.env.PATH = ''
await import('../../../preload')
const expand = (s: string) => s.replaceAll('$HOME', home).replaceAll('$CONFIG', config).replaceAll('$WORKSPACE', workspace)
for (const file of input.files ?? []) {
  const path = expand(file.path)
  if (!path.startsWith(home + '/')) throw new Error('Unsafe search fixture path')
  mkdirSync(dirname(path), { recursive: true })
  writeFileSync(path, file.base64 ? Buffer.from(file.base64, 'base64') : file.content)
  const when = file.mtime ?? 1791244800
  utimesSync(path, when, when)
}
const { SearchService } = await import('../../../src/server/services/searchService')
const { createSearchContentCoordinator } = await import('../../../src/server/services/localIndex/searchContentCoordinator')
const coordinator = createSearchContentCoordinator({ resolveScope: () => config, resolveDatabasePath: () => join(config, 'cc-haha', 'db', 'search-index-v1.sqlite') })
let primary: any
let session: any
let onBatch: any
if (input.primary) {
  const {createLocalIndexCoordinator} = await import('../../../src/server/services/localIndex/coordinator')
  const {SessionService} = await import('../../../src/server/services/sessionService')
  primary=createLocalIndexCoordinator({resolveMode:()=>({mode:input.mode??'on',warningCode:null}),resolveScope:()=>config,resolveDatabasePath:()=>join(config,'cc-haha','db','index-v1.sqlite'),schedule:()=>{},createWatcher:(options:any)=>{onBatch=options.onBatch;return {start:async()=>{},stop:async()=>{},queueFullSweep(){},queueTranscriptPath(){}} as any}})
  session=new SessionService(primary)
  await primary.start()
  if ((input.mode??'on')!=='off') await onBatch({fullSweep:true,paths:[]})
}
const service = new SearchService({ resolveRipgrepCommand: () => ({ rgPath: input.rg ?? undefined, rgArgs: input.rgArgs ?? ['--no-config', '--threads', '1'] }), searchIndexedContent: (q, o) => coordinator.search(q, o), suggestIndexedSessions: (q, o) => coordinator.suggestSessions(q, o), ...(session?{getMetadataForPaths:(paths:any)=>session.getIndexedSessionSearchMetadata(paths),getCandidatesForFilters:(filters:any)=>session.getIndexedSessionSearchCandidates(filters),readEntriesAtLines:(path:any,lines:any)=>session.readSessionEntriesAtLines(path,lines,['user','assistant'])}:{}) })
const api = await import('../../../src/server/api/search')
const reduce = (value: any) => JSON.parse(JSON.stringify(value).replaceAll(realpathSync(home), '$HOME').replaceAll(home, '$HOME'))
const observations: any[] = []
try {
  if (input.index) {
    await coordinator.start()
    const deadline = Date.now() + 5000
    while (coordinator.getStatus().state === 'building' && Date.now() < deadline) await Bun.sleep(5)
    if (coordinator.getStatus().state !== 'ready') throw new Error(`Real Source index not ready: ${JSON.stringify(coordinator.getStatus())}`)
  }
  for (const row of input.cases) {
    try {
      if (row.path) {
        const path=expand(row.path)
        if (!path.startsWith(home+'/')) throw new Error('Unsafe case path')
        if (row.write!==undefined){mkdirSync(dirname(path),{recursive:true});writeFileSync(path,row.write)}
        if (row.append!==undefined) appendFileSync(path,row.append)
        if (row.write!==undefined || row.append!==undefined) utimesSync(path,row.mtime??1791331200,row.mtime??1791331200)
        if (row.remove) rmSync(path,{recursive:true,force:true})
        if (row.reconcile && onBatch) await onBatch({fullSweep:true,paths:[]})
      }
      if (row.kind === 'api') {
        const url = new URL('http://fixture.invalid/api/search' + (row.sub ? '/' + row.sub : ''))
        const controller = new AbortController()
        if (row.aborted) controller.abort()
        const response = await api.handleSearchApi(new Request(url, { method: row.method, ...(row.method === 'GET' || row.method === 'HEAD' ? {} : { body: expand(row.body) }), signal: controller.signal }), url, url.pathname.split('/').filter(Boolean))
        const text = await response.text()
        observations.push({ status: response.status, body: text ? reduce(JSON.parse(text)) : null })
      } else if (row.kind === 'workspace') observations.push({ result: reduce(await service.searchWorkspace(row.query, JSON.parse(expand(JSON.stringify(row.options ?? {}))))) })
      else if (row.kind === 'sessions') observations.push({ result: reduce(await service.searchSessions(row.query, row.options ?? {})) })
      else if (row.kind === 'suggest') observations.push({ result: reduce(await service.searchSessionSuggestions(row.query, row.options ?? {})) })
      else if (row.kind === 'snippet') observations.push({ result: reduce((service as any).buildSnippet(row.text, row.query, row.caseSensitive ?? false)) })
      else if (row.kind === 'metadata') {const result=await session.getIndexedSessionSearchMetadata(row.paths.map(expand));observations.push({result:result?reduce([...result]):null})}
      else if (row.kind === 'atLines') observations.push({result:reduce(await session.readSessionEntriesAtLines(expand(row.path),new Set(row.lines),['user','assistant']))})
      else if (row.kind === 'filters') {const result=await session.getIndexedSessionSearchCandidates(row.options??{});observations.push({result:result?reduce([...result].map(([path,value]:any)=>[path,Object.fromEntries(Object.entries(value).filter(([k])=>k!=='sourceSnapshot'))])):null})}
      else throw new Error('Unsupported search Source fixture case')
    } catch (error: any) { observations.push({ error: error.message, status: error.statusCode ?? null, code: error.code ?? null }) }
  }
  process.stdout.write(JSON.stringify({ observations }))
} finally { await coordinator.stop();await primary?.stop() }
