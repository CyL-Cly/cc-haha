import { afterEach, expect, test } from 'bun:test'
import { createHash } from 'node:crypto'
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { CLI_TARGETS, cliBinaryName } from './build'
import { CLI_MANIFEST_NAME, writeCLIReleaseManifest } from './release-manifest'

const directories: string[] = []
afterEach(() => { for (const directory of directories.splice(0)) rmSync(directory, { recursive: true, force: true }) })

test('release manifest covers all physical platform assets with exact download checksums', () => {
  const directory = mkdtempSync(join(tmpdir(), 'go-cli-release-'))
  directories.push(directory)
  const expected: Record<string, { sha256: string; size: number }> = {}
  for (const target of CLI_TARGETS) {
    const payload = Buffer.from(`physical native asset fixture for ${target}\n`)
    const name = cliBinaryName(target)
    writeFileSync(join(directory, name), payload)
    expected[name] = { sha256: createHash('sha256').update(payload).digest('hex'), size: payload.length }
  }
  const result = writeCLIReleaseManifest(directory, '1.2.3-dev.fixture')
  expect(result.output).toBe(join(directory, CLI_MANIFEST_NAME))
  expect(JSON.parse(readFileSync(result.output, 'utf8'))).toEqual({ version: '1.2.3-dev.fixture', runtime: 'go', assets: expected })
})

test('missing, empty or directory assets fail before publishing a manifest', () => {
  for (const kind of ['missing', 'empty', 'directory']) {
    const directory = mkdtempSync(join(tmpdir(), 'go-cli-release-invalid-'))
    directories.push(directory)
    const path = join(directory, cliBinaryName(CLI_TARGETS[0]))
    if (kind === 'empty') writeFileSync(path, '')
    if (kind === 'directory') mkdirSync(path)
    expect(() => writeCLIReleaseManifest(directory, '1.2.3')).toThrow()
    expect(() => readFileSync(join(directory, CLI_MANIFEST_NAME))).toThrow()
  }
})

test('release tag text cannot become a filesystem path or installer channel', () => {
  const directory = mkdtempSync(join(tmpdir(), 'go-cli-release-version-'))
  directories.push(directory)
  for (const version of ['../1.2.3', 'latest', '1.2.3\nmalicious', 'v1.2.3']) expect(() => writeCLIReleaseManifest(directory, version)).toThrow()
})
