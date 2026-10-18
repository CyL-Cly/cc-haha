import { createHash } from 'node:crypto'
import { appendFileSync, closeSync, existsSync, fsyncSync, lstatSync, openSync, readFileSync, readdirSync, readlinkSync, realpathSync, unlinkSync, writeFileSync } from 'node:fs'
import { dirname, isAbsolute, join, relative, resolve, sep } from 'node:path'
import { PAIRED_PROTOCOL, type Measurement, type Pair, type Task } from './go-main-paired-benchmark-core'
import { lanesForMode } from '../quality-gate/modes'

type DependencyRow = { path: string; type: 'directory' | 'file' | 'symlink'; mode?: number; hash?: string; target?: string; link?: string }
export type DependencyInventory = { hash: string; canonicalRoot: string; rows: DependencyRow[] }

const digest = (bytes: string | Uint8Array) => createHash('sha256').update(bytes).digest('hex')
const ordered = (a: string, b: string) => a < b ? -1 : a > b ? 1 : 0

/** Read the complete installed dependency tree, including native executables.
 * Only the explicitly resolved node_modules root can be followed. Alias rows
 * preserve resolution topology while physical files and directory cycles are
 * read once. This never writes to the shared, possibly readonly installation.
 */
export function freezeResolvedDependencies(sourceRoot: string, options: { canonicalRoot?: string } = {}): DependencyInventory {
  const installed = realpathSync(join(sourceRoot, 'node_modules'))
  const canonicalRoot = options.canonicalRoot ? realpathSync(options.canonicalRoot) : installed
  if (installed !== canonicalRoot || !lstatSync(canonicalRoot).isDirectory()) throw new Error('Unexpected dependency canonical root')
  const nameOf = (path: string) => relative(canonicalRoot, path).split(sep).join('/') || '.'
  const admit = (path: string) => {
    const name = relative(canonicalRoot, path)
    if (name === '..' || name.startsWith('..' + sep) || isAbsolute(name)) throw new Error('Dependency symlink escapes canonical root')
    return path
  }
  // Resolve links a segment at a time, so even metadata is never read through
  // an intermediate link into an unapproved external installation or HOME.
  const resolveInternal = (path: string, resolutions = new Set<string>()): string => {
    path = admit(resolve(path))
    if (resolutions.has(path)) throw new Error('Unresolvable dependency symlink cycle')
    if (resolutions.size >= 64) throw new Error('Unresolvable dependency symlink cycle or depth')
    const nextResolutions = new Set(resolutions).add(path)
    const segments = relative(canonicalRoot, path).split(sep).filter(Boolean)
    let current = canonicalRoot
    for (let i = 0; i < segments.length; i++) {
      current = join(current, segments[i])
      if (!lstatSync(current).isSymbolicLink()) continue
      const target = admit(resolve(dirname(current), readlinkSync(current)))
      return resolveInternal(join(target, ...segments.slice(i + 1)), nextResolutions)
    }
    return current
  }
  const rows: DependencyRow[] = []
  const visited = new Set<string>()
  const hashes = new Map<string, string>()
  const visit = (path: string) => {
    if (visited.has(path)) return
    visited.add(path)
    const stat = lstatSync(path, { bigint: true })
    const row = { path: nameOf(path), mode: Number(stat.mode & 0o7777n) }
    if (stat.isSymbolicLink()) {
      const link = readlinkSync(path)
      const target = resolveInternal(path)
      rows.push({ path: row.path, type: 'symlink', link, target: nameOf(target) })
      visit(target)
    } else if (stat.isDirectory()) {
      rows.push({ ...row, type: 'directory' })
      for (const name of readdirSync(path).sort(ordered)) visit(join(path, name))
    } else if (stat.isFile()) {
      const identity = `${stat.dev}:${stat.ino}`
      let hash = hashes.get(identity)
      if (!hash) { hash = digest(readFileSync(path)); hashes.set(identity, hash) }
      rows.push({ ...row, type: 'file', hash })
    } else throw new Error('Unsupported dependency tree entry: ' + row.path)
  }
  visit(canonicalRoot)
  rows.sort((a, b) => ordered(a.path, b.path))
  return { hash: digest(JSON.stringify(rows)), canonicalRoot, rows }
}

export type StreamEvidenceResult = { complete: boolean; partialBytes: Uint8Array; error?: string }
export type StreamEvidenceOptions = {
  timeoutMs: number
  maxBytes?: number
  signal?: AbortSignal
  abortController?: AbortController
  onChunk?: (chunk: Uint8Array) => void
}

/** Forward and record one upstream reader. A native tee can leave its evidence
 * reader pending after the HTTP consumer disconnects, so this owns both sides.
 * completion always settles, preserving partial bytes on cancellation/failure.
 * Pass abortController.signal to the upstream fetch before calling this helper
 * when cancellation must also abort an outstanding physical HTTP request.
 */
export function createStreamEvidence(body: ReadableStream<Uint8Array>, options: StreamEvidenceOptions) {
  if (!Number.isSafeInteger(options.timeoutMs) || options.timeoutMs <= 0 || options.timeoutMs > 2_147_483_647) throw new Error('Invalid evidence timeout')
  const maxBytes = options.maxBytes ?? 32 * 1024 * 1024
  if (!Number.isSafeInteger(maxBytes) || maxBytes < 32 * 1024 * 1024) throw new Error('Evidence limit must be at least 32 MiB')
  const abortController = options.abortController ?? new AbortController()
  const reader = body.getReader()
  const chunks: Uint8Array[] = []
  let length = 0
  let settled = false
  let output!: ReadableStreamDefaultController<Uint8Array>
  let finish!: (result: StreamEvidenceResult) => void
  let timer: ReturnType<typeof setTimeout> | undefined
  const completion = new Promise<StreamEvidenceResult>(resolve => { finish = resolve })
  const safeError = (failure: unknown) => failure instanceof Error ? failure.message : typeof failure === 'string' ? failure : 'Evidence stream failed'
  const settle = (error?: string, consumerCanceled = false) => {
    if (settled) return
    if (error === '') error = 'Evidence stream failed'
    settled = true
    if (timer) clearTimeout(timer)
    options.signal?.removeEventListener('abort', externalAbort)
    abortController.signal.removeEventListener('abort', controllerAbort)
    const partialBytes = new Uint8Array(length)
    let offset = 0
    for (const chunk of chunks) { partialBytes.set(chunk, offset); offset += chunk.length }
    chunks.length = 0
    if (!consumerCanceled) {
      try { if (error) output.error(new Error(error)); else output.close() } catch { /* Consumer may already have closed. */ }
    }
    finish({ complete: error === undefined, partialBytes, ...(error ? { error } : {}) })
    if (error) {
      // Neither a stuck underlying cancel nor an already failed reader may
      // prevent the evidence receipt from settling within the deadline.
      try { void reader.cancel(error).catch(() => {}) } catch { /* Already closed. */ }
      if (!abortController.signal.aborted) abortController.abort(new Error(error))
    }
  }
  const abort = (reason = 'Evidence capture canceled') => settle(reason)
  const externalAbort = () => abort('Evidence caller canceled')
  const controllerAbort = () => abort('Evidence upstream canceled')
  const consumer = new ReadableStream<Uint8Array>({
    start(controller) { output = controller },
    cancel() { settle('Evidence consumer canceled', true) },
  })
  options.signal?.addEventListener('abort', externalAbort, { once: true })
  abortController.signal.addEventListener('abort', controllerAbort, { once: true })
  timer = setTimeout(() => abort('Evidence capture timeout'), options.timeoutMs)
  if (options.signal?.aborted) externalAbort()
  else if (abortController.signal.aborted) controllerAbort()
  else void (async () => {
    try {
      while (!settled) {
        const read = await reader.read()
        if (settled) return
        if (read.done) { settle(); return }
        if (length + read.value.byteLength > maxBytes) { abort('Evidence capture exceeded byte limit'); return }
        const chunk = new Uint8Array(read.value)
        chunks.push(chunk)
        length += chunk.length
        options.onChunk?.(chunk)
        output.enqueue(read.value)
      }
    } catch (error) { settle(safeError(error)) }
  })()
  return { consumer, completion, abort, signal: abortController.signal }
}

