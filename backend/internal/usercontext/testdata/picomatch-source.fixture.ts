import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_PICOMATCH_FIXTURE !== '1') throw new Error('Pure matcher requires pinned fixture runtime')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.home, 'Picomatch')
if (Object.keys(input).some(key => !['home', 'cases'].includes(key))) throw new Error('Only original match inputs')
const library = (await import('picomatch')).default
const manifest = await import('../../../../node_modules/picomatch/package.json')
if (manifest.version !== '4.0.4') throw new Error('Picomatch must match locked Source version')
process.stdout.write(JSON.stringify(input.cases.map((value: { path: string, patterns: string[], platform: string }) => library.isMatch(value.path.replaceAll('\\', '/'), value.patterns.map(pattern => pattern.replaceAll('\\', '/')), { dot: true, windows: value.platform === 'win32' }))))
