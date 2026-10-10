import { afterEach, describe, expect, it, vi } from 'vitest'
import { chmodSync, mkdtempSync, mkdirSync, rmSync, symlinkSync } from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { isolateElectronSmokeState } from './smokeState'

const roots: string[] = []
afterEach(() => { for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true }) })

function fixture() {
  const root = mkdtempSync(path.join(os.tmpdir(), 'electron-state-regression-'))
  roots.push(root)
  const app = {
    setPath: vi.fn(),
    commandLine: {
      getSwitchValue: vi.fn(() => path.join(root, 'profile')),
      appendSwitch: vi.fn(),
    },
  }
  const env = {
    CC_HAHA_ELECTRON_SMOKE_STATE_ROOT: root,
    CC_HAHA_ELECTRON_WINDOW_SMOKE_LOG: path.join(root, 'window.jsonl'),
    CLAUDE_CONFIG_DIR: path.join(root, 'config'),
    HOME: path.join(root, 'home'),
  }
  return { root, app, env }
}

describe('Electron smoke state isolation', () => {
  it('leaves normal launches untouched and never reads their paths', () => {
    const { app } = fixture()
    expect(isolateElectronSmokeState(app, {})).toBe(false)
    expect(app.setPath).not.toHaveBeenCalled()
    expect(app.commandLine.getSwitchValue).not.toHaveBeenCalled()
  })

  it('sets every native state path from the explicit fixture, including HOME and Chromium cache', () => {
    const { root, app, env } = fixture()
    expect(isolateElectronSmokeState(app, env)).toBe(true)
    const canonical = path.dirname(app.setPath.mock.calls.find(([name]) => name === 'appData')![1])
    expect(app.setPath.mock.calls).toEqual([
      ['home', path.join(canonical, 'home')], ['userData', path.join(canonical, 'profile')],
      ['sessionData', path.join(canonical, 'profile')], ['appData', path.join(canonical, 'app-data')],
      ['cache', path.join(canonical, 'cache')], ['logs', path.join(canonical, 'logs')], ['temp', path.join(canonical, 'tmp')],
    ])
    expect(app.commandLine.appendSwitch).toHaveBeenCalledWith('disk-cache-dir', path.join(canonical, 'cache'))
    expect(app.commandLine.appendSwitch).toHaveBeenCalledWith('log-file', path.join(canonical, 'logs/chromium.log'))
    expect(env.HOME).toBe(path.join(root, 'home'))
  })

  it.each(['HOME', 'CLAUDE_CONFIG_DIR', 'CC_HAHA_ELECTRON_WINDOW_SMOKE_LOG'] as const)('rejects external %s before setting any path', field => {
    const { app, env } = fixture()
    env[field] = path.join(os.tmpdir(), 'external-smoke-state')
    expect(() => isolateElectronSmokeState(app, env)).toThrow('must stay inside')
    expect(app.setPath).not.toHaveBeenCalled()
  })

  it('requires existing window smoke mode and an absolute private directory', () => {
    const { root, app, env } = fixture()
    expect(() => isolateElectronSmokeState(app, { ...env, CC_HAHA_ELECTRON_WINDOW_SMOKE_LOG: undefined })).toThrow('window smoke mode')
    expect(() => isolateElectronSmokeState(app, { ...env, CC_HAHA_ELECTRON_SMOKE_STATE_ROOT: 'relative' })).toThrow('absolute root')
    if (process.platform !== 'win32') {
      chmodSync(root, 0o755)
      expect(() => isolateElectronSmokeState(app, env)).toThrow('private directory')
    }
    expect(app.setPath).not.toHaveBeenCalled()
  })

  it('rejects a nested symlink escape before any native path mutation', () => {
    const { root, app, env } = fixture()
    const outside = mkdtempSync(path.join(os.tmpdir(), 'electron-external-regression-'))
    roots.push(outside)
    mkdirSync(path.join(root, 'home'))
    symlinkSync(outside, path.join(root, 'cache'), process.platform === 'win32' ? 'junction' : 'dir')
    expect(() => isolateElectronSmokeState(app, env)).toThrow('cache must stay inside')
    expect(app.setPath).not.toHaveBeenCalled()
  })
})
