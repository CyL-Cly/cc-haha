import { assertDisposableFixtureHome } from './fixture-guard'
import WebSocket from 'ws'
assertDisposableFixtureHome(process.env.HOME, 'Native Slack socket')
if (process.env.CC_HAHA_SLACK_SOCKET_FIXTURE !== '1') throw new Error('Unowned Slack socket fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const { SlackSocketMode } = await import('../../../adapters/slack/socket-mode')
const logs: string[] = [], envelopes: any[] = []
console.log = console.error = (...args) => { logs.push(args.map(String).join(' ')) }
let opens=0, done!:()=>void
const completed = new Promise<void>(resolve=> {done=resolve})
const socket=new SlackSocketMode({openConnection:async()=>{opens++;if(opens===1)throw new Error('private open failure');return input.url+(opens===2?'/one':'/two')},createWebSocket:url=>new WebSocket(url,{ca:input.ca}),onEnvelope:e=>{envelopes.push(e);if(envelopes.length===2)done()}})
await socket.start()
await Promise.race([completed,new Promise((_,reject)=>setTimeout(()=>reject(new Error('actual source socket timed out: '+JSON.stringify({opens,logs,envelopes}))),8000))])
await new Promise(resolve=>setTimeout(resolve,50))
socket.stop()
await new Promise(resolve=>setTimeout(resolve,50))
process.stdout.write(JSON.stringify({opens,envelopes,logs}))
process.exit(0)
