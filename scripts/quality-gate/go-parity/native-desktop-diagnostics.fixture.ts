import { assertDisposableFixtureHome } from './fixture-guard'
import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'

const home = assertDisposableFixtureHome(process.env.HOME, 'Desktop diagnostics Source comparison')
if (process.env.CC_HAHA_DESKTOP_DIAGNOSTICS_FIXTURE !== '1') throw new Error('Missing diagnostics fixture marker')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const OriginalDate = Date
const fixed = new OriginalDate(input.now).valueOf()
class FixedDate extends OriginalDate {
  constructor(value?: string | number) { super(value === undefined ? fixed : value) }
  static now() { return fixed }
}
globalThis.Date = FixedDate as DateConstructor
process.env.CLAUDE_CODE_LOCAL_VERSION = 'fixture-version'
process.env.CLAUDE_CODE_LOCAL_BUILD_TIME = 'fixture-build'
await import('../../../preload')
const config = join(home, '.claude')
mkdirSync(config, { recursive: true })
for (const [relative, data] of Object.entries(input.files ?? {})) {
  const file = join(config, relative)
  mkdirSync(join(file, '..'), { recursive: true })
  writeFileSync(file, data as string)
}
for (const [relative, data] of Object.entries(input.projectFiles ?? {})) {
  const file = join(home, 'project', relative)
  mkdirSync(join(file, '..'), { recursive: true })
  writeFileSync(file, data as string)
}
const { DiagnosticsService } = await import('../../../src/server/services/diagnosticsService')
const { projectDiagnosticEventForSharing, projectProviderSummaryForSharing, buildDiagnosticsIssueReport } = await import('../../../src/server/services/diagnosticsShare')
const service = new DiagnosticsService()
const observations = []
for (const row of input.cases) {
  if (row.action === 'sanitize') observations.push(service.sanitizeString(row.value, row.max ?? 4096))
  else if (row.action === 'value') observations.push(service.sanitizeValue(row.value))
  else if (row.action === 'share') observations.push(projectDiagnosticEventForSharing(row.event))
  else if (row.action === 'providers') observations.push(projectProviderSummaryForSharing(row.value))
  else if (row.action === 'issue') observations.push(buildDiagnosticsIssueReport(row.value))
  else if (row.action === 'record') observations.push(await service.recordEvent(row.event))
  else if (row.action === 'recent') observations.push(await service.readRecentEvents(row.limit))
  else if (row.action === 'status') observations.push(await service.getStatus())
  else if (row.action === 'doctor' || row.action === 'doctorRepair') {
    process.env.CLAUDE_CODE_DISABLE_AGENT_SKILLS_DIR = row.agents === false ? '1' : '0'
    const { DoctorService } = await import('../../../src/server/services/doctorService')
    const doctor = new DoctorService({ configDir: config, homeDir: home, ...(row.project ? { projectRoot: join(home, 'project') } : {}) })
    observations.push(row.action === 'doctor' ? await doctor.getReport() : await doctor.repair(row.targetIds))
  }
  else if (row.action === 'providerSchema') {
    const { ProvidersIndexSchema } = await import('../../../src/server/types/provider')
    const result = ProvidersIndexSchema.safeParse(row.value)
    observations.push(result.success ? '' : result.error.issues.map((issue) => `${issue.path.join('.') || '<root>'}: ${issue.message}`).join('; '))
  }
  else if (row.action === 'doctorApi') {
    const { handleDoctorApi } = await import('../../../src/server/api/doctor')
    const url = new URL(row.path.replaceAll('<home>', home), 'http://127.0.0.1:3000')
    const response = await handleDoctorApi(new Request(url, {
      method: row.method,
      ...(row.headers === undefined ? (row.body === undefined ? {} : { headers: { 'content-type': 'application/json' } }) : { headers: row.headers }),
      ...(row.body === undefined ? {} : { body: row.body.replaceAll('<home>', home) }),
    }), url, url.pathname.split('/').filter(Boolean))
    observations.push({ status: response.status, body: await response.json() })
  }
  else if (row.action === 'clear') { await service.clear(); observations.push({ ok: true }) }
  else if (row.action === 'api') {
    const { handleDiagnosticsApi } = await import('../../../src/server/api/diagnostics')
    const url = new URL(row.path, 'http://127.0.0.1:3000')
    const response = await handleDiagnosticsApi(new Request(url, {
      method: row.method,
      headers: { 'content-type': 'application/json' },
      ...(row.body === undefined ? {} : { body: row.body }),
    }), url, url.pathname.split('/').filter(Boolean))
    observations.push({ status: response.status, body: await response.json() })
  }
  else if (row.action === 'export') {
    const { readFileSync } = await import('node:fs')
    const { gunzipSync } = await import('node:zlib')
    const bundle = await service.exportBundle()
    const tar = gunzipSync(readFileSync(bundle.path))
    const files: Record<string, string> = {}
    for (let offset = 0; offset < tar.length; offset += 512) {
      const header = tar.subarray(offset, offset + 512)
      const name = header.subarray(0, 100).toString('utf8').replace(/\0.*$/s, '')
      if (!name) break
      const size = Number.parseInt(header.subarray(124, 136).toString('ascii').replace(/\0.*$/s, '').trim(), 8)
      files[name] = tar.subarray(offset + 512, offset + 512 + size).toString('utf8')
      offset += Math.ceil(size / 512) * 512
    }
    observations.push(files)
  } else throw new Error(`Unknown diagnostics fixture action: ${row.action}`)
}
process.stdout.write(JSON.stringify(observations))
