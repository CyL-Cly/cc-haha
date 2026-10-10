import { assertDisposableFixtureHome } from './fixture-guard'
import { readFileSync, mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
assertDisposableFixtureHome(process.env.HOME, 'Native Slack handler')
if (process.env.CC_HAHA_SLACK_HANDLER_FIXTURE !== '1') throw new Error('Unowned Slack handler fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
console.log = console.warn = console.error = () => {}
let now = input.now
Date.now = () => now
mkdirSync(process.env.CLAUDE_CONFIG_DIR!, { recursive: true })
writeFileSync(join(process.env.CLAUDE_CONFIG_DIR!, 'adapters.json'), JSON.stringify(input.config))
writeFileSync(join(process.env.CLAUDE_CONFIG_DIR!, 'adapter-sessions.json'), JSON.stringify({D1:{sessionId:'bound-session',workDir:input.work,updatedAt:now}}))
const calls: any[] = []
let post = 0, failure = ''
const files = input.files
const fakeFetch = async (value: any, options: any = {}) => {
 const url = new URL(String(value)), method = options.method ?? 'GET'
 const body = typeof options.body === 'string' ? JSON.parse(options.body) : options.body ? [...options.body] : null
 const headers = options.headers ?? {}
 if (url.pathname.endsWith('/auth.test')) return Response.json({ok:true,user_id:'UBOT'})
 calls.push({url:url.toString(),method,headers,body})
 if (failure && url.pathname.endsWith('/' + failure)) { failure = ''; return Response.json({ok:false,error:'private_failure'}, {status:429}) }
 if (url.hostname === 'files.slack.com') return new Response(new Uint8Array(files[url.pathname] ?? []))
 if (url.pathname.endsWith('/users.info')) return Response.json({ok:true,user:{real_name:` User ${url.searchParams.get('user')} `}})
 if (url.pathname.endsWith('/files.getUploadURLExternal')) return Response.json({ok:true,upload_url:'https://files.slack.com/upload',file_id:'F1'})
 return Response.json({ok:true,ts:`${++post}.1`})
}
globalThis.fetch = fakeFetch as any
Bun.plugin({name:'source-slack-handler-private-exports',setup(builder){
 builder.onLoad({filter:/adapters\/slack\/index\.ts$/},args=>({loader:'ts',contents:readFileSync(args.path,'utf8')+'\nexport { SlackResponse, collectAttachments, resolveDisplayName, replyThreads, displayNames, runtime, bridge, dedup, port }\n'}))
 builder.onLoad({filter:/adapters\/slack\/socket-mode\.ts$/},()=>({loader:'ts',contents:'export class SlackSocketMode { constructor(options){} async start(){} stop(){} }'}))
}})
const source:any = await import('../../../adapters/slack/index')
await new Promise(resolve=>setImmediate(resolve))
const responses = new Map(), results=[]
for(const a of input.actions){
 if(a.now!==undefined) now=a.now
 if(a.thread!==undefined)source.replyThreads.set('D1',a.thread)
 if(a.failure)failure=a.failure
 if(a.kind==='new')responses.set(a.id,new source.SlackResponse('D1'))
 if(a.kind==='append')await responses.get(a.id).append(a.text)
 if(a.kind==='finish')await responses.get(a.id).finish()
 if(a.kind==='notice')await source.port.sendNotice('D1',a.text)
 if(a.kind==='collect')results.push(await source.collectAttachments('D1',a.files))
 if(a.kind==='display')results.push(await source.resolveDisplayName(a.user))
 if(a.kind==='image')await source.port.sendImage('D1',{buffer:Buffer.from(a.bytes),mime:a.mime,alt:a.alt})
 if(a.kind==='clear')source.port.clearChat('D1')
 if(a.id&&responses.has(a.id)){const r=responses.get(a.id);results.push({ts:r.messageTs,current:r.current,pending:r.pending,lastEdit:r.lastEditAt})}
}
source.bridge.destroy();source.dedup.destroy();process.removeAllListeners('SIGINT')
process.stdout.write(JSON.stringify({calls,results,thread:source.replyThreads.get('D1')??'',cacheSize:source.displayNames.size}))
