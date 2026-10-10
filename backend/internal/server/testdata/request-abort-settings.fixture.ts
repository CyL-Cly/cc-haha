import fs from 'node:fs'
import path from 'node:path'
import * as physical from 'node:fs/promises'
import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'

const input = JSON.parse(await Bun.stdin.text())
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_REQUEST_ABORT_SETTINGS_FIXTURE !== '1') throw new Error('actual Source request lifecycle fixture only')
const home = assertDisposableFixtureHome(input.home, 'request abort settings')
Object.assign(globalThis, { MACRO: { VERSION: 'fixture', BUILD_TIME: '' } })
const config = path.join(home, '.claude')
fs.mkdirSync(path.join(config, 'cc-haha'), { recursive:true })
fs.writeFileSync(path.join(config, 'settings.json'), JSON.stringify(input.seed))
fs.writeFileSync(path.join(config, 'cc-haha/settings.json'), '{}')
// Retain only these fixture-owned loopback primitives; all business module
// process/network/credential capabilities are denied before importing it.
const physicalServe = Bun.serve.bind(Bun)
const physicalFetch = globalThis.fetch.bind(globalThis)
const capabilities = await installClientFixtureCapabilities(home,{absentPaths:absentManagedSettingsFixturePaths()})
const physicalRead = physical.readFile
let releaseStorage!: () => void
const heldStorage = new Promise<void>(resolve => { releaseStorage=resolve })
let storageStarted!: () => void
const started = new Promise<void>(resolve => { storageStarted=resolve })
let held = false
mock.module('fs/promises', () => ({ ...physical, readFile: async (...args:Parameters<typeof physical.readFile>) => {
  if (!held && String(args[0]) === path.join(config,'cc-haha/settings.json')) { held=true; storageStarted(); await heldStorage }
  return physicalRead(...args)
}}))
const { settleResponseOnRequestAbort } = await import('../../../../src/server/requestLifecycle')
const { handleSettingsApi } = await import('../../../../src/server/api/settings')
let responseStatus: number|undefined
let operationStatus: number|undefined
let returned!: () => void
const responseReturned = new Promise<void>(resolve => { returned=resolve })
let committed!: () => void
const operationCompleted = new Promise<void>(resolve => { committed=resolve })
const server = physicalServe({hostname:'127.0.0.1',port:0,async fetch(req) {
  const operation = handleSettingsApi(req,new URL(req.url),['api','settings','user'])
  operation.then(response => { operationStatus=response.status; committed() })
  const response = await settleResponseOnRequestAbort(req,operation)
  responseStatus=response.status; returned()
  return response
}})
const abort = new AbortController()
const client = physicalFetch(`http://127.0.0.1:${server.port}/api/settings/user`, {method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify(input.patch),signal:abort.signal}).catch(error => error.name)
await started
abort.abort()
await responseReturned
const operationPendingAtResponse = operationStatus === undefined
releaseStorage()
await operationCompleted
await client
server.stop(true)
process.stdout.write(JSON.stringify({responseStatus,operationStatus,operationPendingAtResponse,raw:JSON.parse(fs.readFileSync(path.join(config,'settings.json'),'utf8')),consumer:'src/server/requestLifecycle.ts + api/settings.ts + settingsService.ts + persistentStorageMigrations.ts',heldPhysicalRead:held,capabilities}))
