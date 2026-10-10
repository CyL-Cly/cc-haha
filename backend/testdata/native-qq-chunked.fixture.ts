import https from 'node:https'
import {syncBuiltinESMExports}from'node:module'
import {readFileSync}from'node:fs'
import {dirname}from'node:path'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME,'QQ real chunked upload')
if(process.env.CC_HAHA_QQ_MESSAGE_LEAF!=='1')throw new Error('Unowned chunked fixture')
const input=await Bun.stdin.json()
const realFetch=fetch,realRequest=https.request,realTimeout=setTimeout,realNow=Date.now
let now=100000000
Date.now=()=>now;Math.random=()=>0
const delays:number[]=[],mappedCalls:any[]=[]
globalThis.setTimeout=((f:any,ms:number,...args:any[])=>{if([1000,2000,4000].includes(ms)){delays.push(ms);now+=ms;return realTimeout(f,0,...args)}return realTimeout(f,ms,...args)})as typeof setTimeout
globalThis.fetch=(async(url:any,init:any)=>{const logical=String(url);if(!logical.startsWith('https://bots.qq.com/')&&!logical.startsWith('https://api.sgroup.qq.com/'))throw new Error('Unowned upload API');const h=new Headers(init.headers);h.set('x-fixture-logical-url',logical);return realFetch(input.httpURL+'/api',{...init,headers:h})})as typeof fetch
https.request=((url:any,options:any,callback:any)=>{const u=new URL(String(url));if(u.hostname!=='cos.qq.private'||u.protocol!=='https:')throw new Error('Unowned COS HTTPS primitive');const mapped=new URL(input.cosURL+u.pathname+u.search);mappedCalls.push({hostname:mapped.hostname,port:mapped.port});return realRequest({...options,protocol:'https:',hostname:mapped.hostname,port:Number(mapped.port),path:mapped.pathname+mapped.search,rejectUnauthorized:false,headers:{...options.headers,'X-Fixture-Logical-URL':String(url)}},callback)})as typeof https.request
syncBuiltinESMExports()
;(globalThis as any).__qqOwnedHTTPS=https
Bun.plugin({name:'qq-owned-https-primitive',setup(build){build.onLoad({filter:/qqbot-nodejs\/dist\/protocol\/api\/media-chunked\.js$/},args=>{const original=readFileSync(args.path,'utf8');const declaration='import * as https from "node:https";';if(!original.includes(declaration))throw new Error('Original SDK HTTPS import changed');return{contents:original.replace(declaration,'const https = globalThis.__qqOwnedHTTPS;'),loader:'js',resolveDir:dirname(args.path)}})}})
const {QQBot}=await import('../../adapters/node_modules/@tencent-connect/qqbot-nodejs/dist/QQBot.js')

const bot=new QQBot({appId:'app',appSecret:'private-secret'})
const target={scope:'c2c',targetId:'private-user',msgId:'private-inbound'}as const
const buffer=Buffer.alloc(input.size,0x61)
const values=[]
for(let i=0;i<input.sends;i++){try{const result=await bot.sendMedia({target,fileType:1,buffer,content:'private-image'});values.push({value:result.message??null})}catch(e:any){values.push({error:e.message,name:e.name,filePath:e.filePath,fileSize:e.fileSize})}}
bot.stop();globalThis.setTimeout=realTimeout;Date.now=realNow
process.stdout.write(JSON.stringify({values,delays})+'\n')
