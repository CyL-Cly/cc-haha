import { assertDisposableFixtureHome } from './fixture-guard'
import { mkdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { parse } from 'acorn'
import { Database } from 'bun:sqlite'
const home = assertDisposableFixtureHome(process.env.HOME, 'Trace Source comparison')
if (process.env.CC_HAHA_TRACE_FIXTURE !== '1') throw new Error('Missing trace fixture marker')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const config = join(home,'.claude')
mkdirSync(config,{recursive:true})
if(input.action==='value') {
 await import('../../../preload')
 const mod=await import('../../../src/services/api/traceCapture')
 const observations=input.cases.map((row:any)=>({body:mod.createTraceBodySnapshot(row.body,{...(row.maxPreview!==undefined?{maxPreviewChars:row.maxPreview}:{}),alreadyTruncated:row.alreadyTruncated===true}),semantic:mod.createTraceRequestSemantic(row.body,row.source??'anthropic')}))
 process.stdout.write(JSON.stringify({observations}))
 mod.clearTraceCaptureStateForTests()
} else if(input.action==='service') {
 await import('../../../preload')
 const mod=await import('../../../src/services/api/traceCapture')
 process.env.CC_HAHA_LOCAL_INDEX=input.mode??'on'
 const {writeFileSync,appendFileSync,rmSync,utimesSync}=await import('node:fs')
 const dir=join(config,'cc-haha','traces');mkdirSync(dir,{recursive:true})
 const observations:any[]=[]
 let lastWindow:any
 const normalize=(v:any):any=>Array.isArray(v)?v.map(normalize):v&&typeof v==='object'?Object.fromEntries(Object.entries(v).map(([k,x])=>[k,normalize(x)])):typeof v==='string'?v.replaceAll(home,'$HOME'):v
 for(const row of input.operations) {
  if(row.repeatWrite){const lines=[];for(let n=0;n<row.repeatWrite;n++){const record={...row.record,id:'c'+String(n).padStart(5,'0')};lines.push(JSON.stringify({type:'call',record})+'\n')};writeFileSync(join(dir,row.id+'.jsonl'),lines.join(''))}
  if(row.oversizedWrite){writeFileSync(join(dir,row.id+'.jsonl'),JSON.stringify({ignored:'a'.repeat(row.oversizedWrite)})+'\n'+row.following)}
  if(row.write!==undefined)writeFileSync(join(dir,row.id+'.jsonl'),row.write)
  if(row.append!==undefined)appendFileSync(join(dir,row.id+'.jsonl'),row.append)
  if(row.remove)rmSync(join(dir,row.id+'.jsonl'),{force:true})
  if(row.mtime)utimesSync(join(dir,row.id+'.jsonl'),new Date(row.mtime),new Date(row.mtime))
  let value:any
  try {
   if(row.method==='call')value=await mod.traceCaptureService.recordCall(row.input)
   if(row.method==='event')value=await mod.traceCaptureService.recordEvent(row.input)
   if(row.method==='full')value=await mod.traceCaptureService.getSessionTrace(row.id)
   if(row.method==='overview'){const options={...row.options};if(options.scanCursor==='$NEXT')options.scanCursor=lastWindow?.nextScanCursor;if(options.revisionToken==='$TOKEN')options.revisionToken=lastWindow?.revisionToken;value=await mod.traceCaptureService.getSessionTraceOverview(row.id,options);lastWindow=value.window}
   if(row.method==='detail')value=await mod.traceCaptureService.getSessionTraceCall(row.id,row.callId,row.options??{})
   if(row.method==='list')value=await mod.traceCaptureService.listSessionTraces(row.options)
   if(row.method==='files')value=await mod.traceCaptureService.listSessionTraceFiles()
   if(row.method==='delete')value=await mod.traceCaptureService.deleteSessionTrace(row.id)
   if(row.method==='revision')value=await mod.traceCaptureService.getSessionTraceRevision(row.id,row.since,row.sinceToken)
   if(row.method==='settings')value=await mod.updateTraceCaptureSettings(row.input??{})
   if(row.method==='settingsFile')value=JSON.parse(readFileSync(join(config,'cc-haha','settings.json'),'utf8'))
   if(row.method==='readRaw')value=readFileSync(join(dir,row.id+'.jsonl'),'utf8')
   if(row.method==='drain'){await mod.drainTraceCaptureForTests();value='drained'}
   observations.push({value:normalize(value)})
  }catch(e:any){observations.push({error:e.message,code:e.code??null})}
 }
 await mod.drainTraceCaptureForTests();mod.clearTraceCaptureStateForTests()
 process.stdout.write(JSON.stringify({observations}))
} else if(input.action==='protocolObserver') {
 const {ProtocolTraceObserver}=await import('../../../src/server/proxy/protocolTrace')
 const observations=[]
 for(const row of input.cases){const observer=new ProtocolTraceObserver(row.protocol,row.request,row.budget);for(const op of row.operations){if(op.bytes)observer.push(typeof op.bytes==='string'?new TextEncoder().encode(op.bytes):new Uint8Array(op.bytes));if(op.json!==undefined)observer.observeJson(op.json,op.event);if(op.finish)observer.finish(op.finish)};observations.push(observer.snapshot())}
 process.stdout.write(JSON.stringify({observations}))
} else if(input.action==='proxyProducer') {
 await import('../../../preload')
 process.env.CC_HAHA_TRACE_API_CALLS='1';process.env.CC_HAHA_LOCAL_INDEX='on'
 const mod=await import('../../../src/services/api/traceCapture')
 const {ProtocolTraceObserver}=await import('../../../src/server/proxy/protocolTrace')
 const source=readFileSync(new URL('../../../src/server/proxy/handler.ts',import.meta.url),'utf8')
 const executable=new Bun.Transpiler({loader:'ts'}).transformSync(source)
 const ast=parse(executable,{ecmaVersion:'latest',sourceType:'module'}) as any
 const names=['createProxyTraceRequestBody','startProxyTraceCall','recordProxyTrace','captureTraceStream','parseContentEncodings']
 const functions=names.map(name=>{const declaration=ast.body.find((node:any)=>node.type==='FunctionDeclaration'&&node.id.name===name);if(!declaration)throw new Error('Missing actual proxy Source producer '+name);return executable.slice(declaration.start,declaration.end)})
 const originalDate=Date;const fixedMs=Date.parse('2026-10-01T00:00:00.000Z');globalThis.Date=class extends originalDate{constructor(...args:any[]){super(...(args.length?args:[fixedMs]) as any)}static now(){return fixedMs}} as any
 const {createGunzip,createInflate}=await import('node:zlib')
 const functionsFactory=new Function('traceCaptureService','createTraceBodySnapshot','createTraceCallId','TRACE_STREAM_CAPTURE_BYTES','SUPPORTED_TRACE_CODECS','createGunzip','createInflate',functions.join('\n')+'\nreturn {startProxyTraceCall,recordProxyTrace,captureTraceStream}')
 const observations=[]
 try{for(const row of input.cases){
  if (row.environment !== undefined) {
    for (const key of ['CC_HAHA_TRACE_API_CALLS', 'CLAUDE_CODE_ENTRYPOINT']) {
      if (row.environment[key] === undefined) delete process.env[key]
      else process.env[key] = row.environment[key]
    }
    await mod.updateTraceCaptureSettings({ enabled: row.settingsEnabled })
  }
  const api=functionsFactory(mod.traceCaptureService,mod.createTraceBodySnapshot,()=>row.id,mod.TRACE_STREAM_CAPTURE_BYTES,new Set(['gzip','x-gzip','deflate']),createGunzip,createInflate);const context:any={sessionId:row.sessionId,provider:row.provider,anthropicRequest:row.anthropic,...(row.protocol!=='anthropic'?{protocolTrace:new ProtocolTraceObserver(row.protocol,row.upstream,row.budget)}:{})};const callId=api.startProxyTraceCall({context,model:row.model,upstreamUrl:row.url,upstreamRequest:row.upstream,requestHeaders:row.headers,startedAt:new Date().toISOString()});await mod.drainTraceCaptureForTests();for(const bytes of row.upstreamChunks??[])context.protocolTrace?.push(new TextEncoder().encode(bytes));if(row.transport)context.protocolTrace?.finish(row.transport);let responseBodySnapshot:any;if(row.anthropicChunks){let resolveSnapshot:any;const snapshotReady=new Promise(resolve=>{resolveSnapshot=resolve});const chunks=row.anthropicChunks.map((v:string)=>new TextEncoder().encode(v));const actualStream=new ReadableStream({start(c){for(const b of chunks)c.enqueue(b);c.close()}});const traced=api.captureTraceStream(actualStream,async(snapshot:any)=>{responseBodySnapshot=snapshot;resolveSnapshot(snapshot)});await new Response(traced).arrayBuffer();await snapshotReady};await api.recordProxyTrace({callId,context,model:row.model,upstreamUrl:row.url,upstreamRequest:row.upstream,requestHeaders:row.headers,startedAt:new Date().toISOString(),startedAtMs:Date.now(),...(row.status!==undefined?{responseStatus:row.status}:{}),...(row.upstreamResponse!==undefined?{upstreamResponseBody:row.upstreamResponse}:{}),...(row.anthropicResponse!==undefined?{anthropicResponseBody:row.anthropicResponse}:{}),...(responseBodySnapshot?{responseBodySnapshot}:{}),...(row.delivery?{protocolTraceEnd:row.delivery}:{}),...(row.error?{error:new Error(row.error)}:{}),responseHeaders:new Headers(row.responseHeaders??{})});const trace=await mod.traceCaptureService.getSessionTrace(row.sessionId);observations.push(trace)}}finally{globalThis.Date=originalDate;await mod.drainTraceCaptureForTests();mod.clearTraceCaptureStateForTests()};process.stdout.write(JSON.stringify({observations}))
} else if(input.action==='processGate') {
 await import('../../../preload')
 const mod=await import('../../../src/services/api/traceCapture')
 const {writeFileSync}=await import('node:fs')
 mkdirSync(join(config,'cc-haha'),{recursive:true})
 const observations=[]
 for(const row of input.cases){writeFileSync(join(config,'cc-haha','settings.json'),JSON.stringify({traceCapture:{enabled:row.settingsEnabled}}));for(const key of ['CC_HAHA_TRACE_API_CALLS','CLAUDE_CODE_ENTRYPOINT']){if(row.environment[key]===undefined)delete process.env[key];else process.env[key]=row.environment[key]};observations.push({shouldCapture:mod.shouldCaptureApiTrace(),enabled:mod.isTraceCaptureEnabled()})}
 mod.clearTraceCaptureStateForTests();process.stdout.write(JSON.stringify({observations}))
} else if(input.action==='http') {
 await import('../../../preload')
 process.env.CC_HAHA_LOCAL_INDEX=input.mode??'off'
 const {writeFileSync,utimesSync}=await import('node:fs')
 for(const [relative,value] of Object.entries(input.files??{})){const p=join(config,relative);mkdirSync(join(p,'..'),{recursive:true});writeFileSync(p,String(value));if(input.mtime)utimesSync(p,new Date(input.mtime),new Date(input.mtime))}
 const {handleTracesApi}=await import('../../../src/server/api/traces')
 const {handleSessionsApi}=await import('../../../src/server/api/sessions')
 const mod=await import('../../../src/services/api/traceCapture')
 const observations:any[]=[]
 for(const row of input.requests){const u=new URL(row.path,'http://127.0.0.1');const req=new Request(u,{method:row.method??'GET',...(row.raw!==undefined?{body:row.raw,headers:{'content-type':'application/json'}}:{})});const parts=u.pathname.split('/').filter(Boolean);const res=parts[1]==='traces'?await handleTracesApi(req,u,parts):await handleSessionsApi(req,u,parts);const body=await res.text();let value:any;try{value=JSON.parse(body)}catch{value=body};const reduce=(v:any):any=>Array.isArray(v)?v.map(reduce):v&&typeof v==='object'?Object.fromEntries(Object.entries(v).filter(([k])=>!['revisionToken','nextScanCursor','fileUpdatedAt','revision','messageSignature'].includes(k)).map(([k,v])=>[k,reduce(v)])):typeof v==='string'?v.replaceAll(home,'$HOME'):v;observations.push({status:res.status,body:reduce(value),...(parts[4]==='raw'&&res.status===200?{headers:{'content-type':res.headers.get('content-type'),'content-disposition':res.headers.get('content-disposition'),'cache-control':res.headers.get('cache-control')}}:{})})}
 await mod.drainTraceCaptureForTests();mod.clearTraceCaptureStateForTests();process.stdout.write(JSON.stringify({observations}))
} else if(input.action==='response') {
 await import('../../../preload')
 const mod=await import('../../../src/services/api/traceCapture')
 const observations:any[]=[]
 for(const row of input.cases) {
  const controller=new AbortController()
  const chunks=row.chunks.map((v:any)=>typeof v==='string'?new TextEncoder().encode(v):new Uint8Array(v))
  let cancelled=false
  const body=new ReadableStream({start(c){for(const b of chunks)c.enqueue(b);if(row.error)c.error(new Error(row.error));else if(!row.hang)c.close();if(row.abort)setTimeout(()=>controller.abort(new Error('fixture abort')),10)},cancel(){cancelled=true}})
  const response=new Response(body,{headers:row.headers??{}})
  if(row.alreadyAbort)controller.abort(new Error('fixture abort'))
  try {const result=await mod.captureResponseTraceSnapshot(response,{signal:controller.signal,abortGraceMs:25});observations.push({value:{snapshot:result.snapshot,aborted:result.aborted,...(result.abortReason?{abortReason:result.abortReason.message}:{})},cancelled})}catch(e:any){observations.push({error:e.message,cancelled})}
 }
 process.stdout.write(JSON.stringify({observations}));mod.clearTraceCaptureStateForTests()
} else if(input.action==='schema'||input.action==='index') {
 const source=readFileSync(new URL('../../../src/server/services/localIndex/traceMigrations.ts',import.meta.url),'utf8')
 const ast=parse(new Bun.Transpiler({loader:'ts'}).transformSync(source),{ecmaVersion:'latest',sourceType:'module'}) as any
 const schemas:Record<string,string>={}
 for(const st of ast.body) if(st.type==='VariableDeclaration') for(const d of st.declarations) if(/^SCHEMA_V\d+$/.test(d.id?.name??'')){if(d.init?.type!=='TemplateLiteral'||d.init.expressions.length)throw new Error('Static migration required');schemas[d.id.name]=d.init.quasis[0].value.cooked}
 const dbPath=join(config,'cc-haha','db','trace-index-v1.sqlite');mkdirSync(join(config,'cc-haha','db'),{recursive:true})
 if(input.oldVersion){const db=new Database(dbPath);for(let i=1;i<=input.oldVersion;i++){db.exec(schemas[`SCHEMA_V${i}`]);db.exec(`PRAGMA user_version=${i}`)};for(const sql of input.seed??[])db.exec(sql);db.close(true)}
 const {openTraceIndexDatabase}=await import('../../../src/server/services/localIndex/traceDatabase')
 const db=openTraceIndexDatabase({path:dbPath,scope:config})
 try {
 if(input.action==='schema')process.stdout.write(JSON.stringify(db.read(op=>({schemas,version:op.get('PRAGMA user_version'),schema:op.all("SELECT type,name,sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY type,name"),journalSize:op.get('PRAGMA journal_size_limit'),sources:op.all('SELECT * FROM trace_sources'),sessions:op.all('SELECT * FROM trace_sessions'),calls:op.all('SELECT * FROM trace_calls'),models:op.all('SELECT * FROM trace_session_models')}))))
 else {const {createTraceIndex}=await import('../../../src/server/services/localIndex/traceIndex');const index=createTraceIndex(db);const observations:any[]=[];for(const row of input.operations){try{let value:any;if(row.method==='replace')value=index.replaceSession(row.input);if(row.method==='append')value=index.appendEntries(row.input);if(row.method==='get')value=index.getSession(row.id);if(row.method==='page')value=index.getSessionPage(row.id,row.offset,row.limit);if(row.method==='changes')value=index.getChanges(row.id,row.since);if(row.method==='list')value=index.listSessions(row.options);if(row.method==='call')value=index.getCallLocator(row.id,row.callId);if(row.method==='delete'){index.deleteSession(row.id);value=index.getSource(row.id)};if(row.method==='degrade'){index.markDegraded(row.id,row.code);value=index.getSource(row.id)};observations.push({value})}catch(e:any){observations.push({error:e.message})}};process.stdout.write(JSON.stringify({observations}))}
 }finally{db.close()}
} else throw new Error('Unknown trace fixture action')