export type FileProof = { path: string; sha256: string }
type FileRow = { path: string; sha256: string; mode: number; bytes: number }
const object = (value: unknown): Record<string, any> => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('Expected evidence object')
  return value as Record<string, any>
}
const parse = (path: string) => object(JSON.parse(readFileSync(path, 'utf8')))
const integer = (value: unknown, name: string) => {
  if (!Number.isSafeInteger(value) || (value as number) < 0) throw new Error('Invalid actual ' + name)
  return value as number
}
const positive = (value: unknown, name: string) => {
  if (typeof value !== 'number' || !Number.isFinite(value) || value <= 0) throw new Error('Missing positive ' + name)
  return value
}
function regular(path: string) {
  if (!isAbsolute(path) || realpathSync(path) !== path || !lstatSync(path).isFile()) throw new Error('Evidence must be a canonical regular file: ' + path)
  return readFileSync(path)
}
export function verifyFile(proof: FileProof) {
  if (!proof || !/^[a-f0-9]{64}$/.test(proof.sha256) || digest(regular(proof.path)) !== proof.sha256) throw new Error('Evidence file changed or missing')
  return proof.sha256
}

/** Every physical result, transcript, judge output and raw wire file is sealed.
 * Unsupported objects and links fail closed rather than reading external HOME.
 */
export function evidenceFiles(root: string, omit: readonly string[] = []): FileRow[] {
  if (realpathSync(root) !== root) throw new Error('Evidence root must be canonical')
  const rows: FileRow[] = []
  const visit = (directory: string) => {
    for (const name of readdirSync(directory).sort(ordered)) {
      const path = join(directory, name), stat = lstatSync(path), local = relative(root, path).split(sep).join('/')
      if (omit.includes(local)) continue
      if (stat.isDirectory()) visit(path)
      else if (stat.isFile()) rows.push({ path: local, sha256: digest(readFileSync(path)), mode: stat.mode & 0o777, bytes: stat.size })
      else throw new Error('Unsupported evidence entry: ' + local)
    }
  }
  visit(root)
  return rows
}

/** A first invalidation is permanent for this allocation, including after a
 * process restart. Restoration of the input cannot make its prior use valid.
 */
export function invalidateBatch(root: string, reason: string) {
  const path = join(root, 'invalid-batch.json')
  if (existsSync(path)) return
  const fd = openSync(path, 'wx', 0o600)
  try { writeFileSync(fd, JSON.stringify({ version: 2, status: 'invalid-batch', reason }) + '\n'); fsyncSync(fd) } finally { closeSync(fd) }
}
export function requireValidBatch(root: string) {
  if (existsSync(join(root, 'invalid-batch.json'))) throw new Error('Invalid batch is permanently unscorable; use a fresh allocation')
  if (existsSync(join(root, 'pair-in-progress.json'))) {
    invalidateBatch(root, 'Interrupted pair cannot be selectively repeated')
    throw new Error('Interrupted pair requires a fresh allocation')
  }
}

/** Startup validation belongs to the same durable allocation as pair checks. */
export async function validateBatchStartup<T>(root: string, validate: () => Promise<T>): Promise<T> {
  requireValidBatch(root)
  try { return await validate() } catch (error) {
    if (existsSync(join(root, 'manifest.json'))) invalidateBatch(root, 'Existing-batch startup input validation failed')
    throw error
  }
}

function candidateCommand(run: Record<string, any>, bun: Record<string, any>, script: 'build:cli' | 'verify', root: string) {
  const actualArgv = run.argv
  if (!Array.isArray(actualArgv) || !actualArgv.every(value => typeof value === 'string') || JSON.stringify(run.effectiveCommand) !== JSON.stringify(actualArgv)) throw new Error('Actual pinned Bun command is required')
  // The optional global flag is retained verbatim in the authenticated receipt.
  const argv = script === 'build:cli' && actualArgv[1] === '--no-env-file' ? [actualArgv[0], ...actualArgv.slice(2)] : actualArgv
  if (argv[0] !== bun.path || argv[1] !== 'run' || argv[2] !== script) throw new Error('Actual pinned Bun command is required')
  if (script === 'build:cli') {
    if (argv.length === 3) return undefined
    if (argv.length !== 5 || argv[3] !== '--version' || !/^[A-Za-z0-9.+_-]+$/.test(argv[4])) throw new Error('An ordinary build:cli receipt is required')
    const desktop = parse(join(root, 'desktop/package.json'))
    if (argv[4] !== desktop.version) throw new Error('CLI product version must match the frozen desktop version')
    return argv[4]
  }
  if (argv.length === 3) return join(root, 'artifacts/quality-runs')
  if (argv.length !== 5 || argv[3] !== '--artifacts-dir' || !isAbsolute(argv[4]) || realpathSync(argv[4]) !== argv[4] || !lstatSync(argv[4]).isDirectory()) throw new Error('An unfiltered verify receipt is required')
  return argv[4] as string
}

function candidateInputPath(root: string, name: string) {
  const file = resolve(root, name), local = relative(root, file)
  if (!local || isAbsolute(name) || name.split('/').includes('..') || isAbsolute(local) || local === '..' || local.startsWith('..' + sep)) throw new Error('Unsupported candidate input')
  // Do not even inspect an absent leaf through an external parent alias.
  const parents = local.split(sep).slice(0, -1)
  let directory = root
  for (const segment of parents) {
    directory = join(directory, segment)
    let stat
    try { stat = lstatSync(directory) } catch (error) {
      if ((error as NodeJS.ErrnoException).code === 'ENOENT') break
      throw error
    }
    if (!stat.isDirectory() || stat.isSymbolicLink()) throw new Error('Candidate input parent is not a physical directory')
  }
  return file
}

/** Only the explicitly user-excluded CU surface can omit its dedicated helper.
 * This is evidence scope, never permission to skip the native lane or Go90.
 */
function candidateScopeExclusions(value: unknown): string[] {
  if (value === undefined) return []
  if (!Array.isArray(value) || value.length !== 1 || value[0] !== 'computer-use') throw new Error('Unsupported candidate scope exclusion')
  return value
}

/** Candidate evidence is supplied by the final build/gate, never manufactured
 * by a benchmark. All referenced files are re-read at every pair boundary.
 * A manifest-only preparation may omit this; --allow-live cannot.
 */
