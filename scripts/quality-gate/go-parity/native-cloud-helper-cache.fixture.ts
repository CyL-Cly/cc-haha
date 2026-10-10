import { parse } from 'acorn'
import { assertDisposableFixtureHome } from './fixture-guard'

assertDisposableFixtureHome(process.env.HOME, 'Native cloud helper memo')
if (process.env.CC_HAHA_CLOUD_HELPER_FIXTURE !== '1') throw new Error('Unowned cloud helper fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(await Bun.file(new URL('../../../src/utils/memoize.ts', import.meta.url)).text())
const ast: any = parse(source, { ecmaVersion: 'latest', sourceType: 'module' })
const declaration = ast.body.map((node: any) => node.type === 'ExportNamedDeclaration' ? node.declaration : node).find((node: any) => node?.type === 'FunctionDeclaration' && node.id.name === 'memoizeWithTTLAsync')
if (!declaration) throw new Error('Original memoizeWithTTLAsync missing')
const make = new Function('jsonStringify', 'Date', 'logError', source.slice(declaration.start, declaration.end) + ';return memoizeWithTTLAsync')
const rows = []
for (const row of input.rows) {
  let now = 1000, calls = 0
  const errors: string[] = []
  const engine = make(JSON.stringify, { now: () => now }, (error: Error) => errors.push(error.message))(async () => {
    const response = row.responses[calls++]
    if (response.error) throw new Error(response.error)
    return response.value
  }, 3600000)
  const steps = []
  for (const action of row.actions) {
    if (action.now !== undefined) now = action.now
    if (action.clear) engine.cache.clear()
    let value = null, error = null
    try { value = await engine() } catch (failure: any) { error = failure.message }
    await new Promise(resolve => setImmediate(resolve))
    steps.push({ value, error, calls })
  }
  rows.push({ steps, errors })
}
const concurrent = []
for (const profile of ['cold-clear', 'stale-clear', 'stale-error-clear']) {
  let now = 1000, calls = 0
  const errors: string[] = []
  const deferred: Array<{ resolve: (value: any) => void; reject: (value: any) => void }> = []
  const engine = make(JSON.stringify, { now: () => now }, (error: Error) => errors.push(error.message))(() => {
    calls++
    return new Promise((resolve, reject) => deferred.push({ resolve, reject }))
  }, 3600000)
  const first = engine()
  let old: Promise<any>, duplicate: Promise<any> | undefined
  if (profile === 'cold-clear') { old = first; duplicate = engine() }
  else { deferred[0].resolve('initial'); await first; now = 3601001; await engine(); old = Promise.resolve('initial') }
  engine.cache.clear()
  const fresh = engine(), last = deferred.at(-1)!
  last.resolve('fresh')
  const freshResult = await fresh
  const prior = deferred[profile === 'cold-clear' ? 0 : 1]
  if (profile === 'stale-error-clear') prior.reject(new Error('orphan stale failure'))
  else prior.resolve('older')
  const oldResult = await old
  const duplicateResult = duplicate ? await duplicate : null
  await new Promise(resolve => setImmediate(resolve))
  concurrent.push({ profile, freshResult, oldResult, duplicateResult, cached: await engine(), calls, errors })
}
process.stdout.write(JSON.stringify({ rows, concurrent }))
