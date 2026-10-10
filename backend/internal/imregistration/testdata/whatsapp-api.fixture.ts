import fs from 'node:fs'
import path from 'node:path'
import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'
const input = JSON.parse(await Bun.stdin.text())
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_WHATSAPP_REGISTRATION_FIXTURE !== '1') throw new Error('pinned actual WhatsApp registration fixture only')
const home = assertDisposableFixtureHome(input.home, 'WhatsApp desktop API')
const config = path.join(home, '.claude')
fs.mkdirSync(config, { recursive: true })
fs.writeFileSync(path.join(config, 'adapters.json'), JSON.stringify(input.seed ?? {future: {retained: true}}))
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../../preload')
const login = await import('../../../../adapters/whatsapp/protocol')
const originalStart = login.startWhatsAppLoginWithQr
const sockets: any[] = []
spyOn(login,'startWhatsAppLoginWithQr').mockImplementation(options => originalStart({...options, createSocket: async options => {
  fs.mkdirSync(options.authDir,{recursive:true,mode:0o700})
  fs.writeFileSync(path.join(options.authDir,'creds.json'),JSON.stringify({privateFixture:true,me:{id:'222:3@s.whatsapp.net'}}))
  const handlers: Function[]=[]
  const socket={user:{id:'222:3@s.whatsapp.net'},ev:{on(event:string,fn:Function){if(event==='connection.update')handlers.push(fn)}},end(){},async emit(value:any){await Promise.all(handlers.map(fn=>fn(value)))}}
  sockets.push(socket);options.onQr?.(input.qr ?? 'private-qr');return socket as any
}}))
const { handleAdaptersApi } = await import('../../../../src/server/api/adapters')
const rows = []
let sessionKey='unknown-private'
for (const row of input.actions) {
  if(row.event==='open')await sockets.at(-1).emit({connection:'open'})
  const url = new URL('http://127.0.0.1/api/adapters/whatsapp/' + row.tail)
  const response = await handleAdaptersApi(new Request(url, { method: row.method ?? 'POST', ...((row.method ?? 'POST') === 'POST' ? { body: row.raw === '$session' ? JSON.stringify({sessionKey}) : row.raw ?? '' } : {}) }), url, url.pathname.split('/').filter(Boolean))
  const body=await response.json()
  if(body.sessionKey)sessionKey=body.sessionKey
  rows.push({status:response.status,body})
}
process.stdout.write(JSON.stringify({home, rows, raw: JSON.parse(fs.readFileSync(path.join(config,'adapters.json'),'utf8')), capabilities }))