export function verifyCandidateEvidence(path: string, binaryHash: string, target: string) {
  const receiptHash = digest(regular(path)), value = parse(path)
  if (value.version !== 1 || value.target !== target || value.binary?.sha256 !== binaryHash) throw new Error('Candidate binary/target receipt mismatch')
  verifyFile(value.binary)
  verifyFile(value.inputs)
  const inputs = parse(value.inputs.path)
  if (inputs.preCopyPostIdentityEqual !== true || !inputs.files || !Number.isSafeInteger(inputs.allTrackedAndUntrackedFiles) || Object.keys(inputs.files).length !== inputs.allTrackedAndUntrackedFiles) throw new Error('Candidate snapshot identity is incomplete')
  const root = realpathSync(value.root)
  const scopeExclusions = candidateScopeExclusions(value.scopeExclusions)
  for (const [name, entry] of Object.entries(inputs.files)) {
    const file = candidateInputPath(root, name)
    if (entry === null) {
      try { lstatSync(file) } catch (error) {
        if ((error as NodeJS.ErrnoException).code === 'ENOENT') continue
        throw error
      }
      throw new Error('Deleted candidate source restored: ' + name)
    }
    const row = object(entry)
    if (!Number.isInteger(row.mode) || row.mode < 0 || row.mode > 0o7777 || !/^[a-f0-9]{64}$/.test(row.sha256)) throw new Error('Unsupported candidate input')
    if (row.kind === 'symlink') {
      if (typeof row.target !== 'string' || !lstatSync(file).isSymbolicLink() || readlinkSync(file) !== row.target || digest('symlink:' + row.target) !== row.sha256 || (lstatSync(file).mode & 0o7777) !== row.mode) throw new Error('Candidate source link changed: ' + name)
      const target = relative(root, resolve(dirname(file), row.target))
      if (isAbsolute(target) || target === '..' || target.startsWith('..' + sep)) throw new Error('Candidate source link escapes snapshot')
    } else if (row.kind !== 'file' || digest(regular(file)) !== row.sha256 || (lstatSync(file).mode & 0o7777) !== row.mode) throw new Error('Candidate source changed: ' + name)
  }
  for (const name of ['backend/go.mod', 'package.json', 'scripts/cli/build.ts', 'desktop/package.json']) if (!Object.hasOwn(inputs.files, name)) throw new Error('Incomplete candidate source inventory')
  const walkInputs = (directory: string) => {
    for (const name of readdirSync(directory)) {
      if (['node_modules', '.git', 'target', 'dist', 'build', 'coverage', 'artifacts'].includes(name)) continue
      const file = join(directory, name), stat = lstatSync(file)
      if (stat.isSymbolicLink()) {
        if (inputs.files[relative(root, file).split(sep).join('/')]?.kind !== 'symlink') throw new Error('Unbound candidate source link')
      } else if (stat.isDirectory()) walkInputs(file)
      else if (stat.isFile() && /\.(?:go|c|h|m|mm|swift|rs|[cm]?js|jsx|ts|tsx|json|mod|sum)$/.test(name) && !Object.hasOwn(inputs.files, relative(root, file).split(sep).join('/'))) throw new Error('Candidate source omitted from snapshot')
    }
  }
  for (const name of ['backend', 'src', 'adapters', 'stubs', 'scripts', 'desktop/src', 'desktop/electron', 'desktop/native', 'native']) if (existsSync(join(root, name))) walkInputs(join(root, name))
  for (const stage of ['build', 'quality']) {
    const run = object(value[stage])
    if (run.exitCode !== 0 || run.inputsBefore !== value.inputs.sha256 || run.inputsAfter !== value.inputs.sha256) throw new Error('Candidate ' + stage + ' did not pass on the same inputs')
    verifyFile(run.log)
  }
  const bun = object(value.bun)
  verifyFile(bun as FileProof)
  if (bun.version !== '1.3.14' || bun.revision !== '0d9b296af33f2b851fcbf4df3e9ec89751734ba4' || Bun.version !== bun.version || Bun.revision !== bun.revision || digest(regular(realpathSync(process.execPath))) !== bun.sha256) throw new Error('Candidate pinned Bun identity mismatch')
  const productVersion = candidateCommand(value.build, bun, 'build:cli', root)
  if (value.build.binaryHash !== binaryHash || value.build.infoBinaryHash !== binaryHash) throw new Error('An ordinary build:cli receipt is required')
  verifyFile(value.build.info)
  const info = readFileSync(value.build.info.path, 'utf8')
  if (!info.includes('github.com/NanmiCoder/cc-haha/backend/cmd/claude-haha') || /-cover|-race|-tags|native_process_fixture/.test(info) || !info.includes('-trimpath=true')) throw new Error('Candidate must be the ordinary uninstrumented CLI')
  if (productVersion !== undefined) {
    const settings = info.split(/\r?\n/).map(line => /^\s*build\t-ldflags=(.*)$/.exec(line)).filter(match => match !== null)
    if (settings.length) {
      // Preserve exact validation when the toolchain records linker settings.
      if (settings.length !== 1) throw new Error('Versioned CLI requires its actual product-version buildinfo')
      const encoded = settings[0]![1]
      const flags = encoded.startsWith('"') ? JSON.parse(encoded) : encoded
      if (flags !== '-X main.version=' + productVersion) throw new Error('Versioned CLI requires its actual product-version buildinfo')
    } else {
      // Go omits -ldflags under -trimpath (cmd/go/internal/load, issue 52372).
      // Bind the real ordinary build and public fast-path output instead.
      if (!value.build.receipt || !value.build.versionProbe) throw new Error('Versioned CLI requires its actual product-version buildinfo or ordinary build and public version probe')
      verifyFile(value.build.receipt)
      const built = parse(value.build.receipt.path)
      if (JSON.stringify(built.argv) !== JSON.stringify(value.build.argv) || built.cwd !== root || built.exitCode !== 0 || built.timedOut !== false) throw new Error('Versioned CLI requires its actual ordinary build receipt')
      verifyFile(value.build.versionProbe)
      const probe = parse(value.build.versionProbe.path)
      if (probe.kind !== 'ordinary-cli-public-version-probe' || probe.version !== 1
        || JSON.stringify(probe.argv) !== JSON.stringify([value.binary.path, '--version']) || probe.cwd !== root
        || probe.exitCode !== 0 || probe.signal !== null || probe.timedOut !== false
        || probe.binaryHashBefore !== binaryHash || probe.binaryHashAfter !== binaryHash
        || probe.inputsBefore !== value.inputs.sha256 || probe.inputsAfter !== value.inputs.sha256
        || !Array.isArray(probe.ownedGroupRemaining) || probe.ownedGroupRemaining.length !== 0) throw new Error('Versioned CLI requires its same-binary public version probe')
      verifyFile(probe.stdout)
      verifyFile(probe.stderr)
      if (readFileSync(probe.stdout.path, 'utf8') !== productVersion + ' (Claude Code)\n' || readFileSync(probe.stderr.path, 'utf8') !== '') throw new Error('Versioned CLI public version output mismatch')
    }
  }
  const [os, arch] = target.split('/')
  const goOS = os === 'win32' ? 'windows' : os, goArch = arch === 'x64' ? 'amd64' : arch
  if (!info.includes('GOOS=' + goOS) || !info.includes('GOARCH=' + goArch)) throw new Error('Ordinary CLI build target mismatch')
  const artifacts = candidateCommand(value.quality, bun, 'verify', root)!
  verifyFile(value.quality.report)
  const report = parse(value.quality.report.path)
  if (typeof report.runId !== 'string' || !report.runId || /[\\/]/.test(report.runId) || report.runId === '.' || report.runId === '..' || value.quality.report.path !== join(artifacts, report.runId, 'report.json')) throw new Error('Actual verify artifact directory/report mismatch')
  const required = lanesForMode('pr').filter(lane => !lane.live)
  if (JSON.stringify(candidateScopeExclusions(report.scopeExclusions)) !== JSON.stringify(scopeExclusions)) throw new Error('Candidate quality scope exclusion mismatch')
  if (report.mode !== 'pr' || report.dryRun !== false || report.allowLive !== false || realpathSync(report.rootDir) !== root || report.impact?.blocked === true || report.summary?.failed !== 0 || report.summary?.skipped !== 0 || report.summary?.passed !== required.length || !Array.isArray(report.results) || report.results.length !== required.length) throw new Error('Incomplete candidate quality gate')
  if (new Set(report.results.map((row: any) => row.id)).size !== report.results.length) throw new Error('Duplicate candidate quality lane')
  for (const lane of required) {
    const result = report.results.find((row: any) => row.id === lane.id)
    if (!result || result.status !== 'passed' || result.exitCode !== 0 || JSON.stringify(result.command) !== JSON.stringify(lane.command)) throw new Error('Candidate required lane not passed: ' + lane.id)
    if (lane.id === 'native-checks' && JSON.stringify(candidateScopeExclusions(result.scopeExclusions)) !== JSON.stringify(scopeExclusions)) throw new Error('Candidate native lane scope exclusion mismatch')
    if (result.logPath !== join(artifacts, report.runId, 'logs', lane.id.replace(/[^a-zA-Z0-9._-]+/g, '-') + '.log')) throw new Error('Actual verify lane log directory mismatch')
    const proof = value.quality.logs?.find((row: any) => row.path === result.logPath)
    if (!proof) throw new Error('Missing actual quality lane log: ' + lane.id)
    verifyFile(proof)
  }
  // Actual distributable bytes, not merely source or build exit statuses.
  const assetPaths = new Set<string>()
  for (const role of ['react', 'native-host', 'native-helper', 'native-assets']) {
    const asset = value.assets?.find((row: any) => row.role === role)
    if (role === 'native-helper' && scopeExclusions.includes('computer-use')) {
      if (!asset || value.assets.filter((row: any) => row.role === role).length !== 1 || asset.status !== 'excluded' || asset.scope !== 'computer-use' || asset.reason !== 'user-requested' || asset.inputsHash !== value.inputs.sha256 || Object.hasOwn(asset, 'files') || Object.hasOwn(asset, 'receipt') || Object.hasOwn(asset, 'exitCode')) throw new Error('Explicit user-excluded CU helper record is required')
      if (asset.productVersion !== undefined && asset.productVersion !== productVersion) throw new Error('Excluded CU helper product version mismatch')
      continue
    }
    if (!asset || value.assets.filter((row: any) => row.role === role).length !== 1 || asset.inputsHash !== value.inputs.sha256 || !Array.isArray(asset.files) || !asset.files.length) throw new Error('Missing final candidate asset: ' + role)
    for (const file of asset.files) {
      if (assetPaths.has(file.path)) throw new Error('Candidate asset reused for different roles')
      assetPaths.add(file.path); verifyFile(file)
    }
    verifyFile(asset.receipt)
    const built = parse(asset.receipt.path)
    if (built.role !== role || built.exitCode !== 0 || built.inputsHash !== value.inputs.sha256 || JSON.stringify(built.files) !== JSON.stringify(asset.files)) throw new Error('Asset build identity mismatch: ' + role)
    if (productVersion !== undefined && (asset.productVersion !== productVersion || built.productVersion !== productVersion)) throw new Error('Asset product version mismatch: ' + role)
  }
  return { path, sha256: receiptHash, inputsHash: value.inputs.sha256, binaryHash, target }
}

