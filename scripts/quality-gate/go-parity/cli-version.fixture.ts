import fs from 'node:fs'
import { join, relative } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as { home: string; args: string[] }
const home = assertDisposableFixtureHome(input.home, 'CLI version')
const capabilities = await installClientFixtureCapabilities(home)
const loaded: string[] = []
// Bun 1.3.14 requires onLoad to return content. The observer returns the exact
// original file bytes with the matching loader; no production code is changed.
Bun.plugin({ name: 'cli-version-import-observer', setup(build) {
  build.onLoad({ filter: /[\\/]src[\\/].*\.(?:ts|tsx|js)$/ }, args => {
    loaded.push(relative(new URL('../../../', import.meta.url).pathname, args.path))
    return {
      contents: fs.readFileSync(args.path, 'utf8'),
      loader: args.path.endsWith('.tsx') ? 'tsx' : args.path.endsWith('.ts') ? 'ts' : 'js',
    }
  })
} })
process.on('exit', code => fs.writeFileSync(join(home, 'version-evidence.json'), JSON.stringify({ code, loaded, ...capabilities })))
process.argv = [process.execPath, new URL('../../../src/entrypoints/cli.tsx', import.meta.url).pathname, ...input.args]
await import('../../../preload')
await import('../../../src/entrypoints/cli')
