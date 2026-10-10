// @vitest-environment node

import path from 'node:path'
import { mkdtemp, readFile, rm, stat, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { describe, expect, it } from 'vitest'
import { assertGoSidecarEnvironment, sidecarBinaryName } from '../electron/services/sidecarBackend'
import { createGoSidecarBuildPlan, mapTargetTripleToGo, removeLegacyBusinessSidecars } from './go-sidecar-build'

describe('Go sidecar build target', () => {
  it('removes stale TS business binaries from packaging and preserves Go and native helpers', async () => {
    const directory = await mkdtemp(path.join(tmpdir(), 'cc-haha-go-package-fixture-'))
    try {
      const obsolete = ['claude-sidecar-aarch64-apple-darwin', 'claude-sidecar-x86_64-pc-windows-msvc.exe']
      const retained = ['claude-sidecar-go-aarch64-apple-darwin', 'cc-haha-computer-use', 'rg-aarch64-apple-darwin', 'fixture-worker']
      for (const name of [...obsolete, ...retained]) await writeFile(path.join(directory, name), 'fixture')
      await removeLegacyBusinessSidecars(directory)
      for (const name of obsolete) await expect(stat(path.join(directory, name))).rejects.toMatchObject({ code: 'ENOENT' })
      for (const name of retained) expect(await readFile(path.join(directory, name), 'utf8')).toBe('fixture')
      await expect(removeLegacyBusinessSidecars(directory)).resolves.toBeUndefined()
    } finally {
      await rm(directory, { recursive: true, force: true })
    }
  })
  it.each([
    ['aarch64-apple-darwin', 'darwin', 'arm64'],
    ['x86_64-apple-darwin', 'darwin', 'amd64'],
    ['x86_64-pc-windows-msvc', 'windows', 'amd64'],
    ['aarch64-pc-windows-msvc', 'windows', 'arm64'],
    ['x86_64-unknown-linux-gnu', 'linux', 'amd64'],
    ['aarch64-unknown-linux-gnu', 'linux', 'arm64'],
    ['x86_64-unknown-linux-musl', 'linux', 'amd64'],
    ['aarch64-unknown-linux-musl', 'linux', 'arm64'],
  ])('cross-builds %s without CGO or an ambient architecture override', (triple, goos, goarch) => {
    const plan = createGoSidecarBuildPlan({
      repoRoot: '/checkout', binariesDir: '/package/binaries', triple,
      version: '0.6.7', telemetryDir: '/private/telemetry',
      env: { GOOS: 'wrong', GOARCH: 'wrong', CGO_ENABLED: '1', GOAMD64: 'v4', GOARM64: 'v9.5' },
    })
    expect(plan.command).toBe('go')
    expect(plan.cwd).toBe(path.join('/checkout', 'backend'))
    expect(plan.args).toEqual(['build', '-trimpath', '-ldflags', '-X main.version=0.6.7', '-o', plan.outputPath, './cmd/claude-haha'])
    expect(plan.outputPath).toBe(path.join('/package/binaries', `claude-sidecar-go-${triple}${goos === 'windows' ? '.exe' : ''}`))
    expect(plan.env).toMatchObject({ GOOS: goos, GOARCH: goarch, CGO_ENABLED: '0', GOAMD64: 'v1', GOARM64: 'v8.0', GOENV: 'off', GOTOOLCHAIN: 'local', GOFLAGS: '-mod=readonly', GOTELEMETRY: 'off', TEST_TELEMETRY_DIR: '/private/telemetry' })
  })

  it('uses an executable argument, including paths with spaces, instead of a shell command', () => {
    const plan = createGoSidecarBuildPlan({
      repoRoot: '/checkout', binariesDir: '/package/binaries', triple: 'aarch64-apple-darwin',
      version: '999.0.0-local', telemetryDir: '/private/telemetry',
      env: { CC_HAHA_GO_EXECUTABLE: '/tools/Go Runtime/go', PATH: '/tools' },
    })
    expect(plan.command).toBe('/tools/Go Runtime/go')
    expect(plan.env.PATH).toBe('/tools')
  })

  it('fails unknown targets and invalid version linker values', () => {
    expect(() => mapTargetTripleToGo('unsupported')).toThrow('Unsupported Go target triple')
    for (const version of ['', '-bad', '1.2.3 -X other.variable=value']) {
      expect(() => createGoSidecarBuildPlan({
        repoRoot: '/checkout', binariesDir: '/package/binaries', triple: 'aarch64-apple-darwin',
        version, telemetryDir: '/private/telemetry', env: {},
      })).toThrow('version identifier')
    }
  })

  it('uses Go by default and rejects legacy production backend selection', () => {
    expect(() => assertGoSidecarEnvironment({})).not.toThrow()
    expect(() => assertGoSidecarEnvironment({ CC_HAHA_BACKEND: 'go' })).not.toThrow()
    for (const value of ['typescript', '', 'Go', 'go ', 'auto']) {
      expect(() => assertGoSidecarEnvironment({ CC_HAHA_BACKEND: value })).toThrow('The desktop backend is Go')
    }
    expect(sidecarBinaryName('x86_64-pc-windows-msvc')).toBe('claude-sidecar-go-x86_64-pc-windows-msvc.exe')
  })
})
