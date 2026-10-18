import { createHash } from 'node:crypto'
import { createReadStream, existsSync, lstatSync, mkdirSync, readFileSync, readdirSync, realpathSync, writeFileSync } from 'node:fs'
import { join, relative, resolve, sep } from 'node:path'
import { createInterface } from 'node:readline'
import type { GoControlledModule, GoSourceScope } from './go-platforms'
import type { Readable } from 'node:stream'
import type { GoCoverageStorage } from './go-coverage-storage'

type Runner = (command: string[], options: { cwd: string; env: Record<string, string> }) => Promise<{ exitCode: number; stdout: string; stderr: string }>
type Snapshot = Array<{ file: string; sha256: string }>
export type GoChildCoverageManifest = {
  schemaVersion: 1
  rootDir: string
  modulePath: string
  target: string
  toolchain: string
  coveragePackages: string[]
  sourceSnapshot: Snapshot
  sources: GoSourceScope
  controlledModules: GoControlledModule[]
}
const digest = (data: string | Buffer) => createHash('sha256').update(data).digest('hex')
export function writeGoChildCoverageManifest(directory: string, manifest: GoChildCoverageManifest) {
  mkdirSync(join(directory, 'raw'), { recursive: true, mode: 0o700 })
  writeFileSync(join(directory, 'manifest.json'), JSON.stringify(manifest) + '\n', { mode: 0o600 })
}
function regular(path: string) {
  const info = lstatSync(path)
  if (!info.isFile() || info.isSymbolicLink()) throw new Error(`Child coverage requires a regular file: ${path}`)
}
function verifyBuildArguments(identity: any, manifest: GoChildCoverageManifest) {
  if (identity.kind !== 'fresh-cli') return
  const args = identity.buildArguments
  const packages = manifest.coveragePackages.map(pattern => pattern === './...' ? `${manifest.modulePath}/...` : pattern)
  if (!Array.isArray(args) || args.some(arg => typeof arg !== 'string')
    || !/^(?:.*[\\/])?go(?:\.exe)?$/.test(args[0] ?? '') || args[1] !== 'build'
    || args.filter(arg => arg === '-cover').length !== 1
    || args.filter(arg => arg === '-covermode=atomic').length !== 1
    || args.filter(arg => arg === `-coverpkg=${packages.join(',')}`).length !== 1
    || args.filter(arg => arg.startsWith('-cover')).length !== 3
    || args.indexOf('-o') < 2 || !args[args.indexOf('-o') + 1]) {
    throw new Error('Child binary has no matching original instrumented build arguments')
  }
}
function profileFile(name: string, manifest: GoChildCoverageManifest) {
  const modules = [{ path: manifest.modulePath, directory: 'backend' }, ...manifest.controlledModules]
  const module = modules.find(module => name.startsWith(`${module.path}/`))
  if (!module) throw new Error(`Child coverage source is outside controlled modules: ${name}`)
  const file = `${module.directory}/${name.slice(module.path.length + 1)}`
  if (file.split('/').some(part => !part || part === '.' || part === '..')) throw new Error(`Invalid child coverage source path: ${file}`)
  return file
}
type Block = { identity: string; file: string; statements: number; count: bigint }
async function readProfile(path: string, manifest: GoChildCoverageManifest, consume: (block: Block) => void, input?: Readable) {
  let mode = false
  for await (const line of createInterface({ input: input ?? createReadStream(path), crlfDelay: Infinity })) {
    if (!mode) {
      if (line !== 'mode: atomic') throw new Error('Child coverage requires actual atomic instrumentation')
      mode = true
      continue
    }
    if (!line) continue
    const match = line.match(/^(.+):(\d+)\.(\d+),(\d+)\.(\d+) (\d+) (\d+)$/)
    if (!match) throw new Error(`Invalid child coverage compiler block: ${line}`)
    const identity = `${match[1]}:${match[2]}.${match[3]},${match[4]}.${match[5]}`
    consume({ identity, file: profileFile(match[1], manifest), statements: Number(match[6]), count: BigInt(match[7]) })
  }
  if (!mode) throw new Error('Missing child coverage profile')
}