export const SOURCE_PARENT_HELPERS = ['record_source_parent.py', 'credential_capability.py', 'record-source-parent.ts', 'formal-command.ts', 'formal-transport.ts'] as const
export type SourceParentContext = {
  candidate: ReturnType<typeof verifyCandidateEvidence>
  source: { root: string; rows: [string, string][] }
  dependenciesHash: string
  wrapperHash: string
}

/** Bind an unscored, physically completed Source parent to this final candidate.
 * Receipts prevent accidental/stale acceptance; they are not signed attestations
 * against a malicious author who fabricates an entirely consistent evidence set.
 */
export function verifySourceParentEvidence(path: string, transcript: string, context: SourceParentContext) {
  const allocation = dirname(path), owner = process.getuid?.()
  const directory = lstatSync(allocation)
  if (!allocation.startsWith('/private/tmp/') && !allocation.startsWith('/tmp/') || realpathSync(allocation) !== allocation || !directory.isDirectory() || (directory.mode & 0o777) !== 0o700 || owner !== undefined && directory.uid !== owner) throw new Error('Private canonical parent allocation required')
  if (path !== join(allocation, 'frozen-parent-receipt.json') || transcript !== join(allocation, 'frozen-parent.jsonl')) throw new Error('Final frozen parent paths required')
  const below = (base: string, value: string) => {
    const local = relative(base, value)
    return !!local && local !== '..' && !local.startsWith('..' + sep) && !isAbsolute(local)
  }
  const privateBytes = (file: string, require600 = true) => {
    if (!below(allocation, file) || realpathSync(file) !== file) throw new Error('Parent evidence escaped its allocation')
    const stat = lstatSync(file)
    if (!stat.isFile() || owner !== undefined && stat.uid !== owner || require600 && (stat.mode & 0o777) !== 0o600) throw new Error('Private physical parent evidence required')
    return regular(file)
  }
  const proofBytes = (proof: FileProof, expectedPath?: string) => {
    if (!proof || expectedPath !== undefined && proof.path !== expectedPath || !/^[a-f0-9]{64}$/.test(proof.sha256)) throw new Error('Missing bound parent evidence')
    const bytes = privateBytes(proof.path)
    if (digest(bytes) !== proof.sha256) throw new Error('Parent evidence file changed')
    return bytes
  }
  const json = (bytes: Uint8Array) => object(JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes)))
  const rows = (bytes: Uint8Array) => new TextDecoder('utf-8', { fatal: true }).decode(bytes).trim().split('\n').filter(Boolean).map(line => object(JSON.parse(line)))
  const receiptBytes = privateBytes(path), receipt = json(receiptBytes)
  if (receipt.version !== 2 || receipt.status !== 'frozen-real-source-parent-not-formal120') throw new Error('Qualified Source parent receipt required')
  const inputs = json(proofBytes(receipt.inputs, join(allocation, 'source-inputs.json')))
  const pilot = json(proofBytes(receipt.pilot, join(allocation, 'pilot-process.json')))
  const supervision = json(proofBytes(receipt.supervision, join(allocation, 'worker-supervision.json')))
  const stdout = rows(proofBytes(receipt.stdout, join(allocation, 'stdout.jsonl')))
  proofBytes(receipt.stderr, join(allocation, 'stderr.log'))
  proofBytes(receipt.driverLog, join(allocation, 'driver.log'))
  if (supervision.code !== 0 || supervision.credentialAdmitted !== true || supervision.credentialStable !== true || supervision.outputRedactions !== 0 || supervision.failure !== null) throw new Error('Source parent FD worker did not complete safely')
  const candidate = context.candidate, candidateValue = parse(candidate.path)
  if (digest(regular(candidate.path)) !== candidate.sha256 || inputs.scope !== 'unscored-real-source-parent-preparation' || inputs.candidateEvidenceSHA256 !== candidate.sha256 || inputs.binarySHA256 !== candidate.binaryHash || inputs.bunSHA256 !== candidateValue.bun.sha256 || JSON.stringify(pilot.candidate) !== JSON.stringify(candidate)) throw new Error('Source parent used a different final candidate')
  verifyFile(candidateValue.inputs)
  const candidateInputs = parse(candidateValue.inputs.path), candidateRoot = realpathSync(candidateValue.root)
  if (!Array.isArray(receipt.helpers) || receipt.helpers.length !== SOURCE_PARENT_HELPERS.length || new Set(receipt.helpers.map((row: any) => row.name)).size !== SOURCE_PARENT_HELPERS.length) throw new Error('Incomplete Source parent producer identity')
  for (const name of SOURCE_PARENT_HELPERS) {
    const helper = receipt.helpers.find((row: any) => row.name === name)
    const local = 'scripts/perf/source-parent-recorder/' + name
    if (!helper || helper.path !== join(candidateRoot, local) || candidateInputs.files?.[local]?.kind !== 'file' || candidateInputs.files[local].sha256 !== helper.sha256 || inputs.helpers?.[name] !== helper.sha256) throw new Error('Source parent producer is outside the final candidate')
    verifyFile(helper)
  }
  if (inputs.source?.main !== PAIRED_PROTOCOL.main || inputs.source.root !== context.source.root || pilot.source !== context.source.root || !Array.isArray(inputs.source.files)) throw new Error('Source parent is not frozen MAIN')
  const sourceRows = new Map(inputs.source.files.map((row: any) => [row.path, row.sha256]))
  const production = inputs.source.files.filter((row: any) => /^(?:src|adapters|stubs)\//.test(row.path) || ['preload.ts', 'package.json', 'bun.lock', 'tsconfig.json'].includes(row.path))
  if (sourceRows.size !== inputs.source.files.length || production.length !== context.source.rows.length || context.source.rows.some(([name, hash]) => sourceRows.get(name) !== hash)) throw new Error('Source parent MAIN bytes differ from batch')
  if (pilot.dependenciesHash !== context.dependenciesHash || pilot.wrapperHash !== context.wrapperHash || pilot.binaryHash !== candidate.binaryHash || JSON.stringify(pilot.bun) !== JSON.stringify({ path: candidateValue.bun.path, version: candidateValue.bun.version, revision: candidateValue.bun.revision, sha256: candidateValue.bun.sha256 })) throw new Error('Source parent runtime or dependency identity changed')
  const p = object(pilot.process), state = object(pilot.transport)
  if (pilot.scope !== 'real-Source-unscored-parent-pilot-not-120-benchmark' || pilot.failure || pilot.outputRedactions !== 0 || p.code !== 0 || p.timedOut !== false || p.joined !== true || p.stdoutComplete !== true || p.stderrComplete !== true || !Array.isArray(p.recordingErrors) || p.recordingErrors.length || !Array.isArray(state.errors) || state.errors.length || !Array.isArray(state.recordingErrors) || state.recordingErrors.length || state.redactions !== 0 || !Number.isSafeInteger(state.modelCalls) || state.modelCalls < 1) throw new Error('Source parent pilot did not complete successfully')
  const prompt = 'Reply with exactly SOURCE_LIVE_FULL_READY and no other text. Do not use tools.'
  const expectedArgv = [candidateValue.bun.path, '--no-env-file', join(candidateRoot, 'scripts/perf/go-main-source-live.ts'), '--print', '--model', PAIRED_PROTOCOL.model, '--thinking', PAIRED_PROTOCOL.thinking, '--no-computer-use', '--dangerously-skip-permissions', '--output-format', 'stream-json', '--verbose', '--max-turns', String(PAIRED_PROTOCOL.maxTurns), prompt]
  if (JSON.stringify(pilot.argv) !== JSON.stringify(expectedArgv) || pilot.prompt !== prompt || pilot.cwd !== join(allocation, 'source-home')) throw new Error('Source parent used a different CLI entry or arguments')
  const parent = object(receipt.parent), sid = parent.sessionId
  if (typeof sid !== 'string' || !/^[a-zA-Z0-9-]+$/.test(sid) || parent.frozenPath !== transcript || parent.realSourceHistory !== true || parent.originalAndCopyEqual !== true || !below(join(allocation, 'source-home/.claude/projects'), parent.sourcePath) || parent.sourcePath.split(sep).at(-1) !== sid + '.jsonl') throw new Error('Source parent SID or physical transcript path mismatch')
  const frozen = privateBytes(transcript), original = privateBytes(parent.sourcePath, false)
  if (!/^[a-f0-9]{64}$/.test(parent.sha256) || digest(frozen) !== parent.sha256 || digest(original) !== parent.sha256 || parent.bytes !== frozen.length || !frozen.equals(original)) throw new Error('Source parent transcript bytes changed')
  const messages = rows(frozen), terminals = stdout.filter(row => row.type === 'result'), terminal = terminals.at(-1)
  if (terminals.length !== 1 || stdout.at(-1) !== terminal || terminal?.session_id !== sid || terminal.subtype !== 'success' || terminal.is_error !== false || typeof terminal.result !== 'string' || terminal.result.trim() !== 'SOURCE_LIVE_FULL_READY') throw new Error('Source parent public final result or SID mismatch')
  if (stdout.some(row => (row.type === 'user' || row.type === 'assistant') && row.session_id !== sid)) throw new Error('Source parent public message SID mismatch')
  const conversational = messages.filter(row => row.type === 'user' || row.type === 'assistant')
  const assistant = conversational.filter(row => row.type === 'assistant').at(-1)
  const content = assistant?.message?.content
  const text = typeof content === 'string' ? content : Array.isArray(content) ? content.filter((row: any) => row.type === 'text').map((row: any) => row.text).join('') : undefined
  if (parent.records !== messages.length || !conversational.some(row => row.type === 'user') || !assistant || conversational.some(row => row.sessionId !== sid) || text?.trim() !== 'SOURCE_LIVE_FULL_READY') throw new Error('Source parent physical history or SID mismatch')
  const transportRoot = join(allocation, 'transport')
  if (!Array.isArray(receipt.transportFiles) || new Set(receipt.transportFiles.map((row: any) => row.path)).size !== receipt.transportFiles.length) throw new Error('Missing complete Source parent transport proof')
  const transportDirectory = lstatSync(transportRoot)
  if (realpathSync(transportRoot) !== transportRoot || !transportDirectory.isDirectory() || (transportDirectory.mode & 0o777) !== 0o700 || owner !== undefined && transportDirectory.uid !== owner) throw new Error('Private physical parent transport directory required')
  const actualFiles = readdirSync(transportRoot).map(name => join(transportRoot, name)).sort(ordered)
  if (JSON.stringify(receipt.transportFiles.map((row: any) => row.path).sort(ordered)) !== JSON.stringify(actualFiles)) throw new Error('Source parent transport file inventory changed')
  for (const proof of receipt.transportFiles) proofBytes(proof)
  const transport = rows(privateBytes(join(transportRoot, 'transport.jsonl'))), requests = rows(privateBytes(join(transportRoot, 'requests.jsonl')))
  if (transport.length !== state.calls || requests.length !== state.modelCalls || new Set(requests.map(row => row.number)).size !== requests.length) throw new Error('Source parent actual request inventory mismatch')
  if (!Array.isArray(state.transportErrors) || state.transportErrors.some((error: unknown) => typeof error !== 'string')) throw new Error('Invalid Source parent preconnect diagnostics')
  const unmatchedHeadErrors = [...state.transportErrors]
  const responseIDs = new Set<string>()
  for (let index = 0; index < transport.length; index++) {
    const row = transport[index], number = index + 1
    if (row.number !== number || typeof row.pathname !== 'string' || typeof row.query !== 'string' || row.upstreamURL !== 'https://api.deepseek.com' + row.pathname + row.query || !row.pathname.startsWith('/anthropic')) throw new Error('Source parent transport was not the official endpoint')
    const response = json(privateBytes(join(transportRoot, `response-${number}.json`)))
    if (response.method !== row.method) throw new Error('Source parent response method mismatch')
    if (row.method === 'HEAD') {
      if (row.pathname !== '/anthropic' || response.bytes !== 0 || response.sha256 !== digest(new Uint8Array())) throw new Error('Source parent preconnect evidence mismatch')
      if (response.complete === true) {
        if (response.error || !Number.isInteger(response.status) || response.status < 100 || response.status > 599) throw new Error('Invalid Source parent completed preconnect')
      } else {
        const matched = unmatchedHeadErrors.indexOf(response.error)
        if (response.complete !== false || response.status !== 0 || typeof response.error !== 'string' || !response.error || matched < 0) throw new Error('Unbound Source parent failed preconnect')
        unmatchedHeadErrors.splice(matched, 1)
      }
      continue
    }
    if (response.complete !== true || response.error) throw new Error('Incomplete Source parent actual response')
    const request = requests.find(request => request.number === number)
    if (!request || row.method !== 'POST' || row.pathname !== '/anthropic/v1/messages' || request.method !== row.method || request.pathname !== row.pathname || request.query !== row.query) throw new Error('Source parent request route mismatch')
    const body = privateBytes(join(transportRoot, `request-${number}.txt`)), raw = privateBytes(join(transportRoot, `response-${number}.txt`))
    if (request.bodyHash !== digest(body) || JSON.stringify(JSON.parse(body.toString('utf8'))) !== JSON.stringify(request.body) || request.body?.model !== PAIRED_PROTOCOL.model || response.requestHash !== digest(JSON.stringify(request)) || response.bytes !== raw.length || response.sha256 !== digest(raw) || !Number.isInteger(response.status) || response.status < 200 || response.status >= 300) throw new Error('Source parent actual model exchange mismatch')
    const message = parseActualMessage(raw.toString('utf8'), response.contentType)
    if (message.model !== PAIRED_PROTOCOL.model || typeof message.id !== 'string' || !message.id || responseIDs.has(message.id) || !stdout.some(row => row.type === 'assistant' && row.message?.id === message.id && row.message?.model === message.model)) throw new Error('Source parent actual response identity mismatch')
    responseIDs.add(message.id)
  }
  if (responseIDs.size !== state.modelCalls) throw new Error('Missing Source parent actual model result')
  if (!responseIDs.has(assistant.message?.id)) throw new Error('Physical Source parent assistant is not the recorded model response')
  return { path, sha256: digest(receiptBytes), parentHash: parent.sha256, sessionId: sid, candidateHash: candidate.sha256 }
}

