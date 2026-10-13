import { chmodSync, copyFileSync, mkdirSync, readFileSync, statSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { cliBinaryName } from './build'

const targets: Record<string, { target: string; bundle?: string }> = {
  'aarch64-apple-darwin': { target: 'darwin/arm64', bundle: 'mac-arm64' },
  'x86_64-apple-darwin': { target: 'darwin/amd64', bundle: 'mac' },
  'x86_64-unknown-linux-gnu': { target: 'linux/amd64' },
  'aarch64-unknown-linux-gnu': { target: 'linux/arm64' },
  'x86_64-pc-windows-msvc': { target: 'windows/amd64', bundle: 'win-unpacked' },
  'aarch64-pc-windows-msvc': { target: 'windows/arm64', bundle: 'win-arm64-unpacked' },
}

// Copy the final packaged sidecar, after macOS/SignPath signing. Rebuilding it
// here would discard the executable identity used by Computer Use attestation.
export function stageCLIReleaseAsset(root: string, triple: string, outputDirectory: string) {
  const settings = targets[triple]
  if (!settings) throw new Error(`Unsupported CLI release target triple: ${triple}`)
  const sidecar = `claude-sidecar-${triple}${settings.target.startsWith('windows/') ? '.exe' : ''}`
  const desktop = join(root, 'desktop')
  let source: string
  if (settings.target.startsWith('darwin/')) {
    const product = JSON.parse(readFileSync(join(desktop, 'package.json'), 'utf8')).build.productName
    source = join(desktop, 'build-artifacts/electron', settings.bundle!, `${product}.app`, 'Contents/Resources/app.asar.unpacked/src-tauri/binaries', sidecar)
  } else if (settings.target.startsWith('windows/')) {
    source = join(desktop, 'build-artifacts/electron', settings.bundle!, 'resources/app.asar.unpacked/src-tauri/binaries', sidecar)
  } else {
    source = join(desktop, 'src-tauri/binaries', sidecar)
  }
  const stat = statSync(source)
  if (!stat.isFile() || stat.size === 0) throw new Error(`Missing final packaged Go sidecar: ${source}`)
  const output = join(outputDirectory, cliBinaryName(settings.target))
  mkdirSync(outputDirectory, { recursive: true })
  copyFileSync(source, output)
  chmodSync(output, 0o755)
  return { source, output, target: settings.target }
}

if (import.meta.main) {
  const args = process.argv.slice(2)
  if (args.length !== 2) throw new Error('Usage: stage-release.ts <target-triple> <output-directory>')
  console.log(stageCLIReleaseAsset(resolve(import.meta.dir, '../..'), args[0], resolve(args[1])).output)
}
