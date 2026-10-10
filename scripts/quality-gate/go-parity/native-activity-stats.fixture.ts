import { assertDisposableFixtureHome } from './fixture-guard'
import { appendFileSync, existsSync, mkdirSync, readFileSync, rmSync, truncateSync, utimesSync, writeFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'
import { parse } from 'acorn'

const home = assertDisposableFixtureHome(process.env.HOME, 'Activity stats Source comparison')
if (process.env.CC_HAHA_ACTIVITY_FIXTURE !== '1') throw new Error('Missing activity fixture marker')
const config = resolve(process.env.CLAUDE_CONFIG_DIR!)
if (config !== join(home, '.claude')) throw new Error('Unexpected activity fixture configuration')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const OriginalDate = Date
let timestamp = new OriginalDate(input.now).valueOf()
class FixedDate extends OriginalDate {
  constructor(...args: [] | [string | number]) { super(args.length === 0 ? timestamp : args[0]!) }
  static now() { return timestamp }
}
globalThis.Date = FixedDate as DateConstructor
await import('../../../preload')
const { aggregateClaudeCodeStatsForRange } = await import('../../../src/utils/stats')
const { handleActivityStatsApi } = await import('../../../src/server/api/activityStats')
const { localIndexCoordinator } = await import('../../../src/server/services/localIndex/coordinator')
// The original file scanner executes; the optional local-index coordinator
// primitive is held off so actual private transcripts are the source of truth.
localIndexCoordinator.getMode = () => 'off'
const observations = []
let shotAggregator: typeof aggregateClaudeCodeStatsForRange | undefined
async function withShotFeature() {
  if (shotAggregator) return shotAggregator
  const entry = Bun.resolveSync('../../../src/utils/stats', import.meta.dir)
  const cache = Bun.resolveSync('../../../src/utils/statsCache', import.meta.dir)
  // bun:bundle.feature is a compiler intrinsic, so module mocks do not enable
  // it. Compile the actual two Source files with its actual public feature API;
  // all other imports resolve to the untouched original modules.
  const built = await Bun.build({
    entrypoints: [entry], target: 'bun', features: ['SHOT_STATS'],
    plugins: [{ name: 'original-activity-dependencies', setup(builder) {
      builder.onResolve({ filter: /.*/ }, args => {
        if (args.importer !== entry && args.importer !== cache) return
        if (args.path === entry || args.path === 'bun:bundle') return
        if (args.path.startsWith('.')) {
          const path = Bun.resolveSync(args.path, args.resolveDir || dirname(args.importer))
          return { path, external: path !== cache }
        }
        return { path: args.path, external: true }
      })
    } }],
  })
  if (!built.success) throw new Error(built.logs.join('\n'))
  let source = await built.outputs[0]!.text()
  // Bun keeps external specifier spelling in output. Both compiled originals
  // live in utils/, so relocate only AST import literals to their real paths.
  const ast = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
  for (const node of [...ast.body].reverse()) {
    if (node.type === 'ImportDeclaration' && typeof node.source.value === 'string' && node.source.value.startsWith('.')) {
      const dependency = Bun.resolveSync(node.source.value, dirname(entry))
      source = source.slice(0, node.source.start) + JSON.stringify(dependency) + source.slice(node.source.end)
    }
  }
  const directory = join(home, 'actual-shot-build')
  mkdirSync(directory, { recursive: true })
  const path = join(directory, 'stats.mjs')
  await Bun.write(path, source)
  if (!existsSync(path)) throw new Error('Source build file was not written')
  shotAggregator = (await import(pathToFileURL(path).href + '?feature=SHOT_STATS')).aggregateClaudeCodeStatsForRange
  return shotAggregator!
}
function readCache() {
  const path = join(config, 'stats-cache.json')
  if (!existsSync(path)) return null
  const text = readFileSync(path, 'utf8')
  try { return JSON.parse(text) } catch { return text }
}
for (const row of input.cases) {
  if (!row.continue) {
    rmSync(config, { force: true, recursive: true })
    mkdirSync(config, { recursive: true })
    writeFileSync(join(config, '.claude.json'), '{}')
  }
  if (row.now) timestamp = new OriginalDate(row.now).valueOf()
  if (row.cache !== undefined) writeFileSync(join(config, 'stats-cache.json'), typeof row.cache === 'string' ? row.cache : JSON.stringify(row.cache))
  for (const file of row.files ?? []) {
    const path = resolve(config, 'projects', file.path)
    if (!path.startsWith(join(config, 'projects') + '/')) throw new Error('Unsafe activity fixture path')
    mkdirSync(resolve(path, '..'), { recursive: true })
    writeFileSync(path, file.raw ?? file.entries.map((entry: unknown) => JSON.stringify(entry)).join('\n') + '\n')
    if (file.prefixBytes) {
      const tail = readFileSync(path)
      truncateSync(path, file.prefixBytes)
      appendFileSync(path, Buffer.concat([Buffer.from('\n'), tail]))
    }
    const date = new OriginalDate(file.mtime ?? input.now)
    utimesSync(path, date, date)
  }
  try {
    if (row.action === 'pricingCatalog') {
      const path = Bun.resolveSync('../../../src/utils/usageAccounting', import.meta.dir)
      const js = new Bun.Transpiler({ loader: 'ts', target: 'bun' }).transformSync(readFileSync(path, 'utf8'))
      const ast = parse(js, { ecmaVersion: 'latest', sourceType: 'module' })
      const catalogs: Record<string, string[]> = {}
      for (const node of ast.body) {
        if (node.type !== 'VariableDeclaration') continue
        for (const declaration of node.declarations) {
          if (declaration.id.type !== 'Identifier' || !['MODEL_TIERS', 'FAST_MODE_TIERS'].includes(declaration.id.name)) continue
          if (declaration.init?.type !== 'ArrayExpression') throw new Error('Unexpected original pricing catalog shape')
          catalogs[declaration.id.name] = declaration.init.elements.map(element => {
            if (element?.type !== 'ArrayExpression' || element.elements[0]?.type !== 'Literal' || typeof element.elements[0].value !== 'string') throw new Error('Unexpected original price tier shape')
            return element.elements[0].value
          })
        }
      }
      observations.push({ value: catalogs })
    } else if (row.api) {
      const url = new URL(row.path ?? '/api/activity-stats', 'http://127.0.0.1:3000')
      const response = await handleActivityStatsApi(new Request(url, { method: row.method ?? 'GET' }), url, url.pathname.split('/').filter(Boolean))
      observations.push({ status: response.status, body: await response.json(), cache: readCache() })
    } else {
      const aggregate = row.shots === true ? await withShotFeature() : aggregateClaudeCodeStatsForRange
      const value = await aggregate(row.range ?? 'all', { now: new FixedDate() })
      observations.push({ value, cache: readCache() })
    }
  } catch (error) {
    observations.push({ error: { name: (error as Error).name, message: (error as Error).message }, cache: readCache() })
  }
}
process.stdout.write(JSON.stringify(observations))