const usageKeys = ['input_tokens', 'output_tokens', 'cache_read_input_tokens', 'cache_creation_input_tokens'] as const
type Usage = Record<typeof usageKeys[number], number>
function usage(value: unknown): Usage {
  const row = object(value)
  const result = Object.fromEntries(usageKeys.map(key => [key, integer(row[key] ?? (key.startsWith('cache_') ? 0 : undefined), key)])) as Usage
  if (row.cache_creation) {
    const cache = object(row.cache_creation)
    if (integer(cache.ephemeral_1h_input_tokens ?? 0, '1h cache') + integer(cache.ephemeral_5m_input_tokens ?? 0, '5m cache') !== result.cache_creation_input_tokens) throw new Error('Cache creation breakdown mismatch')
  }
  return result
}
function sumUsage(rows: Usage[]): Usage {
  return Object.fromEntries(usageKeys.map(key => [key, integer(rows.reduce((sum, row) => sum + row[key], 0), key)])) as Usage
}
function sameUsage(actual: Usage, expected: Usage) {
  if (usageKeys.some(key => actual[key] !== expected[key])) throw new Error('Actual tokens/cache do not match the terminal ledger')
}

/** Parse original Anthropic Messages JSON or SSE, without replacing SDK parsing
 * or synthesizing stream completion. Deltas overwrite cumulative usage fields.
 */
