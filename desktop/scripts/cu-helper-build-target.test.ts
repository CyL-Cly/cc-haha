// @vitest-environment node

import { describe, expect, it } from 'vitest'
import { mkdir, mkdtemp, readFile, rm, stat, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import {
  createCuHelperBuildEnv,
  resolveCuHelperArch,
  removeStagedCuHelperArtifacts,
} from './cu-helper-build-target'

describe('cu-helper release target', () => {
  it('maps both macOS release triples to their native Swift architecture', () => {
    expect(resolveCuHelperArch('aarch64-apple-darwin')).toBe('arm64')
    expect(resolveCuHelperArch('x86_64-apple-darwin')).toBe('x86_64')
  })

  it('overrides a stale host architecture while preserving the signing environment', () => {
    expect(createCuHelperBuildEnv('x86_64-apple-darwin', {
      CU_HELPER_ARCH: 'arm64',
      CU_HELPER_IDENTITY: 'Apple Development: Stale Identity',
      CC_HAHA_SIGN_IDENTITY: 'Developer ID Application: Example',
      PATH: '/usr/bin',
    })).toMatchObject({
      CU_HELPER_ARCH: 'x86_64',
      CU_HELPER_IDENTITY: 'Developer ID Application: Example',
      CC_HAHA_SIGN_IDENTITY: 'Developer ID Application: Example',
      PATH: '/usr/bin',
    })
  })

  it('drops a helper-only identity when no build-wide identity was selected', () => {
    expect(createCuHelperBuildEnv('aarch64-apple-darwin', {
      CU_HELPER_IDENTITY: 'Apple Development: Stale Identity',
    }).CU_HELPER_IDENTITY).toBeUndefined()
  })

  it('skips non-macOS targets and rejects unknown Apple architectures', () => {
    expect(resolveCuHelperArch('x86_64-pc-windows-msvc')).toBeNull()
    expect(resolveCuHelperArch('aarch64-unknown-linux-gnu')).toBeNull()
    expect(() => resolveCuHelperArch('armv7-apple-darwin')).toThrow(
      'unsupported macOS cu-helper target',
    )
  })
})


describe('explicit non-CU generated artifacts', () => {
  it('removes only the five declared generated helper paths and keeps ordinary sidecars intact', async () => {
    const root = await mkdtemp(path.join(tmpdir(), 'excluded-cu-artifacts-'))
    try {
      const helperNames = [
        'cc-haha-computer-use.app', 'cc-haha-computer-use', 'cu-helper',
        'cu-helper_cc-haha-computer-use.bundle', 'cu-helper_cu-helper.bundle',
      ]
      for (const name of helperNames) {
        await mkdir(path.join(root, name))
        await writeFile(path.join(root, name, 'generated'), name)
      }
      await writeFile(path.join(root, 'claude-sidecar-go-aarch64-apple-darwin'), 'same Go binary')
      await writeFile(path.join(root, 'rg'), 'same ripgrep')
      await writeFile(path.join(root, 'unrelated'), 'keep')
      await removeStagedCuHelperArtifacts(root)
      for (const name of helperNames) await expect(stat(path.join(root, name))).rejects.toMatchObject({ code: 'ENOENT' })
      expect(await readFile(path.join(root, 'claude-sidecar-go-aarch64-apple-darwin'), 'utf8')).toBe('same Go binary')
      expect(await readFile(path.join(root, 'rg'), 'utf8')).toBe('same ripgrep')
      expect(await readFile(path.join(root, 'unrelated'), 'utf8')).toBe('keep')
    } finally {
      await rm(root, { recursive: true, force: true })
    }
  })
})
