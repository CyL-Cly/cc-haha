import fs from 'node:fs'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { assertDisposableFixtureHome } from './fixture-guard'

const mode = process.env.CC_HAHA_CONTROLLER_MODULE_CACHE
if (Bun.version !== '1.3.14' || !['cold', 'modules-only'].includes(mode ?? '')) throw new Error('Pinned owned controller module-cache diagnostic required')
assertDisposableFixtureHome(process.env.HOME, 'Actual original controller module cache')
const target = process.env.CC_HAHA_CONTROLLER_MODULE_CACHE_ORIGINAL
if (!target || !target.endsWith('/scripts/quality-gate/go-parity/session-controller.fixture.ts') || fs.realpathSync(target) !== target) throw new Error('Exact frozen original fixture path required')
const source = fs.readFileSync(target, 'utf8')
const anchor = 'if (input.warmCleanup) {'
if (source.split(anchor).length !== 2) throw new Error('Original fixture cleanup-cache admission changed')
// Only the test fixture is transformed. Original print, QueryEngine, writer,
// native filesystem guards and all three original module bodies stay intact.
// Importing modules invokes no cleanup/overlay/badge export and performs no
// previous cleanup turn. This isolates module caching from warmCleanup's state.
Bun.plugin({ name: 'actual-controller-module-only-cache-diagnostic', setup(build) {
  build.onLoad({ filter: /\/session-controller\.fixture\.ts$/ }, args => {
    if (fileURLToPath(pathToFileURL(args.path)) !== target) throw new Error('Foreign controller fixture intercepted')
    const unchanged = fs.readFileSync(args.path, 'utf8')
    if (unchanged !== source) throw new Error('Original controller fixture changed before loading')
    const prefix = `if (input.warmCleanup) throw new Error('Cleanup invocation is excluded from module-only diagnostic')
${mode === 'modules-only' ? `await import('../../../src/utils/computerUse/cleanup')
await import('../../../src/utils/computerUse/cuHelperDaemon')
await import('../../../src/utils/computerUse/winCursorBadge')
` : ''}`
    return { loader: 'ts', contents: unchanged.replace(anchor, prefix + anchor) }
  })
} })
await import(pathToFileURL(target).href)