export function parseActualMessage(text: string, contentType: string) {
  if (!contentType.toLowerCase().includes('text/event-stream')) {
    const message = object(JSON.parse(text))
    if (message.type !== 'message' || !message.stop_reason) throw new Error('Incomplete actual JSON message')
    return { id: message.id, model: message.model, usage: usage(message.usage) }
  }
  let message: Record<string, any> | undefined, stopped = false, delta = false, current: Record<string, any> = {}
  const events = text.replaceAll('\r\n', '\n').split('\n\n')
  if (events.pop() !== '') throw new Error('Truncated SSE frame')
  for (const frame of events) {
    const data = frame.split('\n').filter(line => line.startsWith('data:')).map(line => line.slice(5).replace(/^ /, '')).join('\n')
    if (!data) continue
    const event = object(JSON.parse(data))
    if (event.type === 'ping') continue
    if (stopped) throw new Error('SSE event after message_stop')
    if (event.type === 'error') throw new Error('Actual provider stream error')
    if (event.type === 'message_start') {
      if (message) throw new Error('Duplicate message_start')
      message = object(event.message); current = { ...object(message.usage) }
    } else if (event.type === 'message_delta') {
      if (!message) throw new Error('message_delta before message_start')
      current = { ...current, ...object(event.usage) }
      if (event.delta?.stop_reason) delta = true
    } else if (event.type === 'message_stop') {
      if (!message || !delta) throw new Error('Incomplete message_stop')
      stopped = true
    }
  }
  if (!message || !stopped) throw new Error('Missing actual message_stop')
  return { id: message.id, model: message.model, usage: usage(current) }
}

/** Recompute from physical raw bytes and public output on every resume. Caller
 * identity is proven only by the original public message ID/parent relation.
 * Unexposed auxiliary callers remain explicitly unknown, never guessed.
 */
