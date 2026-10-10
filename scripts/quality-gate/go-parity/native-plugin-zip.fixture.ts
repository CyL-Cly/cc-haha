import { assertDisposableFixtureHome } from './fixture-guard'
import { mkdir, writeFile, readFile, lstat, readdir, chmod, symlink } from 'node:fs/promises'
import { join } from 'node:path'

const home = process.env.HOME!
assertDisposableFixtureHome(home, 'Native plugin ZIP')
if (process.env.CC_HAHA_PLUGIN_ZIP_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') {
  throw new Error('Plugin ZIP fixture requires isolated configuration')
}
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const clock = Date.parse(input.clock)
const OriginalDate = Date
globalThis.Date = class extends OriginalDate {
  constructor(value?: string | number) { super(value === undefined ? clock : value) }
  static now() { return clock }
} as DateConstructor
const zipCache = await import('../../../src/utils/plugins/zipCache')
const zip = await import('../../../src/utils/dxt/zip')

async function seed(root: string, items: any[]) {
  await mkdir(root, { recursive: true })
  for (const item of items) {
    const path = join(root, item.path)
    await mkdir(join(path, '..'), { recursive: true })
    if (item.kind === 'directory') await mkdir(path, { recursive: true })
    else if (item.kind === 'symlink') await symlink(item.target, path)
    else {
      await writeFile(path, Buffer.from(item.data, 'base64'))
      if (item.mode) await chmod(path, item.mode)
    }
  }
}
async function tree(root: string): Promise<unknown[]> {
  const values: unknown[] = []
  async function visit(relative: string) {
    const path = join(root, relative)
    let info
    try { info = await lstat(path) } catch { return }
    if (info.isDirectory()) {
      if (relative) values.push({ path: relative, kind: 'directory', mode: info.mode & 0o777 })
      for (const name of (await readdir(path)).sort()) await visit(relative ? `${relative}/${name}` : name)
    } else if (info.isFile()) values.push({ path: relative, kind: 'file', data: (await readFile(path)).toString('base64'), mode: info.mode & 0o777 })
  }
  await visit('')
  return values
}
async function contents(data: Uint8Array) {
  const files = await zip.unzipFile(Buffer.from(data))
  return Object.entries(files).map(([name, value]) => ({ name, data: Buffer.from(value).toString('base64') }))
}
const archives = []
for (const [index, item] of input.archives.entries()) {
  const data = Buffer.from(item.data, 'base64')
  const target = join(home, 'archives', String(index))
  const path = `${target}.zip`
  await writeFile(path, data).catch(async () => { await mkdir(join(home, 'archives'), { recursive: true }); await writeFile(path, data) })
  if (item.seed) await seed(target, item.seed)
  try {
    const entries = await contents(data)
    await zipCache.extractZipToDirectory(path, target)
    archives.push({ entries, modes: zip.parseZipModes(data), tree: await tree(target) })
  } catch (error) { archives.push({ error: (error as Error).message.replace(join(home, 'archives'), '$ARCHIVES'), code: (error as any).code ?? null, tree: await tree(target) }) }
}
const created = []
for (const [index, item] of input.create.entries()) {
  const source = join(home, 'create', String(index), 'source')
  await seed(source, item.seed)
  try {
    const data = await zipCache.createZipFromDirectory(source)
    const target = join(home, 'create', String(index), 'target')
    const path = `${target}.zip`
    await writeFile(path, data)
    await zipCache.extractZipToDirectory(path, target)
    created.push({ entries: await contents(data), modes: zip.parseZipModes(data), tree: await tree(target), archive: Buffer.from(data).toString('base64') })
  } catch (error) { created.push({ error: (error as Error).message, code: (error as any).code ?? null }) }
}
const validations = input.validations.map((item: any) => {
  const state = { ...item.state, errors: [] }
  const result = zip.validateZipFile(item.file, state)
  return { result, state }
})
const paths = input.paths.map((path: string) => zip.isPathSafe(path))
const names = input.names.map((name: string) => zipCache.getMarketplaceJsonRelativePath(name))
const supported = input.sources.map((source: string) => zipCache.isMarketplaceSourceSupportedByZipCache({ source } as any))
const environment = []
for (const item of input.environment) {
  process.env.CLAUDE_CODE_PLUGIN_USE_ZIP_CACHE = item.enabled
  if (item.path === null) delete process.env.CLAUDE_CODE_PLUGIN_CACHE_DIR
  else process.env.CLAUDE_CODE_PLUGIN_CACHE_DIR = item.path
  const value: Record<string, unknown> = { enabled: zipCache.isPluginZipCacheEnabled(), path: zipCache.getPluginZipCachePath() ?? null }
  try { value.known = zipCache.getZipCacheKnownMarketplacesPath(); value.installed = zipCache.getZipCacheInstalledPluginsPath(); value.marketplaces = zipCache.getZipCacheMarketplacesDir(); value.plugins = zipCache.getZipCachePluginsDir() }
  catch (error) { value.error = (error as Error).message }
  environment.push(value)
}
zipCache.resetSessionPluginCache()
const sessions = await Promise.all(Array.from({ length: 20 }, () => zipCache.getSessionPluginCachePath()))
const oneSession = new Set(sessions).size === 1
await zipCache.cleanupSessionPluginCache()
let removed = false
try { await lstat(sessions[0]) } catch { removed = true }
const nextSession = await zipCache.getSessionPluginCachePath()
const freshSession = nextSession !== sessions[0]
await zipCache.cleanupSessionPluginCache()
const converted = join(home, 'convert', 'source')
await seed(converted, [{ path: 'actual', kind: 'file', data: Buffer.from('retained').toString('base64'), mode: 0o755 }])
await zipCache.convertDirectoryToZipInPlace(converted, `${converted}.zip`)
let sourceRemoved = false
try { await lstat(converted) } catch { sourceRemoved = true }
const failedSource = join(home, 'failed-convert', 'source')
await seed(failedSource, [{ path: 'actual', kind: 'file', data: Buffer.from('must-remain').toString('base64'), mode: 0o755 }])
await mkdir(`${failedSource}.zip`, { recursive: true })
let conversionError: string | null = null
try { await zipCache.convertDirectoryToZipInPlace(failedSource, `${failedSource}.zip`) }
catch (error) { conversionError = (error as any).code ?? null }
const failedConvert = { code: conversionError, sourceTree: await tree(failedSource), siblings: (await readdir(join(home, 'failed-convert'))).sort() }
const atomicPath = join(home, 'atomic', 'owned.json')
await mkdir(join(home, 'atomic'), { recursive: true })
await writeFile(atomicPath, 'old')
await chmod(atomicPath, 0o600)
await zipCache.atomicWriteToZipCache(atomicPath, 'replacement')
const atomic = { data: await readFile(atomicPath, 'utf8'), mode: (await lstat(atomicPath)).mode & 0o777, siblings: (await readdir(join(home, 'atomic'))).sort() }
process.stdout.write(JSON.stringify({ archives, created, validations, paths, names, supported, environment, session: { oneSession, removed, freshSession }, convert: { sourceRemoved, entries: await contents(await readFile(`${converted}.zip`)) }, failedConvert, atomic }))
