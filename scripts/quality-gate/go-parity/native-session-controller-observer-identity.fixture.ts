import fs from 'node:fs'
import { assertDisposableFixtureHome } from './fixture-guard'

const profile = process.env.CC_HAHA_CONTROLLER_OBSERVER_PROFILE
if (Bun.version !== '1.3.14' || !['direct', 'sync', 'legacy-async'].includes(profile ?? '')) throw new Error('Owned pinned observer profile required')
assertDisposableFixtureHome(process.env.HOME!, 'Actual Source observer identity audit')
// Transform only the test's observation adapters. All original Source product
// modules, inputs, OS guard and real main/headless consumers remain unchanged.
// This is a separate experiment; the old strict fixture is never rewritten.
Bun.plugin({ name: 'owned-controller-observer-identity-audit', setup(build) {
  build.onLoad({ filter: /\/session-controller\.fixture\.ts$/ }, args => {
    let text = fs.readFileSync(args.path, 'utf8')
    const replaceOne = (before: string, after: string) => {
      if (text.split(before).length !== 2) throw new Error('Exact observer boundary changed: ' + before.slice(0, 70))
      text = text.replace(before, after)
    }
    if (profile === 'sync') {
      replaceOne("spyOn(storage, 'recordTranscript').mockImplementation(async (...args) => {", "spyOn(storage, 'recordTranscript').mockImplementation((...args) => {")
      replaceOne("spyOn(storage, 'flushSessionStorage').mockImplementation(async () => {", "spyOn(storage, 'flushSessionStorage').mockImplementation(() => {")
      replaceOne(";(globalThis as any).fetch = (async (url: unknown, init?: RequestInit) => {", ";(globalThis as any).fetch = ((url: unknown, init?: RequestInit) => {")
      replaceOne("spyOn(storage, 'doesMessageExistInSession').mockImplementation(async (sid, uuid) => {\n  const exists = await oldExists(sid, uuid)\n  uuidChecks.push({ uuid, exists })\n  return exists\n})", "spyOn(storage, 'doesMessageExistInSession').mockImplementation((sid, uuid) => {\n  const promise = oldExists(sid, uuid)\n  void promise.then(exists => uuidChecks.push({ uuid, exists }))\n  return promise\n})")
    }
    if (profile === 'direct') {
      replaceOne("spyOn(storage, 'recordTranscript').mockImplementation(async (...args) => {\n  records.push(encode(args[0]))\n  phases.push({ phase: 'record', messages: encode(args[0]) })\n  return oldRecord(...args)\n})", '')
      replaceOne("spyOn(storage, 'flushSessionStorage').mockImplementation(async () => {\n  phases.push({ phase: 'flush' })\n  return oldFlush()\n})", '')
      replaceOne(";(globalThis as any).fetch = (async (url: unknown, init?: RequestInit) => {\n  if (init?.method === 'POST') {\n    requests.push(String(init.body))\n    phases.push({ phase: 'HTTP', messages: encode(activeMessages) })\n  }\n  return fetchOriginal(url as any, init)\n})", '')
      replaceOne("spyOn(storage, 'doesMessageExistInSession').mockImplementation(async (sid, uuid) => {\n  const exists = await oldExists(sid, uuid)\n  uuidChecks.push({ uuid, exists })\n  return exists\n})", '')
      const begin = text.indexOf("for (const name of ['stat', 'mkdir', 'open', 'readFile', 'readdir'] as const) {")
      const end = text.indexOf("const logging = await import('../../../src/utils/log')", begin)
      if (begin < 0 || end < begin) throw new Error('Exact native read-only observer range changed')
      text = text.slice(0, begin) + text.slice(end)
    }
    return { loader: 'ts', contents: text }
  })
} })
await import('./session-controller.fixture')