export function validateEngineEvidence(home: string, task: Task, parentHash: string) {
  const command = parse(join(home, 'command.json')), judged = parse(join(home, 'judge.json'))
  const args = ['--print', '--model', PAIRED_PROTOCOL.model, '--thinking', PAIRED_PROTOCOL.thinking, '--no-computer-use', '--dangerously-skip-permissions', '--output-format', 'stream-json', '--verbose', '--max-turns', String(PAIRED_PROTOCOL.maxTurns), ...(task.fork ? ['--resume', join(home, '.claude/parent.jsonl'), '--fork-session'] : []), task.prompt]
  if (!Array.isArray(command.argv) || ![1, 3].includes(command.argv.indexOf('--print')) || command.argv.indexOf('--print') === 3 && command.argv[1] !== '--no-env-file' || JSON.stringify(command.argv.slice(command.argv.indexOf('--print'))) !== JSON.stringify(args)) throw new Error('Actual CLI arguments differ from frozen task')
  if (!Number.isInteger(command.exitCode) || typeof command.timedOut !== 'boolean' || !Number.isInteger(judged.exitCode) || typeof judged.timedOut !== 'boolean' || judged.codeHash !== digest(task.judge) || judged.logHash !== digest(readFileSync(join(home, 'judge.log')))) throw new Error('Invalid command/judge receipt')
  if (command.joined === false || command.stdoutComplete === false || command.stderrComplete === false || Array.isArray(judged.recordingErrors) && judged.recordingErrors.length) throw new Error('Incomplete command/judge process or output cleanup')
  if (positive(command.endedMonotonicMs, 'monotonic end') - positive(command.startedMonotonicMs, 'monotonic start') !== command.durationMs) throw new Error('Whole-task duration receipt mismatch')
  if (!Array.isArray(command.evidenceErrors) || !command.evidenceErrors.every((value: unknown) => typeof value === 'string') || !Array.isArray(command.recordingErrors) || !command.recordingErrors.every((value: unknown) => typeof value === 'string') || command.recordingErrors.length) throw new Error('Incomplete or failed raw evidence recording')
  const stdout = readFileSync(join(home, 'stdout.jsonl'), 'utf8'), stderr = readFileSync(join(home, 'stderr.log'), 'utf8')
  const records = stdout.trim().split('\n').filter(Boolean).map(line => {
    try { return object(JSON.parse(line)) } catch { return { type: 'invalid_stdout', line } }
  })
  const terminals = records.filter(row => row.type === 'result'), terminal = terminals.at(-1)
  const finalResult = terminals.length === 1 && records.at(-1) === terminal && typeof terminal?.subtype === 'string' && typeof terminal?.is_error === 'boolean'
  const problems: string[] = [...command.evidenceErrors]
  if (!finalResult) problems.push('No unique final public result')
  if (records.some(row => row.type === 'invalid_stdout')) problems.push('Invalid public stdout')
  if (command.exitCode !== 0) problems.push('CLI exited unsuccessfully: ' + command.exitCode)
  if (command.timedOut) problems.push('Whole-task timeout')
  if (/capability:|capability is not (?:implemented|migrated)|RUNTIME_CONFIG_INVALID/i.test(stderr + stdout)) problems.push('Runtime capability/configuration failure')
  const requests = readFileSync(join(home, 'requests.jsonl'), 'utf8').trim().split('\n').filter(Boolean).map(line => object(JSON.parse(line)))
  if (integer(command.modelRequests, 'model requests') !== requests.length || new Set(requests.map(row => row.number)).size !== requests.length) throw new Error('Missing or duplicate actual request evidence')
  const transport = readFileSync(join(home, 'transport.jsonl'), 'utf8').trim().split('\n').filter(Boolean).map(line => object(JSON.parse(line)))
  if (transport.length !== integer(command.transportCalls, 'transport calls') || transport.some((row, index) => row.number !== index + 1)) throw new Error('Incomplete actual transport sequence')
  const responses = new Map<number, { wire: Record<string, any>; raw: Buffer }>()
  for (const row of transport) {
    const wire = parse(join(home, `response-${row.number}.json`))
    if (wire.method !== row.method || typeof wire.complete !== 'boolean' || wire.complete && wire.error || !wire.complete && (typeof wire.error !== 'string' || !wire.error) || !Number.isInteger(wire.status) || !(wire.status >= 100 && wire.status <= 599 || wire.status === 0 && !wire.complete)) throw new Error('Incomplete actual transport response')
    if (row.method === 'HEAD') {
      if (row.pathname !== '/anthropic' || wire.bytes !== 0 || wire.sha256 !== digest(new Uint8Array())) throw new Error('Invalid actual preconnect evidence')
    } else {
      const request = requests.find(request => request.number === row.number && request.method === row.method && request.pathname === row.pathname && request.query === row.query)
      if (!request) throw new Error('Unaccounted actual HTTP request')
      const raw = readFileSync(join(home, `response-${row.number}.txt`))
      if (wire.sha256 !== digest(raw) || wire.bytes !== raw.length || wire.requestHash !== digest(JSON.stringify(request))) throw new Error('Incomplete or changed actual response')
      responses.set(row.number, { wire, raw })
    }
  }
  const exchanges: { number: number; caller: 'main' | 'agent' | 'unknown-auxiliary'; id: string; usage: Usage }[] = []
  const failedRequests: { number: number; status: number }[] = []
  const unmeasuredRequests: { number: number; reason: string }[] = []
  for (const request of requests) {
    const body = object(request.body), number = integer(request.number, 'request number')
    const requestBytes = readFileSync(join(home, `request-${number}.txt`))
    if (request.bodyHash !== digest(requestBytes) || JSON.stringify(JSON.parse(new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(requestBytes))) !== JSON.stringify(body)) throw new Error('Changed actual request body')
    if (!number || request.method !== 'POST' || !/^\/anthropic\/(?:v1\/)?messages$/.test(request.pathname) || body.model !== PAIRED_PROTOCOL.model || body.thinking?.type !== 'disabled' || body.output_config?.effort === 'adaptive') throw new Error('Actual request violates frozen model/thinking protocol')
    const max = integer(body.max_tokens, 'max_tokens')
    if (!max || max > PAIRED_PROTOCOL.maxOutputTokens) throw new Error('Actual output limit exceeds protocol')
    const response = responses.get(number)
    if (!response) throw new Error('Missing actual transport response')
    const { wire, raw } = response
    if (!wire.complete) {
      unmeasuredRequests.push({ number, reason: wire.error })
      continue
    }
    if (wire.status !== 200) {
      // A recorded HTTP failure is a task observation, not a reason to stop
      // the predeclared batch. Never invent usage for an error or partial body.
      failedRequests.push({ number, status: wire.status })
      let failed
      try { failed = object(JSON.parse(raw.toString('utf8'))) } catch { /* Raw non-JSON errors remain sealed. */ }
      if (failed?.type !== 'error' || failed.usage || failed.message?.usage) unmeasuredRequests.push({ number, reason: 'Unaccounted provider error response' })
      continue
    }
    let message: ReturnType<typeof parseActualMessage>
    try { message = parseActualMessage(raw.toString('utf8'), wire.contentType) } catch (error) {
      unmeasuredRequests.push({ number, reason: String(error) })
      continue
    }
    if (typeof message.id !== 'string' || !message.id || message.model !== PAIRED_PROTOCOL.model || exchanges.some(row => row.id === message.id)) throw new Error('Actual response model/identity mismatch')
    const publicRows = records.filter(row => row.type === 'assistant' && row.message?.id === message.id)
    const parents = new Set(publicRows.map(row => row.parent_tool_use_id))
    if (parents.size > 1) throw new Error('Ambiguous actual assistant parent')
    const caller = !publicRows.length ? 'unknown-auxiliary' : parents.has(null) ? 'main' : typeof publicRows[0].parent_tool_use_id === 'string' && publicRows[0].parent_tool_use_id ? 'agent' : undefined
    if (!caller) throw new Error('Missing actual public caller relationship')
    if (caller === 'main' && max !== PAIRED_PROTOCOL.maxOutputTokens) throw new Error('Actual main output limit differs from protocol')
    if (message.usage.output_tokens > max) throw new Error('Actual output usage exceeds requested limit')
    if (message.usage.input_tokens + message.usage.cache_read_input_tokens + message.usage.cache_creation_input_tokens + max > PAIRED_PROTOCOL.contextWindow) throw new Error('Actual context exceeds protocol')
    for (const row of publicRows) if (row.message.model !== message.model) throw new Error('Public/raw model mismatch')
    const finalPublic = publicRows.at(-1)
    if (finalPublic) sameUsage(usage(finalPublic.message.usage), message.usage)
    exchanges.push({ number, caller, id: message.id, usage: message.usage })
  }
  if (unmeasuredRequests.length) problems.push('Missing actual usage for recorded requests')
  const hasMain = exchanges.some(row => row.caller === 'main')
  if (!hasMain) problems.push('No actual main model request')
  for (const row of records.filter(row => row.type === 'assistant' && row.message?.model === PAIRED_PROTOCOL.model)) if (!exchanges.some(exchange => exchange.id === row.message.id)) {
    if (!unmeasuredRequests.length) throw new Error('Public assistant missing actual wire response')
    problems.push('Public assistant has only incomplete actual wire evidence')
  }
  // These are sums of observed complete responses only, not fabricated totals
  // for absent, failed or truncated exchanges. accountingComplete qualifies them.
  const aggregate = sumUsage(exchanges.map(row => row.usage)), main = sumUsage(exchanges.filter(row => row.caller === 'main').map(row => row.usage))
  const reportedSDKCost = terminal?.total_cost_usd ?? null
  let cost = 0, accountingComplete = false
  if (finalResult && terminal && hasMain && !unmeasuredRequests.length && terminal.usage && terminal.modelUsage) {
    sameUsage(usage(terminal.usage), main)
    const models = object(terminal.modelUsage), names = Object.keys(models)
    if (names.length !== 1 || names[0] !== PAIRED_PROTOCOL.model) throw new Error('Unreconciled terminal model ledger')
    const model = object(models[PAIRED_PROTOCOL.model])
    sameUsage(usage({ input_tokens: model.inputTokens, output_tokens: model.outputTokens, cache_read_input_tokens: model.cacheReadInputTokens, cache_creation_input_tokens: model.cacheCreationInputTokens }), aggregate)
    if (model.contextWindow !== PAIRED_PROTOCOL.contextWindow || model.maxOutputTokens !== PAIRED_PROTOCOL.maxOutputTokens) throw new Error('Terminal model limits differ from protocol')
    if (typeof reportedSDKCost === 'number' && Number.isFinite(reportedSDKCost) && reportedSDKCost > 0 && typeof model.costUSD === 'number' && Number.isFinite(model.costUSD) && model.costUSD > 0) {
      if (model.costUSD !== reportedSDKCost) throw new Error('Terminal SDK estimate mismatch')
      cost = reportedSDKCost
      accountingComplete = true
    }
    if (!(aggregate.input_tokens + aggregate.cache_read_input_tokens + aggregate.cache_creation_input_tokens) || !aggregate.output_tokens) accountingComplete = false
  }
  if (!accountingComplete) problems.push('Missing reconciled SDK estimated cost/usage')
  if (finalResult && terminal && (!Number.isSafeInteger(terminal.num_turns) || terminal.num_turns < 1 || terminal.num_turns > PAIRED_PROTOCOL.maxTurns)) problems.push('Actual terminal turn limit mismatch')
  const unchanged = (path: string, expected: string, hash = false) => {
    try {
      const bytes = regular(join(home, path))
      return hash ? digest(bytes) === expected : bytes.toString('utf8') === expected
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code === 'ENOENT' || (error as NodeJS.ErrnoException).code === 'EISDIR' || (error as NodeJS.ErrnoException).code === 'ENOTDIR' || (error as Error).message.startsWith('Evidence must be a canonical regular file:')) return false
      throw error
    }
  }
  const immutable = task.immutable.every(path => unchanged(path, task.files[path])) && (!task.fork || unchanged('.claude/parent.jsonl', parentHash, true))
  if (!immutable) problems.push('Immutable task or parent changed')
  const measurement: Measurement = { success: command.exitCode === 0 && finalResult && terminal?.subtype === 'success' && terminal?.is_error === false && judged.exitCode === 0 && !judged.timedOut && immutable && !problems.length, durationMs: positive(command.durationMs, 'monotonic whole-task duration'), estimatedCost: accountingComplete ? cost : 0, runtimeErrors: problems }
  return { measurement, exchanges, aggregate, main, accountingComplete, reportedSDKCost, missingMeasurements: accountingComplete ? [] : ['estimatedCost'], failedRequests, unmeasuredRequests, estimatedCostBasis: accountingComplete ? 'Original SDK unknown-model estimate; not a DeepSeek invoice' : 'Missing reconciled estimate; numeric zero is only the existing core missing-measurement sentinel, not a bill or an observed zero cost' }
}