// Keep the parent compiler's complete default-tag denominator. Child counters
// can only change a matching block's hit, never its range or statement count.
export async function mergeGoChildCoverage(options: {
  directory: string; parentProfile: string; outputProfile: string
  manifest: GoChildCoverageManifest; execute: Runner; env: Record<string, string>; profileStorage?: GoCoverageStorage
}) {
  const { directory, manifest, execute, env } = options
  const bytes = readFileSync(join(directory, 'manifest.json'))
  if (JSON.stringify(JSON.parse(bytes.toString())) !== JSON.stringify(manifest)) throw new Error('Child coverage manifest changed')
  const root = realpathSync(manifest.rootDir)
  const snapshot = new Map(manifest.sourceSnapshot.map(row => [row.file, row.sha256]))
  const verifySource = (file: string) => {
    const sha256 = snapshot.get(file)
    const path = resolve(root, file)
    if (!sha256 || !path.startsWith(`${root}${sep}`)) throw new Error(`Child coverage source is absent from frozen snapshot: ${file}`)
    regular(path)
    if (realpathSync(path) !== path || digest(readFileSync(path)) !== sha256) throw new Error(`Child coverage source SHA mismatch: ${file}`)
  }
  const readActualProfile = (path: string, consume: (block: Block) => void) => options.profileStorage
    ? options.profileStorage.read(path, input => readProfile(path, manifest, consume, input))
    : readProfile(path, manifest, consume)
  const parent = new Map<string, Block>()
  await readActualProfile(options.parentProfile, block => {
    const previous = parent.get(block.identity)
    if (previous && previous.statements !== block.statements) throw new Error(`Parent compiler block conflicts: ${block.identity}`)
    parent.set(block.identity, { ...block, count: (previous?.count ?? 0n) > 0n || block.count > 0n ? 1n : 0n })
  })
  if (!parent.size) throw new Error('Parent coverage has no compiler blocks')
  const verified = new Set<string>()
  const binaries = new Map<string, string>()
  const profiles: string[] = []
  const ignored: Array<{ file: string; reason: string }> = []
  let counterFiles = 0, executedBlocks = 0, newlyCoveredBlocks = 0
  const raw = join(directory, 'raw')
  for (const entry of readdirSync(raw, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name))) {
    if (!entry.isDirectory() || entry.isSymbolicLink() || !entry.name.startsWith('child-')) throw new Error('Unowned child coverage directory')
    const child = join(raw, entry.name)
    const receiptPath = join(child, 'receipt.json')
    regular(receiptPath)
    const receipt = JSON.parse(readFileSync(receiptPath, 'utf8'))
    if (receipt.schemaVersion !== 1 || receipt.manifestSha256 !== digest(bytes) || !['self-test', 'fresh-cli'].includes(receipt.kind)
      || !/^[a-f0-9]{64}$/.test(receipt.binarySha256) || receipt.binary !== `bin/${receipt.binarySha256}` || typeof receipt.test !== 'string' || !receipt.test) throw new Error('Invalid child coverage receipt')
    const additional = receipt.additionalBinaries ?? []
    if (!Array.isArray(additional)) throw new Error('Invalid nested child binary receipt')
    const tagsInPod = new Set<string>()
    for (const identity of [receipt, ...additional]) {
      if (!['self-test', 'fresh-cli'].includes(identity.kind) || !/^[a-f0-9]{64}$/.test(identity.binarySha256)
        || identity.binary !== `bin/${identity.binarySha256}`) throw new Error('Invalid nested child binary identity')
      verifyBuildArguments(identity, manifest)
      const binary = join(directory, identity.binary)
      regular(binary)
      if (digest(readFileSync(binary)) !== identity.binarySha256) throw new Error('Child coverage binary SHA mismatch')
      if (!binaries.has(binary)) {
        const result = await execute(['go', 'version', '-m', binary], { cwd: join(root, 'backend'), env })
        if (result.exitCode !== 0) throw new Error(`Cannot read actual child Go binary metadata: ${result.stderr}`)
        const version = result.stdout.split('\n')[0].trim().split(/\s+/).at(-1)
        const settings = new Map([...result.stdout.matchAll(/^\s*build\s+(\S+)=(.*)$/gm)].map(match => [match[1], match[2]]))
        if (version !== manifest.toolchain || `${settings.get('GOOS')}/${settings.get('GOARCH')}` !== manifest.target || settings.get('-cover') !== 'true') throw new Error('Child Go target/toolchain/instrumentation mismatch')
        const tags = settings.get('-tags') ?? ''
        if (tags && tags !== 'native_process_fixture') throw new Error(`Unreviewed child Go tags: ${tags}`)
        binaries.set(binary, tags)
      }
      tagsInPod.add(binaries.get(binary)!)
    }
    const entries = readdirSync(child, { withFileTypes: true })
    if (entries.some(entry => !entry.isFile() || entry.isSymbolicLink() || !/^(receipt\.json|covmeta\.[a-f0-9]{32}|covcounters\.[a-f0-9]{32}\.\d+\.\d+)$/.test(entry.name))) throw new Error('Unexpected raw child coverage artifact')
    const counters = entries.filter(entry => entry.name.startsWith('covcounters.'))
    counterFiles += counters.length
    // A killed process can emit no counters. This is an explicit zero-evidence
    // receipt, not execution or a fabricated uncovered/covered statement.
    if (!counters.length) continue
    const output = join(directory, `${entry.name}.cover`)
    const textfmt = () => execute(['go', 'tool', 'covdata', 'textfmt', `-i=${child}`, `-o=${output}`], { cwd: join(root, 'backend'), env })
    const result = options.profileStorage ? await options.profileStorage.capture(output, async () => {
      const actual = await textfmt()
      if (actual.exitCode !== 0) throw new Error(`Invalid actual child coverage data: ${actual.stderr}`)
      return actual
    }) : await textfmt()
    if (result.exitCode !== 0 || !(options.profileStorage ? options.profileStorage.has(output) : existsSync(output))) throw new Error(`Invalid actual child coverage data: ${result.stderr}`)
    profiles.push(relative(directory, output))
    await readActualProfile(output, block => {
      if (!verified.has(block.file)) { verifySource(block.file); verified.add(block.file) }
      const original = parent.get(block.identity)
      if (!original) {
        if (/\/(fixtures|testdata|vendor)\//.test(block.file)) {
          if (!ignored.some(row => row.file === block.file)) ignored.push({ file: block.file, reason: 'existing fixture-only source policy; retained raw counters do not extend the production denominator' })
          return
        }
        // These are real alternate OS primitives from the existing explicit
        // private tag. Retain their counters without substituting them for the
        // default build's active sources or reducing its denominator.
        if (tagsInPod.has('native_process_fixture')
          && ['backend/cmd/claude-haha/main_auth_login_fixture.go', 'backend/cmd/claude-haha/main_native_process_fixture.go'].includes(block.file)
          && manifest.sources.excluded.includes(block.file) && !manifest.sources.active.includes(block.file)) {
          if (!ignored.some(row => row.file === block.file)) ignored.push({ file: block.file, reason: 'not compiled in the unchanged default-tag parent inventory' })
          return
        }
        throw new Error(`Child compiler block is absent from parent denominator: ${block.identity}`)
      }
      if (original.statements !== block.statements || original.file !== block.file) throw new Error(`Child compiler block mismatch: ${block.identity}`)
      if (block.count > 0n) {
        executedBlocks++
        if (original.count === 0n) newlyCoveredBlocks++
        original.count = 1n
      }
    })
  }
  // One unique compiler block is equivalent to the parser's preexisting OR
  // merge of duplicate parent processes. Distinct overlapping blocks retain
  // their separate identities and the parser's existing per-line AND rule.
  writeFileSync(options.outputProfile, `mode: atomic\n${[...parent.values()].map(block => `${block.identity} ${block.statements} ${block.count}`).join('\n')}\n`, { mode: 0o600 })
  return { parentProfile: options.parentProfile, outputProfile: options.outputProfile, binaries: binaries.size, counterFiles, profiles, executedBlocks, newlyCoveredBlocks, compilerBlocks: parent.size, ignored, ...(options.profileStorage ? { profileStorage: options.profileStorage.evidence(), profilePathsAreLogical: true } : {}) }
}
