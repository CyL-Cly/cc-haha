import { createHash } from 'node:crypto'
import { lstatSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { isAbsolute, join, relative, resolve, sep } from 'node:path'
import { createHermeticGoEnvironment } from '../../pr/go-dependencies'

type Digest = { file: string; sha256: string }
type Result = { exitCode: number; stdout: string; stderr: string }
type Runner = (command: string[], options: { cwd: string; env: Record<string, string>; inheritOutput?: boolean }) => Promise<Result>
const policy = 'All Git versioned and nonignored untracked regular repository inputs; raw bytes; dependencies/generated outputs excluded by Git ignore. No HEAD-only or import-graph inference.'

async function execute(command: string[], options: Parameters<Runner>[1]): Promise<Result> {
  const child = Bun.spawn(command, { cwd: options.cwd, env: options.env, stdin: 'ignore', stdout: options.inheritOutput ? 'inherit' : 'pipe', stderr: options.inheritOutput ? 'inherit' : 'pipe' })
  if (options.inheritOutput) return { exitCode: await child.exited, stdout: '', stderr: '' }
  const [exitCode, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
  return { exitCode, stdout, stderr }
}

function inputPath(root: string, file: string, deleted = false): string | null {
  if (!file || isAbsolute(file) || file.includes('\0') || file.split(/[\\/]/).some(part => !part || part === '.' || part === '..' || part === '.git')) throw new Error(`Invalid corpus input path: ${file}`)
  let path = root
  for (const component of file.split('/')) {
    path = join(path, component)
    try {
      if (lstatSync(path).isSymbolicLink()) throw new Error(`Corpus input cannot follow a symlink: ${file}`)
    } catch (error) {
      // Git's cached inventory retains tracked deletions. Only a deletion
      // confirmed by Git and physically absent may leave the current-input
      // inventory; a restored file must still be hashed at this endpoint.
      if (deleted && (error as NodeJS.ErrnoException).code === 'ENOENT') return null
      throw error
    }
  }
  if (!lstatSync(path).isFile() || !realpathSync(path).startsWith(`${realpathSync(root)}${sep}`)) throw new Error(`Corpus input is not a regular repository file: ${file}`)
  return path
}

export async function collectCorpusSnapshot(rootDir: string, env: Record<string, string>, run: Runner = execute): Promise<Digest[]> {
  const root = resolve(rootDir)
  const result = await run(['git', '-c', 'core.quotePath=false', 'ls-files', '--cached', '--others', '--exclude-standard', '-z'], { cwd: root, env })
  if (result.exitCode !== 0) throw new Error(`Could not inventory corpus inputs: ${result.stderr}`)
  const deletionResult = await run(['git', '-c', 'core.quotePath=false', 'ls-files', '--deleted', '-z'], { cwd: root, env })
  if (deletionResult.exitCode !== 0) throw new Error(`Could not inventory deleted corpus inputs: ${deletionResult.stderr}`)
  const deleted = new Set(deletionResult.stdout.split('\0').filter(Boolean))
  const names = [...new Set(result.stdout.split('\0').filter(Boolean))].sort()
  if (!names.length) throw new Error('Corpus input inventory is empty')
  for (const file of deleted) {
    if (!names.includes(file)) throw new Error(`Deleted corpus input is missing from Git's cached inventory: ${file}`)
    inputPath(root, file, true)
  }
  const snapshot: Digest[] = []
  for (const file of names) {
    const path = inputPath(root, file, deleted.has(file))
    if (path !== null) snapshot.push({ file, sha256: createHash('sha256').update(readFileSync(path)).digest('hex') })
  }
  if (!snapshot.length) throw new Error('Corpus input inventory contains no present regular files')
  return snapshot
}

export function assertCorpusSnapshotFresh(before: Digest[], after: Digest[]) {
  const first = new Map(before.map(entry => [entry.file, entry.sha256]))
  const last = new Map(after.map(entry => [entry.file, entry.sha256]))
  const changed = [...new Set([...first.keys(), ...last.keys()])].filter(file => first.get(file) !== last.get(file)).sort()
  if (changed.length) throw new Error(`Corpus inputs changed while checks were running: ${changed.join(', ')}; rerun for fresh evidence`)
}

export async function runCorpusWithManifest(options: {
  rootDir: string; corpus: string[]; artifactDir?: string; sourceEnvironment?: NodeJS.ProcessEnv; execute?: Runner
}) {
  const root = resolve(options.rootDir)
  const artifactDir = resolve(options.artifactDir ?? join(root, 'artifacts/go-parity/checkpoints', new Date().toISOString().replace(/[:.]/g, '-')))
  const outputRelative = relative(join(root, 'artifacts'), artifactDir)
  if (outputRelative === '..' || outputRelative.startsWith(`..${sep}`) || isAbsolute(outputRelative)) throw new Error('Corpus manifest output must be inside repository artifacts/')
  const home = mkdtempSync(join(tmpdir(), 'go-corpus-manifest-'))
  const environment = createHermeticGoEnvironment(home, options.sourceEnvironment, false, root)
  // Explicit seed selection is the only dependency input carried from outside
  // the repository; hydration revalidates its lock and every cached file.
  const seed = (options.sourceEnvironment ?? process.env).CC_HAHA_GO_DEPENDENCY_SEED
  if (seed) environment.CC_HAHA_GO_DEPENDENCY_SEED = seed
  const run = options.execute ?? execute
  const report = {
    schemaVersion: 1, status: 'failed' as 'passed' | 'failed', scope: 'listed-corpus-repository-inputs', inventoryPolicy: policy,
    limitations: [
      'Before/after snapshots detect endpoint changes; a mutate-and-restore event between snapshots requires external freezing or filesystem tracing.',
      'Go dependency seeds are independently verified during hydration; installed JS dependency bytes are excluded and rely on frozen installation plus the hashed dependency locks.',
      'Runtime versions are checked at both endpoints; tool executable bytes and ignored external runtime state are not repository source inputs.',
      'Corpus cases prove only their executed boundaries; no full CLI/Server/Harness or Benchmark-score claim.',
    ],
    corpus: options.corpus.map(path => relative(root, resolve(path)).split(sep).join('/')),
    runtime: { bun: process.versions.bun, bunBefore: '', bunAfter: '', goBefore: '', goAfter: '' },
    before: [] as Digest[], after: [] as Digest[], childExitCode: null as number | null, failures: [] as string[],
  }
  const goVersion = async () => {
    const result = await run(['go', 'version'], { cwd: join(root, 'backend'), env: environment })
    if (result.exitCode !== 0 || !/^go version go\d+\.\d+[^\n]*\S$/.test(result.stdout.trim())) throw new Error(`Go version evidence is unavailable: ${result.stderr || result.stdout}`)
    return result.stdout.trim()
  }
  const bunVersion = async () => {
    const result = await run([process.execPath, '--no-env-file', '--version'], { cwd: root, env: environment })
    if (result.exitCode !== 0 || result.stdout.trim() !== process.versions.bun) throw new Error(`Corpus child Bun version differs from the manifest runtime: ${result.stderr || result.stdout}`)
    return result.stdout.trim()
  }
  try {
    report.runtime.bunBefore = await bunVersion()
    report.runtime.goBefore = await goVersion()
    report.before = await collectCorpusSnapshot(root, environment, run)
    if (!report.corpus.length || new Set(report.corpus).size !== report.corpus.length || report.corpus.some(file => !file.endsWith('.test.ts') || !report.before.some(entry => entry.file === file))) throw new Error('Listed corpus test is missing from the repository input inventory')
    const result = await run([process.execPath, '--no-env-file', 'test', ...options.corpus], { cwd: root, env: environment, inheritOutput: true })
    report.childExitCode = result.exitCode
    report.after = await collectCorpusSnapshot(root, environment, run)
    report.runtime.goAfter = await goVersion()
    report.runtime.bunAfter = await bunVersion()
    assertCorpusSnapshotFresh(report.before, report.after)
    if (report.runtime.goBefore !== report.runtime.goAfter || report.runtime.bunBefore !== report.runtime.bunAfter) throw new Error('Go or Bun runtime changed while the corpus was running')
    if (result.exitCode !== 0) throw new Error(`Corpus tests exited ${result.exitCode}`)
    report.status = 'passed'
  } catch (error) { report.failures.push(error instanceof Error ? error.message : String(error)) }
  finally {
    mkdirSync(artifactDir, { recursive: true })
    writeFileSync(join(artifactDir, 'manifest.json'), JSON.stringify(report, null, 2) + '\n')
    rmSync(home, { recursive: true, force: true })
  }
  console.log(`[go-parity] ${report.status}; listed corpus ${report.corpus.length} files; repository inputs ${report.before.length}; manifest: ${relative(root, join(artifactDir, 'manifest.json'))}`)
  for (const failure of report.failures) console.error(failure)
  return report
}
