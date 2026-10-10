import { assertDisposableFixtureHome } from './fixture-guard'
import { realpathSync } from 'node:fs'
import { isAbsolute, relative, sep } from 'node:path'

const home = assertDisposableFixtureHome(process.env.HOME, 'Native Open Targets')
if (process.env.CC_HAHA_NATIVE_OPEN_TARGETS_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Open Targets requires isolated production fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const within = relative(realpathSync(home), realpathSync(input.cwd))
if (!isAbsolute(input.cwd) || within === '..' || within.startsWith(`..${sep}`) || isAbsolute(within)) throw new Error('Unowned Open Targets cwd')
process.chdir(input.cwd)
const { createOpenTargetService, openTargetService, parseDarwinApplicationListOutput } = await import('../../../src/server/services/openTargetService')
const { handleOpenTargetsApi } = await import('../../../src/server/api/open-targets')
const native = input.native
let now = input.now
const launches: unknown[] = []
const icons: unknown[] = []
const queries: string[] = []
// Overrides are strictly external OS primitives and clock. The actual service
// and HTTP business below remain production Source functions, with real stat
// and path validation over the disposable parent-owned target files.
const service = createOpenTargetService({
  platform: input.platform,
  ttlMs: input.ttl,
  now: () => now,
  commandExists: async command => Boolean(native.commands[command]),
  resolveCommand: async command => native.commands[command] ?? null,
  pathExists: async path => native.exists.includes(path),
  launch: async (command, args) => {
    launches.push({ command, args })
    return { code: native.launchCode ?? 0, stdout: '', stderr: '' }
  },
  readDirNames: async path => native.dirs[path] ?? [],
  readTextFile: async path => native.texts[path] ?? null,
  readPlistValue: async (path, key) => native.plists[`${path}:${key}`] ?? null,
  convertIconToPng: async (path, size) => {
    icons.push({ path, size })
    if (!(path in native.icons)) throw new Error('Native icon conversion failed')
    return Buffer.from(native.icons[path], 'base64')
  },
  listApplicationsForFile: async path => {
    queries.push(path)
    return parseDarwinApplicationListOutput(JSON.stringify(native.applications[path] ?? { defaultApplicationPath: null, applications: [] }))
  },
})
// Bind a real Source service instance to the actual Source API. No business
// result, loader, route or method is mocked.
Object.assign(openTargetService, service)
const observations = []
for (const op of input.operations) {
  if (op.kind === 'advance') { now += op.delta; continue }
  if (op.kind === 'mutate') { Object.assign(native, op.native); continue }
  if (op.kind === 'parse') {
    observations.push({ parsed: parseDarwinApplicationListOutput(op.output) })
    continue
  }
  if (op.kind === 'list') {
    observations.push({ status: 200, body: JSON.stringify(await service.listTargets(op.force)), type: 'application/json', cache: null })
    continue
  }
  const url = new URL(op.path, 'http://127.0.0.1')
  if (url.origin !== 'http://127.0.0.1' || !url.pathname.startsWith('/api/open-targets')) throw new Error('Undeclared Open Targets endpoint')
  const response = await handleOpenTargetsApi(new Request(url, { method: op.method, body: op.body }), url, url.pathname.split('/').filter(Boolean))
  const data = Buffer.from(await response.arrayBuffer())
  const type = response.headers.get('content-type')
  observations.push({ status: response.status, body: type === 'image/png' ? data.toString('base64') : data.toString('utf8'), type, cache: response.headers.get('cache-control') })
}
process.stdout.write(JSON.stringify({ observations, launches, icons, queries }))
