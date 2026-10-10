import { assertDisposableFixtureHome } from './fixture-guard'
import { readFile } from 'node:fs/promises'
import { join } from 'node:path'
const input=JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir,'Native Voice install')
if(process.env.CC_HAHA_VOICE_INSTALL_FIXTURE!=='1')throw new Error('Missing Voice install fixture')
for(const item of input.items)for(const url of item.asset.urls)if(new URL(url).hostname!=='127.0.0.1')throw new Error('loopback assets required')
const {senseVoiceLayout,itemProofPath}=await import('../../../src/server/services/voice/sensevoice/layout')
const {installAll,isInstalled,bytesOnDisk,removeInstall}=await import('../../../src/server/services/voice/sensevoice/install')
const layout=senseVoiceLayout(join(input.homeDir,'voice'))
const report:any[]=[]
await installAll({layout,items:input.items,download:{maxRetries:0,progressIntervalMs:0}},new AbortController().signal,state=>report.push(state))
const files=[]
for(const item of input.items)files.push((await readFile(itemProofPath(layout,item))).toString('base64'))
const ready=await isInstalled(layout,input.items)
const bytes=await bytesOnDisk(layout,input.items)
const manifest=JSON.parse(await readFile(layout.manifest,'utf8'))
await removeInstall(layout)
const removed=await isInstalled(layout,input.items)
await new Promise<void>(resolve=>process.stdout.write(JSON.stringify({files,ready,bytes,manifest,removed,final:report.at(-1)}),()=>resolve()))
process.exit(0)
