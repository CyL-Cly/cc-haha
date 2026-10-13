import { expect, test } from 'bun:test'
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { stageCLIReleaseAsset } from './stage-release'

test('CLI releases preserve final macOS and Windows signatures instead of rebuilding sidecars', () => {
  const root = mkdtempSync(join(tmpdir(), 'go-cli-stage-'))
  try {
    mkdirSync(join(root, 'desktop'), { recursive: true })
    writeFileSync(join(root, 'desktop/package.json'), JSON.stringify({ build: { productName: 'Claude Code Haha' } }))
    const profiles = [
      ['aarch64-apple-darwin', 'build-artifacts/electron/mac-arm64/Claude Code Haha.app/Contents/Resources/app.asar.unpacked/src-tauri/binaries/claude-sidecar-aarch64-apple-darwin', 'claude-haha-darwin-arm64'],
      ['x86_64-apple-darwin', 'build-artifacts/electron/mac/Claude Code Haha.app/Contents/Resources/app.asar.unpacked/src-tauri/binaries/claude-sidecar-x86_64-apple-darwin', 'claude-haha-darwin-amd64'],
      ['x86_64-pc-windows-msvc', 'build-artifacts/electron/win-unpacked/resources/app.asar.unpacked/src-tauri/binaries/claude-sidecar-x86_64-pc-windows-msvc.exe', 'claude-haha-windows-amd64.exe'],
      ['aarch64-pc-windows-msvc', 'build-artifacts/electron/win-arm64-unpacked/resources/app.asar.unpacked/src-tauri/binaries/claude-sidecar-aarch64-pc-windows-msvc.exe', 'claude-haha-windows-arm64.exe'],
      ['x86_64-unknown-linux-gnu', 'src-tauri/binaries/claude-sidecar-x86_64-unknown-linux-gnu', 'claude-haha-linux-amd64'],
      ['aarch64-unknown-linux-gnu', 'src-tauri/binaries/claude-sidecar-aarch64-unknown-linux-gnu', 'claude-haha-linux-arm64'],
    ]
    for (const [triple, relative, name] of profiles) {
      const source = join(root, 'desktop', relative)
      const signedPayload = Buffer.from(`physical final packaged native fixture\0signature:${triple}\xff`, 'utf8')
      mkdirSync(dirname(source), { recursive: true })
      writeFileSync(source, signedPayload)
      const result = stageCLIReleaseAsset(root, triple, join(root, 'release'))
      expect(result.source).toBe(source)
      expect(result.output).toBe(join(root, 'release', name))
      expect(readFileSync(result.output)).toEqual(signedPayload)
      expect(readFileSync(source)).toEqual(signedPayload)
    }
    expect(() => stageCLIReleaseAsset(root, 'unrecognized', join(root, 'release'))).toThrow('Unsupported')
  } finally { rmSync(root, { recursive: true, force: true }) }
})

test('missing final signed application cannot fall back to an unsigned intermediate sidecar', () => {
  const root = mkdtempSync(join(tmpdir(), 'go-cli-stage-missing-'))
  try {
    const desktop = join(root, 'desktop')
    mkdirSync(join(desktop, 'src-tauri/binaries'), { recursive: true })
    writeFileSync(join(desktop, 'package.json'), JSON.stringify({ build: { productName: 'Claude Code Haha' } }))
    writeFileSync(join(desktop, 'src-tauri/binaries/claude-sidecar-x86_64-pc-windows-msvc.exe'), 'unsigned intermediate')
    expect(() => stageCLIReleaseAsset(root, 'x86_64-pc-windows-msvc', join(root, 'release'))).toThrow()
  } finally { rmSync(root, { recursive: true, force: true }) }
})