export function sealEngineEvidence(home: string, task: Task, engine: string, manifestHash: string, parentHash: string) {
  const actual = validateEngineEvidence(home, task, parentHash)
  const seal = { version: 2, task: task.id, engine, manifestHash, actual, files: evidenceFiles(home, ['sealed-evidence.json']) }
  const text = JSON.stringify(seal, null, 2) + '\n'
  writeFileSync(join(home, 'sealed-evidence.json'), text, { flag: 'wx', mode: 0o600 })
  return { sha256: digest(text), measurement: actual.measurement }
}
export function revalidateEngineEvidence(home: string, task: Task, engine: string, manifestHash: string, parentHash: string, expectedHash: string) {
  const path = join(home, 'sealed-evidence.json'), text = regular(path), seal = parse(path)
  if (digest(text) !== expectedHash || seal.version !== 2 || seal.task !== task.id || seal.engine !== engine || seal.manifestHash !== manifestHash || JSON.stringify(seal.files) !== JSON.stringify(evidenceFiles(home, ['sealed-evidence.json']))) throw new Error('Completed engine raw/artifact seal mismatch')
  const actual = validateEngineEvidence(home, task, parentHash)
  if (JSON.stringify(actual) !== JSON.stringify(seal.actual)) throw new Error('Completed usage/judge/measurement changed')
  return actual.measurement
}

export type ProvenPair = Pair & { evidence: { version: 2; manifestHash: string; source: string; go: string } }
function verifyPair(root: string, task: Task, pair: ProvenPair, manifestHash: string, parentHash: string) {
  if (pair.id !== task.id || pair.evidence?.version !== 2 || pair.evidence.manifestHash !== manifestHash) throw new Error('Legacy or invalid completed pair; a fresh batch is required')
  for (const engine of ['source', 'go'] as const) {
    const actual = revalidateEngineEvidence(join(root, task.id, engine), task, engine, manifestHash, parentHash, pair.evidence[engine])
    if (JSON.stringify(actual) !== JSON.stringify(pair[engine])) throw new Error('Completed pair differs from actual raw evidence')
  }
}
export function loadCompletedPairs(root: string, tasks: readonly Task[], manifestHash: string, parentHash: string): ProvenPair[] {
  requireValidBatch(root)
  const path = join(root, 'pairs.jsonl'), pairs: ProvenPair[] = []
  if (!existsSync(path)) return pairs
  try {
    for (const line of readFileSync(path, 'utf8').trim().split('\n').filter(Boolean)) {
      const pair = JSON.parse(line) as ProvenPair, task = tasks[pairs.length]
      if (!task) throw new Error('Extra or repeated completed pair')
      verifyPair(root, task, pair, manifestHash, parentHash)
      pairs.push(pair)
    }
    return pairs
  } catch (error) { invalidateBatch(root, 'Completed-pair evidence revalidation failed'); throw error }
}
export function commitCompletedPair(root: string, task: Task, pair: ProvenPair, manifestHash: string, parentHash: string, checkInputs: () => void) {
  try {
    if (existsSync(join(root, 'invalid-batch.json'))) throw new Error('Batch is permanently invalid')
    checkInputs()
    const pending = parse(join(root, 'pair-in-progress.json'))
    if (pending.id !== task.id || pending.manifestHash !== manifestHash) throw new Error('Wrong pair completion owner')
    verifyPair(root, task, pair, manifestHash, parentHash)
    appendFileSync(join(root, 'pairs.jsonl'), JSON.stringify(pair) + '\n', { mode: 0o600, flush: true })
    unlinkSync(join(root, 'pair-in-progress.json'))
  } catch (error) { invalidateBatch(root, 'Pair input or evidence changed before commit'); throw error }
}
