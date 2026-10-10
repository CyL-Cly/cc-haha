import { mkdirSync, realpathSync, statSync } from 'node:fs'
import path from 'node:path'
import type { App } from 'electron'

type SmokeApp = Pick<App, 'setPath'> & {
  commandLine: Pick<App['commandLine'], 'getSwitchValue' | 'appendSwitch'>
}

function contained(root: string, candidate: string): boolean {
  const relative = path.relative(root, candidate)
  return relative === '' || (!relative.startsWith(`..${path.sep}`) && relative !== '..' && !path.isAbsolute(relative))
}

function privatePath(root: string, value: string | undefined, label: string): string {
  if (!value || !path.isAbsolute(value)) throw new Error(`[desktop-smoke] ${label} must be an explicit absolute path`)
  let ancestor = path.resolve(value)
  const suffix: string[] = []
  while (true) {
    try {
      const resolved = path.join(realpathSync(ancestor), ...suffix)
      if (!contained(root, resolved)) throw new Error(`[desktop-smoke] ${label} must stay inside the smoke state root`)
      return resolved
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error
      const parent = path.dirname(ancestor)
      if (parent === ancestor) throw error
      suffix.unshift(path.basename(ancestor))
      ancestor = parent
    }
  }
}

/** opt-in fixture isolation, before Electron or a business service opens state. */
export function isolateElectronSmokeState(app: SmokeApp, env: NodeJS.ProcessEnv = process.env): boolean {
  const requestedRoot = env.CC_HAHA_ELECTRON_SMOKE_STATE_ROOT
  if (!requestedRoot) return false
  if (!path.isAbsolute(requestedRoot) || !env.CC_HAHA_ELECTRON_WINDOW_SMOKE_LOG) {
    throw new Error('[desktop-smoke] state isolation requires an absolute root and the window smoke mode')
  }
  const root = realpathSync(requestedRoot)
  const stat = statSync(root)
  if (!stat.isDirectory() || path.dirname(root) === root
    || (process.platform !== 'win32' && ((stat.mode & 0o077) !== 0 || stat.uid !== process.getuid?.()))) {
    throw new Error('[desktop-smoke] state root must be an existing private directory owned by the current user')
  }

  privatePath(root, env.CC_HAHA_ELECTRON_WINDOW_SMOKE_LOG, 'window smoke log')
  privatePath(root, env.CLAUDE_CONFIG_DIR, 'CLAUDE_CONFIG_DIR')
  const home = privatePath(root, env.HOME || env.USERPROFILE, 'HOME')
  const userData = privatePath(root, app.commandLine.getSwitchValue('user-data-dir'), '--user-data-dir')
  const paths = {
    home,
    userData,
    sessionData: userData,
    appData: path.join(root, 'app-data'),
    cache: path.join(root, 'cache'),
    logs: path.join(root, 'logs'),
    temp: path.join(root, 'tmp'),
  } as const
  // Validate every existing ancestor before creating or setting any path.
  for (const [name, value] of Object.entries(paths)) privatePath(root, value, name)
  for (const [name, value] of Object.entries(paths)) {
    mkdirSync(value, { recursive: true, mode: 0o700 })
    app.setPath(name as keyof typeof paths, value)
  }
  app.commandLine.appendSwitch('disk-cache-dir', paths.cache)
  app.commandLine.appendSwitch('log-file', path.join(paths.logs, 'chromium.log'))
  return true
}
