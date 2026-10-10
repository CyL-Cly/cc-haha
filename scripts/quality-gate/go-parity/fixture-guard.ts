import { existsSync, lstatSync, realpathSync } from 'node:fs'
import { isAbsolute, join, relative, sep } from 'node:path'

/** Validate the disposable child boundary before loading any production graph. */
export function assertDisposableFixtureHome(value: unknown, label: string): string {
  const fail = () => { throw new Error(`${label} fixture requires disposable HOME/config before production imports`) }
  if (typeof value !== 'string' || !value || !isAbsolute(value) || value.includes('\0') || value !== process.env.HOME) return fail()
  const home = value
  if (!existsSync(home) || lstatSync(home).isSymbolicLink() || !lstatSync(home).isDirectory()) return fail()
  const canonical = realpathSync(home)
  // Child TMPDIR is inside HOME. The trusted parent records the allocation
  // root before replacing TMPDIR; recomputing tmpdir() here rejects legitimate
  // macOS /var -> /private/var homes and nested corpus sandboxes.
  const allocationRoot = process.env.CC_HAHA_FIXTURE_TEMP_ROOT
  if (!allocationRoot || !isAbsolute(allocationRoot) || !existsSync(allocationRoot) || !lstatSync(allocationRoot).isDirectory()) return fail()
  if (!canonical.startsWith(`${realpathSync(allocationRoot)}${sep}`) || realpathSync(process.cwd()) !== canonical) return fail()
  const config = join(home, '.claude')
  if (process.env.CLAUDE_CONFIG_DIR !== config || process.env.USERPROFILE !== home || process.env.BUN_OPTIONS !== '--no-env-file') return fail()
  let configStat
  try { configStat = lstatSync(config) } catch (error) { if ((error as NodeJS.ErrnoException).code !== 'ENOENT') return fail() }
  if (configStat) {
    if (configStat.isSymbolicLink() || !configStat.isDirectory()) return fail()
    const path = relative(canonical, realpathSync(config))
    if (path === '..' || path.startsWith(`..${sep}`) || isAbsolute(path)) return fail()
  }
  return home
}
