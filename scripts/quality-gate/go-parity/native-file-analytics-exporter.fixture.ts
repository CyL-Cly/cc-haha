import { readFile, readdir, mkdir, rm, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
const home = assertDisposableFixtureHome(process.env.HOME, 'Analytics exporter Source comparison')
const input = await Bun.stdin.json()
const origin = new URL(input.origin)
if (origin.hostname !== '127.0.0.1' || origin.protocol !== 'http:' || !origin.port) throw new Error('Explicit isolated event sink required')
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
const config = await import('../../../src/utils/config')
state.setCwdState(home)
state.setOriginalCwd(home)
state.setIsInteractive(false)
config.enableConfigs()
process.env.ANTHROPIC_API_KEY = 'fake-event-key'
mock.module('../../../src/utils/secureStorage/index', () => ({ getSecureStorage: () => ({ read: () => null, readAsync: async () => null }) }))
mock.module('../../../src/utils/secureStorage/keychainPrefetch', () => ({ getLegacyApiKeyPrefetchResult: () => ({ stdout: '' }), getLegacyApiKeyPrefetchPromise: () => undefined, startKeychainPrefetch: () => {}, clearLegacyApiKeyPrefetch: () => {} }))
const { FirstPartyEventLoggingExporter } = await import('../../../src/services/analytics/firstPartyEventLoggingExporter')
let scans: Promise<unknown>[] = []
let oldJobs: Promise<unknown>[] = []
const prototype = FirstPartyEventLoggingExporter.prototype as any
const originalScan = prototype.retryPreviousBatches
const originalOld = prototype.retryFileInBackground
prototype.retryPreviousBatches = function (...args: unknown[]) {
  const promise = originalScan.apply(this, args)
  scans.push(promise)
  return promise
}
prototype.retryFileInBackground = function (...args: unknown[]) {
  const promise = originalOld.apply(this, args)
  oldJobs.push(promise)
  return promise
}
const results = []
for (let i = 0; i < input.cases.length; i++) {
  const row = input.cases[i]
  state.switchSession(`private-source-${i}`)
  const directory = join(process.env.CLAUDE_CONFIG_DIR!, 'telemetry')
  await rm(directory, { recursive: true, force: true })
  await mkdir(directory, { recursive: true })
  for (const file of row.previous ?? []) await writeFile(join(directory, file.name.replace('$SID', `private-source-${i}`)), file.content)
  const scheduled: Array<{ fn: () => Promise<void>, delay: number, canceled: boolean }> = []
  scans = []
  oldJobs = []
  const exporter = new FirstPartyEventLoggingExporter({
    baseUrl: input.origin, path: `/case/${i}`, maxBatchSize: row.batchSize ?? 2, maxAttempts: row.maxAttempts ?? 8,
    batchDelayMs: 1, skipAuth: row.skipAuth ?? false, isKilled: () => row.killed ?? false,
    schedule: (fn, delay) => { const ticket = { fn, delay, canceled: false }; scheduled.push(ticket); return () => { ticket.canceled = true } },
  })
  // Observe original method promises; preserve every actual call and result.
  await Promise.all(scans)
  await Promise.all(oldJobs)
  const codes = []
  for (const records of row.exports ?? []) {
    const code = await new Promise(resolve => exporter.export(records.map((record: any) => ({
      instrumentationScope: { name: 'com.anthropic.claude_code.events' }, body: record.event.name,
      hrTime: [Math.floor(record.timestamp / 1000), (record.timestamp % 1000) * 1_000_000],
      attributes: { event_name: record.event.name, event_id: record.id, core_metadata: record.core, user_metadata: record.user, event_metadata: record.event.metadata },
    })), result => resolve(result.code)))
    codes.push(code)
  }
  if (row.retry) { const ticket = scheduled.find(ticket => !ticket.canceled); if (ticket) await ticket.fn() }
  await exporter.forceFlush()
  const files = []
  for (const name of (await readdir(directory)).sort()) {
    const content = await readFile(join(directory, name), 'utf8')
    const normalized = name.startsWith(`1p_failed_events.private-source-${i}.`) && !name.endsWith('.old.json') ? `1p_failed_events.$SID.$PROCESS.json` : name.replace(`private-source-${i}`, '$SID')
    files.push({ name: normalized, events: content.trim().split('\n').filter(Boolean).map(line => JSON.parse(line)) })
  }
  results.push({ codes, files, scheduled: scheduled.map(ticket => ({ delay: ticket.delay, canceled: ticket.canceled })) })
  await exporter.shutdown()
}
process.stdout.write(JSON.stringify(results))
