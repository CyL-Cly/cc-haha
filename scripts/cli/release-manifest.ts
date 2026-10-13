import { createHash } from 'node:crypto'
import { readFileSync, statSync, writeFileSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { CLI_TARGETS, cliBinaryName } from './build'

export const CLI_MANIFEST_NAME = 'claude-haha-cli-manifest.json'

export function writeCLIReleaseManifest(directory: string, version: string) {
  if (!/^\d+\.\d+\.\d+(-\S+)?$/.test(version)) throw new Error('CLI release manifest requires a release version')
  const assets: Record<string, { sha256: string; size: number }> = {}
  for (const target of CLI_TARGETS) {
    const name = cliBinaryName(target)
    const path = join(directory, name)
    const stat = statSync(path)
    if (!stat.isFile() || stat.size <= 0 || stat.size > 512 * 1024 * 1024) throw new Error(`Invalid Go CLI release asset: ${name}`)
    assets[name] = { sha256: createHash('sha256').update(readFileSync(path)).digest('hex'), size: stat.size }
  }
  const manifest = { version, runtime: 'go', assets }
  const output = join(directory, CLI_MANIFEST_NAME)
  writeFileSync(output, `${JSON.stringify(manifest, null, 2)}\n`)
  return { output, manifest }
}

if (import.meta.main) {
  const args = process.argv.slice(2)
  if (args.length !== 2) throw new Error('Usage: release-manifest.ts <asset-directory> <version>')
  console.log(writeCLIReleaseManifest(resolve(args[0]), args[1]).output)
}
