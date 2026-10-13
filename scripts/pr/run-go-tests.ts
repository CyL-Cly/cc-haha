#!/usr/bin/env bun

import { appendFileSync, createReadStream, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { isAbsolute, join, relative, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import { StringDecoder } from 'node:string_decoder'
import { parseChangedLinesFromDiff } from '../quality-gate/coverage'
import { createHermeticGoEnvironment, goDependencyLock, hydrateGoDependencies, parseGoJsonStream } from './go-dependencies'
import { collectGoSourceScope, goControlledModules, goNativeTarget, goPlatformEvidence, goSourceDirectories, goSourceSnapshot, scanGoSources, type GoControlledModule, type GoSourceScope } from './go-platforms'
import { assertCorpusSnapshotFresh, collectCorpusSnapshot } from '../quality-gate/go-parity/manifest'

type CommandResult = { exitCode: number; stdout: string; stderr: string }
type CommandOptions = { cwd: string; env: Record<string, string> }
type CommandRunner = (command: string[], options: CommandOptions) => Promise<CommandResult>
type FileCoverage = { executable: Set<number>; covered: Set<number> }
export type GoDeclarationEvidence = {
  file: string
  sha256: string
  declarationOnly: boolean
  compilerCounters: number | null
  declarations: { imports: number; types: number; constants: number; variables: number }
  executable: Array<{ kind: string; line: number }>
}
type TestEvidence = { packages: string[]; passedTests: number; noTestPackages?: string[] }
type ChangedCoverage = {
  minimumPercent: number
  covered: number
  total: number
  pct: number | null
  files: Array<{ file: string; covered: number; total: number; missing: number[]; declarationOnly?: { sha256: string; compilerCounters: number } }>
  skipped: Array<{ file: string; reason: string }>
  unmappedExecutable: Array<{ file: string; sha256: string; compilerCounters: number; forms: GoDeclarationEvidence['executable'] }>
  failures: string[]
}

export function collectGoSourcePaths(output: string, backendDir: string): string[] {
  const root = resolve(backendDir)
  const sources = [...new Set(output.trim().split(/\r?\n/).filter(Boolean))].sort()
  if (sources.length === 0) throw new Error('Go list returned no source files for the formatting check')
  for (const source of sources) {
    const resolved = resolve(source)
    if (!isAbsolute(source) || !resolved.startsWith(`${root}${sep}`) || !source.endsWith('.go')) {
      throw new Error(`Go formatting source is outside the backend module: ${source}`)
    }
  }
  return sources
}

export function createGoTestEnvironment(sandboxHome: string, source: NodeJS.ProcessEnv = process.env) {
  return createHermeticGoEnvironment(sandboxHome, source)
}

export function collectGoNoTestPackages(output: string, expectedPackages: string[]): string[] {
  const expected = new Set(expectedPackages)
  const seen = new Set<string>()
  const noTests: string[] = []
  for (const pkg of parseGoJsonStream(output)) {
    if (!expected.has(pkg.ImportPath)) continue
    if (seen.has(pkg.ImportPath) || pkg.Error || pkg.Incomplete) throw new Error(`Invalid Go test package inventory: ${pkg.ImportPath}`)
    seen.add(pkg.ImportPath)
    for (const field of ['TestGoFiles', 'XTestGoFiles']) {
      if (pkg[field] !== undefined && (!Array.isArray(pkg[field]) || pkg[field].some((name: unknown) => typeof name !== 'string' || !name))) {
        throw new Error(`Invalid Go test source inventory: ${pkg.ImportPath} ${field}`)
      }
    }
    if (!pkg.TestGoFiles?.length && !pkg.XTestGoFiles?.length) noTests.push(pkg.ImportPath)
  }
  const missing = expectedPackages.filter(name => !seen.has(name))
  if (missing.length) throw new Error(`Go test package inventory is missing discovered packages: ${missing.join(', ')}`)
  return noTests.sort()
}

export function collectGoTestEvidence(output: string, expectedPackages: string[], packagesWithoutTestFiles: string[] = []): TestEvidence {
  const passedPackages = new Set<string>()
  const startedPackages = new Set<string>()
  const compiledWithoutTests = new Set<string>()
  const coveredWithoutTests = new Set<string>()
  const skippedPackages = new Set<string>()
  const packagesWithTests = new Set<string>()
  let passedTests = 0
  for (const line of output.split(/\r?\n/).filter(Boolean)) {
    const event = JSON.parse(line) as { Action?: string; Package?: string; Test?: string; Output?: string }
    if (event.Action === 'fail') throw new Error(`Go test failed: ${event.Package ?? 'unknown package'} ${event.Test ?? ''}`)
    if (event.Action === 'pass' && event.Test) passedTests += 1
    if (event.Action === 'pass' && !event.Test && event.Package) passedPackages.add(event.Package)
    if (event.Test && event.Package) packagesWithTests.add(event.Package)
    if (event.Action === 'start' && event.Package) startedPackages.add(event.Package)
    if (event.Action === 'skip' && !event.Test && event.Package) skippedPackages.add(event.Package)
    if (event.Action === 'output' && !event.Test && event.Package && typeof event.Output === 'string') {
      const compileResult = event.Output.match(/^\?\s+(\S+)\s+\[no test files\]\s*$/)
      if (compileResult?.[1] === event.Package) compiledWithoutTests.add(event.Package)
      // Instrumented Go packages without test files emit coverage and pass,
      // instead of the ordinary [no test files]/skip terminal receipt.
      const coverageResult = event.Output.match(/^\s+(\S+)\s+coverage: (?:0\.0% of statements|\[no statements\])\s*$/)
      if (coverageResult?.[1] === event.Package) coveredWithoutTests.add(event.Package)
    }
  }
  if (passedTests === 0) throw new Error('Go test output contains no passing tests')
  // Admit only actual go-list no-test inventory with matching physical
  // compiler/terminal receipts. Instrumented compilation is still not a test.
  const noTestPackages = packagesWithoutTestFiles.filter(name => expectedPackages.includes(name)
    && startedPackages.has(name) && !packagesWithTests.has(name)
    && ((compiledWithoutTests.has(name) && skippedPackages.has(name) && !passedPackages.has(name))
      || (coveredWithoutTests.has(name) && passedPackages.has(name) && !skippedPackages.has(name)))).sort()
  // An inventory-confirmed no-test package cannot become a passing suite
  // merely through a pass event without its required compile receipt.
  for (const name of packagesWithoutTestFiles) passedPackages.delete(name)
  const missing = expectedPackages.filter((name) => !passedPackages.has(name) && !noTestPackages.includes(name))
  if (missing.length > 0) throw new Error(`Go test output is missing successful package results: ${missing.join(', ')}`)
  return { packages: [...passedPackages].sort(), passedTests, ...(noTestPackages.length ? { noTestPackages } : {}) }
}

function createGoCoverageParser(modulePath: string, localModules: GoControlledModule[]) {
  const modules = [{ path: modulePath, directory: 'backend' }, ...goControlledModules(localModules)]
  let modeSeen = false
  const hits = new Map<string, Map<number, boolean>>()
  const blocks = new Map<string, { file: string; start: number; end: number; statements: number; covered: boolean }>()
  const addLine = (line: string) => {
    if (!modeSeen) {
      if (line !== 'mode: atomic') throw new Error('Expected an atomic Go coverage profile')
      modeSeen = true
      return
    }
    if (!line) return
    const block = line.match(/^(.+):(\d+)\.(\d+),(\d+)\.(\d+) (\d+) (\d+)$/)
    if (!block) throw new Error(`Malformed Go coverage block: ${line}`)
    const profilePath = block[1]
    const module = modules.find(module => profilePath.startsWith(`${module.path}/`))
    if (!module) throw new Error(`Coverage source is outside the backend module and its controlled local forks: ${profilePath}`)
    const file = `${module.directory}/${profilePath.slice(module.path.length + 1)}`
    if (file.split('/').includes('..')) throw new Error(`Invalid coverage source path: ${file}`)
    const start = Number(block[2])
    const end = Number(block[4]) - (Number(block[5]) === 1 ? 1 : 0)
    const statements = Number(block[6])
    // Empty marker methods retain actual source presence without inventing
    // executable lines or covered statements.
    if (!hits.has(file)) hits.set(file, new Map())
    if (statements === 0) return
    if (start < 1 || end < start) throw new Error(`Invalid Go coverage source range: ${line}`)
    const identity = `${profilePath}:${block[2]}.${block[3]},${block[4]}.${block[5]}`
    const previous = blocks.get(identity)
    if (previous && previous.statements !== statements) throw new Error(`Conflicting Go coverage statement count: ${line}`)
    // -coverpkg emits the same block from multiple package test processes. Merge
    // identical block counters before intersecting distinct blocks on one line.
    blocks.set(identity, { file, start, end, statements, covered: (previous?.covered ?? false) || Number(block[7]) > 0 })
  }
  const finish = () => {
    if (!modeSeen) throw new Error('Expected an atomic Go coverage profile')
    for (const { file, start, end, covered } of blocks.values()) {
      const fileHits = hits.get(file) ?? new Map<number, boolean>()
      for (let number = start; number <= end; number++) {
        // A single line can belong to covered and uncovered blocks. Treat it as
        // covered only when every block touching it ran; OR would hide branches.
        fileHits.set(number, (fileHits.get(number) ?? true) && covered)
      }
      hits.set(file, fileHits)
    }
    if (hits.size === 0) throw new Error('Go coverage profile contains no executable source blocks')
    return new Map([...hits].map(([file, lines]) => [file, {
      executable: new Set(lines.keys()),
      covered: new Set([...lines].filter(([, hit]) => hit).map(([line]) => line)),
    }]))
  }
  return { addLine, finish }
}

export function parseGoCoverageProfile(profile: string, modulePath: string, localModules: GoControlledModule[] = []): Map<string, FileCoverage> {
  const parser = createGoCoverageParser(modulePath, localModules)
  for (const line of profile.trim().split(/\r?\n/)) parser.addLine(line)
  return parser.finish()
}

// The full coverpkg profile repeats counters across package processes and can
// exceed a runtime's single-string limit. Retain one incomplete line and the
// unique counter union, rather than buffering the complete physical file.
export async function parseGoCoverageProfileFile(
  profilePath: string,
  modulePath: string,
  localModules: GoControlledModule[] = [],
  options: { highWaterMark?: number } = {},
): Promise<Map<string, FileCoverage>> {
  const parser = createGoCoverageParser(modulePath, localModules)
  const decoder = new StringDecoder('utf8')
  let incomplete = ''
  let leading = true
  let pendingLine: string | undefined
  let pendingWhitespace: string | undefined
  const addLine = (original: string) => {
    const line = leading ? original.trimStart() : original
    if (leading && !line) return
    leading = false
    // profile.trim() removes whitespace only at the two profile edges. Delay
    // the final line and first whitespace gap so interior whitespace still
    // fails the original block parser, with the same error and precedence.
    if (!line.trim()) {
      if (line && pendingWhitespace === undefined) pendingWhitespace = line
      return
    }
    if (pendingLine !== undefined) parser.addLine(pendingLine)
    if (pendingWhitespace !== undefined) parser.addLine(pendingWhitespace)
    pendingLine = line
    pendingWhitespace = undefined
  }
  const addText = (text: string) => {
    incomplete += text
    let offset = 0
    let newline: number
    while ((newline = incomplete.indexOf('\n', offset)) >= 0) {
      const line = incomplete.slice(offset, newline)
      addLine(line.endsWith('\r') ? line.slice(0, -1) : line)
      offset = newline + 1
    }
    incomplete = incomplete.slice(offset)
  }
  for await (const chunk of createReadStream(profilePath, { highWaterMark: options.highWaterMark ?? 64 * 1024 })) {
    addText(decoder.write(chunk))
  }
  addText(decoder.end())
  addLine(incomplete)
  if (pendingLine !== undefined) parser.addLine(pendingLine.trimEnd())
  return parser.finish()
}

function isGoProductionFile(file: string) {
  return file.startsWith('backend/') && file.endsWith('.go') && !file.endsWith('_test.go')
    && !/\/(fixtures|testdata|vendor)\//.test(file)
}

export function evaluateGoChangedCoverage(
  changed: Map<string, Set<number>>,
  coverage: Map<string, FileCoverage>,
  minimumPercent: number,
  scope?: GoSourceScope,
  declarations: Map<string, GoDeclarationEvidence> = new Map(),
): ChangedCoverage {
  const result: ChangedCoverage = { minimumPercent, covered: 0, total: 0, pct: null, files: [], skipped: [], unmappedExecutable: [], failures: [] }
  if (!Number.isFinite(minimumPercent) || minimumPercent < 0 || minimumPercent > 100) {
    throw new Error('Missing or invalid existing changed-line coverage minimum')
  }
  for (const [file, changedLines] of [...changed].sort(([a], [b]) => a.localeCompare(b))) {
    if (!isGoProductionFile(file) || changedLines.size === 0) continue
    if (scope?.excluded.includes(file)) {
      result.skipped.push({ file, reason: 'Excluded by Go build constraints on the current target; required on another supported native target' })
      continue
    }
    if (scope && !scope.active.includes(file)) {
      result.failures.push(`${file}: changed Go source is unknown or unwired on the current target`)
      continue
    }
    const fileCoverage = coverage.get(file)
    if (!fileCoverage) {
      const declaration = declarations.get(file)
      if (declaration?.declarationOnly === true && declaration.compilerCounters === 0 && declaration.executable.length === 0) {
        result.files.push({ file, covered: 0, total: 0, missing: [], declarationOnly: { sha256: declaration.sha256, compilerCounters: 0 } })
        continue
      }
      if (declaration?.declarationOnly === false && declaration.compilerCounters !== null && declaration.executable.length > 0) {
        result.unmappedExecutable.push({ file, sha256: declaration.sha256, compilerCounters: declaration.compilerCounters, forms: declaration.executable })
      }
      result.failures.push(`${file}: missing Go coverage source mapping`)
      continue
    }
    const executable = [...changedLines].filter((line) => fileCoverage.executable.has(line))
    const missing = executable.filter((line) => !fileCoverage.covered.has(line)).sort((a, b) => a - b)
    const covered = executable.length - missing.length
    result.files.push({ file, covered, total: executable.length, missing })
    result.total += executable.length
    result.covered += covered
  }
  if (result.total > 0) {
    result.pct = result.covered * 100 / result.total
    if (result.pct + Number.EPSILON < minimumPercent) {
      result.failures.push(`Go changed-lines: coverage ${result.pct.toFixed(2)}% is below the existing ${minimumPercent}% minimum`)
    }
  }
  return result
}

export async function collectGoDeclarationEvidence(
  rootDir: string,
  files: string[],
  sourceSnapshot: Array<{ file: string; sha256: string }>,
  env: Record<string, string>,
  execute: CommandRunner = runCommand,
): Promise<Map<string, GoDeclarationEvidence>> {
  if (!files.length) return new Map()
  if (new Set(files).size !== files.length || files.some(file => !isGoProductionFile(file))) throw new Error('Invalid Go declaration source inventory')
  const directory = mkdtempSync(join(tmpdir(), 'cc-haha-go-declarations-'))
  try {
    const input = join(directory, 'input.json')
    const output = join(directory, 'evidence.json')
    writeFileSync(input, JSON.stringify({ rootDir: resolve(rootDir), files, outputFile: output }))
    const helper = fileURLToPath(new URL('./go-coverage-declarations/main.go', import.meta.url))
    const result = await execute(['go', 'run', helper, input], { cwd: join(rootDir, 'backend'), env })
    if (result.exitCode !== 0) throw new Error(`Go declaration AST/cover inspection failed: ${result.stderr || result.stdout}`)
    const artifact = existsSync(output) ? readFileSync(output, 'utf8') : undefined
    if (artifact !== undefined && result.stdout.trim() && artifact.trim() !== result.stdout.trim()) throw new Error('Go declaration AST/cover artifact disagrees with its process output')
    const encoded = artifact ?? result.stdout
    if (!encoded.trim()) throw new Error('Go declaration AST/cover inspection returned no evidence')
    const rows: unknown = JSON.parse(encoded)
    if (!Array.isArray(rows) || rows.length !== files.length) throw new Error('Go declaration inspection returned incomplete evidence')
    const evidence = new Map<string, GoDeclarationEvidence>()
    for (const row of rows as GoDeclarationEvidence[]) {
      const snapshot = sourceSnapshot.find(source => source.file === row.file)
      const counts = row.declarations
      if (!files.includes(row.file) || evidence.has(row.file) || !snapshot || row.sha256 !== snapshot.sha256 || !/^[a-f0-9]{64}$/.test(row.sha256)
        || typeof row.declarationOnly !== 'boolean' || !counts || Object.keys(counts).sort().join(',') !== 'constants,imports,types,variables'
        || Object.values(counts).some(count => !Number.isInteger(count) || count < 0)
        || !Array.isArray(row.executable) || row.executable.some(form => !form || typeof form.kind !== 'string' || !form.kind || !Number.isInteger(form.line) || form.line < 1)
        || row.declarationOnly && (row.compilerCounters !== 0 || row.executable.length !== 0)
        || !row.declarationOnly && (!Number.isInteger(row.compilerCounters) || row.compilerCounters! < 0 || row.executable.length === 0)) {
        throw new Error(`Invalid or stale Go declaration AST/cover evidence: ${row.file}`)
      }
      evidence.set(row.file, row)
    }
    return evidence
  } finally {
    rmSync(directory, { recursive: true, force: true })
  }
}

async function runCommand(command: string[], options: CommandOptions): Promise<CommandResult> {
  const child = Bun.spawn(command, { ...options, stdout: 'pipe', stderr: 'pipe', stdin: 'ignore' })
  const [exitCode, stdout, stderr] = await Promise.all([
    child.exited, new Response(child.stdout).text(), new Response(child.stderr).text(),
  ])
  return { exitCode, stdout, stderr }
}

function commandFailureDetail(command: string[], result: CommandResult): string {
  if (command[0] === 'go' && command[1] === 'test') {
    const failures: string[] = []
    for (const line of result.stdout.split(/\r?\n/)) {
      if (!line.startsWith('{')) continue
      try {
        const event = JSON.parse(line) as { Action?: string; Package?: string; Test?: string }
        if (event.Action === 'fail') failures.push(`${event.Package ?? 'unknown package'}${event.Test ? ` ${event.Test}` : ''}`)
      } catch { /* The complete physical output remains in checks.log. */ }
    }
    if (failures.length) return `${failures.length} failed test/package receipts:\n${failures.join('\n')}\nComplete physical output: checks.log${result.stderr ? `\n${result.stderr.slice(0, 12000)}` : ''}`
  }
  return result.stderr || result.stdout
}

export async function collectGoChangedLines(
  rootDir: string,
  env: Record<string, string>,
  baseRef?: string,
  execute: CommandRunner = runCommand,
) {
  const changed = new Map<string, Set<number>>()
  const checked = async (args: string[]) => {
    const result = await execute(['git', '-c', 'core.quotePath=false', ...args], { cwd: rootDir, env })
    if (result.exitCode !== 0) throw new Error(`Could not determine Go changed lines: ${result.stderr || args.join(' ')}`)
    return result.stdout
  }
  const merge = (diff: string) => {
    for (const [file, lines] of parseChangedLinesFromDiff(diff)) {
      changed.set(file, new Set([...(changed.get(file) ?? []), ...lines]))
    }
  }
  const comparisonBase = baseRef?.trim() || 'origin/main'
  const mergeBase = (await checked(['merge-base', comparisonBase, 'HEAD'])).trim()
  if (!/^[0-9a-f]{40,64}$/.test(mergeBase)) throw new Error('Could not determine a valid Go coverage comparison commit')
  // Compare the final working tree directly with the merge base. Combining a
  // committed diff with another working-tree diff would mix two line coordinates
  // after a staged/unstaged insertion and measure the wrong lines.
  merge(await checked(['diff', '--unified=0', '--no-ext-diff', mergeBase, '--', 'backend/']))
  const untracked = await checked(['ls-files', '--others', '--exclude-standard', '-z', '--', 'backend/'])
  for (const file of untracked.split('\0').filter(isGoProductionFile)) {
    const source = readFileSync(join(rootDir, file), 'utf8')
    changed.set(file, new Set(source.split(/\r?\n/).map((_, index) => index + 1)))
  }
  return changed
}

export async function runGoChecks(options: {
  rootDir: string
  artifactDir?: string
  baseRef?: string
  execute?: CommandRunner
  sourceEnvironment?: NodeJS.ProcessEnv
}) {
  const rootDir = resolve(options.rootDir)
  const artifactDir = resolve(options.artifactDir ?? join(rootDir, 'artifacts', 'go-checks', new Date().toISOString().replace(/[:.]/g, '-')))
  const sandboxHome = mkdtempSync(join(tmpdir(), 'cc-haha-go-tests-'))
  const env = createGoTestEnvironment(sandboxHome, options.sourceEnvironment)
  // Go contracts execute the actual TS oracle in guarded disposable children.
  // Bind the runtime that is executing this Bun lane, not a path inherited
  // from a developer's provider/config environment.
  env.CC_HAHA_SOURCE_BUN = process.execPath
  env.CC_HAHA_PINNED_BUN = process.execPath
  const execute = options.execute ?? runCommand
  const cwd = join(rootDir, 'backend')
  mkdirSync(artifactDir, { recursive: true })
  const logPath = join(artifactDir, 'checks.log')
  const profilePath = join(artifactDir, 'coverage.out')
  const report = {
    schemaVersion: 1,
    status: 'failed' as 'passed' | 'failed',
    scope: 'current-target' as const,
    allTargets: 'not-run' as const,
    platform: undefined as ReturnType<typeof goPlatformEvidence> | undefined,
    sources: undefined as GoSourceScope | undefined,
    sourceSnapshot: undefined as ReturnType<typeof goSourceSnapshot> | undefined,
    repositoryInputs: undefined as Awaited<ReturnType<typeof collectCorpusSnapshot>> | undefined,
    changed: undefined as Array<{ file: string; lines: number[] }> | undefined,
    packages: [] as string[],
    controlledModules: [] as GoControlledModule[],
    coveragePackages: [] as string[],
    testTargets: [] as string[],
    packagesWithoutTestFiles: [] as string[],
    dependencies: undefined as { modules: number; seed: string | null } | undefined,
    unit: undefined as TestEvidence | undefined,
    race: undefined as TestEvidence | undefined,
    coverage: undefined as { covered: number; total: number; pct: number } | undefined,
    changedLines: undefined as ChangedCoverage | undefined,
    declarations: [] as GoDeclarationEvidence[],
    failures: [] as string[],
  }
  const checked = async (command: string[]) => {
    console.log(`[go-checks] ${command.join(' ')}`)
    appendFileSync(logPath, `$ ${command.join(' ')}\n`)
    const result = await execute(command, { cwd, env })
    appendFileSync(logPath, `${result.stdout}${result.stderr}\n`)
    if (result.exitCode !== 0) throw new Error(`${command.join(' ')} exited ${result.exitCode}: ${commandFailureDetail(command, result)}`)
    return result.stdout
  }
  try {
    if (!existsSync(join(cwd, 'go.mod'))) throw new Error('Missing backend/go.mod; the selected Go lane cannot be skipped')
    report.dependencies = await hydrateGoDependencies(rootDir, env, { execute, sourceEnvironment: options.sourceEnvironment ?? process.env })
    // Actual Source oracles are launched from Go tests, but their fixtures,
    // TypeScript modules and locks are outside backend/. Freeze their bytes
    // alongside the Go compiler snapshot rather than trusting an import graph.
    report.repositoryInputs = await collectCorpusSnapshot(rootDir, env, execute)
    // Hydration verifies the pinned replacement identity and complete fork tree.
    // Cover selection alone does not execute a nested replacement module.
    // Discover and check each pinned controlled module as an explicit test root.
    report.controlledModules = goControlledModules(goDependencyLock(rootDir).localForks ?? [])
    report.testTargets = ['./...', ...report.controlledModules.map(module => `${module.path}/...`)]
    report.coveragePackages = [...report.testTargets]
    const nativeTarget = goNativeTarget()
    const expectedTarget = options.sourceEnvironment?.CC_HAHA_GO_TARGET ?? process.env.CC_HAHA_GO_TARGET ?? nativeTarget
    report.platform = goPlatformEvidence(JSON.parse(await checked(['go', 'env', '-json', 'GOOS', 'GOARCH', 'GOVERSION'])), expectedTarget)
    const module = JSON.parse(await checked(['go', 'list', '-m', '-json'])) as { Path?: string; Dir?: string }
    if (!module.Path || !module.Dir || resolve(module.Dir) !== cwd) throw new Error('Go module identity does not match backend/go.mod')
    report.packages = [...new Set((await checked(['go', 'list', ...report.testTargets])).trim().split(/\r?\n/).filter(Boolean))].sort()
    if (report.packages.length === 0) throw new Error('No Go backend packages were discovered')
    const sources = collectGoSourcePaths(scanGoSources(cwd).join('\n'), cwd)
    report.sourceSnapshot = goSourceSnapshot(rootDir, sources)
    const packageInventory = await checked(['go', 'list', '-e', '-json', ...goSourceDirectories(sources, cwd, report.controlledModules)])
    report.sources = collectGoSourceScope(packageInventory, sources, cwd)
    const packagesWithoutTestFiles = collectGoNoTestPackages(packageInventory, report.packages)
    report.packagesWithoutTestFiles = packagesWithoutTestFiles
    const unformatted = (await checked(['gofmt', '-l', ...sources])).trim()
    if (unformatted) throw new Error(`Go source is not formatted; run gofmt locally:\n${unformatted}`)
    await checked(['go', 'build', ...report.testTargets])
    await checked(['go', 'vet', ...report.testTargets])
    // These packages exercise actual process/socket protocols with Source's
    // short deadlines. Bound simultaneous package binaries rather than
    // weakening those deadlines on hosts with many logical CPU cores. The Go
    // -timeout watchdog is cumulative for an entire package, not each test;
    // the expanded clibootstrap Source suite passes in about eight minutes.
    // Keep individual protocol deadlines intact and bound the complete suite.
    report.unit = collectGoTestEvidence(await checked([
      'go', 'test', '-p=4', '-json', '-count=1', '-timeout=15m', '-covermode=atomic', `-coverpkg=${report.coveragePackages.join(',')}`, `-coverprofile=${profilePath}`, ...report.testTargets,
    ]), report.packages, packagesWithoutTestFiles)
    if (report.platform.race === 'required') report.race = collectGoTestEvidence(await checked(['go', 'test', '-p=4', '-race', '-json', '-count=1', '-timeout=15m', ...report.testTargets]), report.packages, packagesWithoutTestFiles)
    const coverage = await parseGoCoverageProfileFile(profilePath, module.Path, report.controlledModules)
    const total = [...coverage.values()].reduce((count, value) => count + value.executable.size, 0)
    const covered = [...coverage.values()].reduce((count, value) => count + value.covered.size, 0)
    report.coverage = { covered, total, pct: covered * 100 / total }
    const thresholds = JSON.parse(readFileSync(join(rootDir, 'scripts/quality-gate/coverage-thresholds.json'), 'utf8')) as {
      changedLines?: { minimumPercent?: number }
    }
    const changed = await collectGoChangedLines(rootDir, env, options.baseRef, execute)
    report.changed = [...changed].sort(([a], [b]) => a.localeCompare(b)).map(([file, lines]) => ({ file, lines: [...lines].sort((a, b) => a - b) }))
    const unmapped = [...changed.keys()].filter(file => isGoProductionFile(file) && report.sources!.active.includes(file) && !coverage.has(file)).sort()
    const declarations = await collectGoDeclarationEvidence(rootDir, unmapped, report.sourceSnapshot, env, async (command, options) => {
      appendFileSync(logPath, `$ ${command.join(' ')}\n`)
      const result = await execute(command, options)
      appendFileSync(logPath, `${result.stdout}${result.stderr}\n`)
      return result
    })
    report.declarations = [...declarations.values()]
    report.changedLines = evaluateGoChangedCoverage(changed, coverage, thresholds.changedLines?.minimumPercent ?? Number.NaN, report.sources, declarations)
    if (JSON.stringify(goSourceSnapshot(rootDir, scanGoSources(cwd))) !== JSON.stringify(report.sourceSnapshot)) throw new Error('Go module inputs or thresholds changed while checks were running; rerun for fresh evidence')
    assertCorpusSnapshotFresh(report.repositoryInputs, await collectCorpusSnapshot(rootDir, env, execute))
    report.failures.push(...report.changedLines.failures)
    if (report.failures.length === 0) report.status = 'passed'
  } catch (error) {
    report.failures.push(error instanceof Error ? error.message : String(error))
  } finally {
    writeFileSync(join(artifactDir, 'report.json'), JSON.stringify(report, null, 2) + '\n')
    writeFileSync(join(artifactDir, 'report.md'), [
      '# Go Backend Checks', '',
      `Status: ${report.status}`, '',
      `Evidence scope: current native target ${report.platform?.target ?? 'not established'}; all supported targets: not run locally`,
      `Race: ${report.platform?.race === 'unsupported' ? `unsupported — ${report.platform.reason}` : report.race ? 'passed' : 'not completed'}`,
      `Packages: ${report.packages.length}; unit tests: ${report.unit?.passedTests ?? 'not completed'}; race tests: ${report.race?.passedTests ?? 'not completed'}`,
      ...(report.unit?.noTestPackages ?? []).map(name => `Compiled without test files (not a passing test): ${name}`),
      `Actual test roots: ${report.testTargets.join(', ') || 'not established'}`,
      `Coverage instrumentation: ${report.coveragePackages.join(', ') || 'not established'}`,
      `Repository input bytes (including Source oracles, fixtures and locks): ${report.repositoryInputs?.length ?? 'not established'}; before/after freshness required`,
      ...report.controlledModules.map(module => `Controlled dependency module: ${module.path} ${module.version} -> ${module.directory}; explicit native test root and parent consumers supply actual coverage counters`),
      `Go coverage: ${report.coverage ? `${report.coverage.pct.toFixed(2)}% (${report.coverage.covered}/${report.coverage.total})` : 'not measured'}`,
      `Changed executable lines: ${report.changedLines?.pct == null ? 'none measured' : `${report.changedLines.pct.toFixed(2)}% (${report.changedLines.covered}/${report.changedLines.total})`}; minimum: ${report.changedLines?.minimumPercent ?? 'unavailable'}%`,
      '', ...(report.changedLines?.skipped ?? []).map(({ file, reason }) => `- Skipped on current target: ${file}: ${reason}`),
      ...report.declarations.filter(row => row.declarationOnly).map(row => `- Declaration-only source (Go AST and cover instrumenter; zero executable lines): ${row.file}`),
      ...report.declarations.filter(row => !row.declarationOnly).map(row => `- Executable source without coverage mapping (not counted as zero executable lines or as covered): ${row.file}: Go cover produced ${row.compilerCounters} counters; ${row.executable.map(form => `${form.kind} at line ${form.line}`).join(', ')}`),
      ...report.failures.map((failure) => `- ${failure}`), '',
    ].join('\n'))
    rmSync(sandboxHome, { recursive: true, force: true })
  }
  console.log(`[go-checks] current-target ${report.status} (${report.platform?.target ?? 'unknown'}); all-target evidence not run; report: ${relative(rootDir, join(artifactDir, 'report.json'))}`)
  return report
}

if (import.meta.main) {
  const index = process.argv.indexOf('--artifact-dir')
  const artifactDir = index === -1 ? undefined : process.argv[index + 1]
  if (index !== -1 && (!artifactDir || !isAbsolute(artifactDir))) throw new Error('--artifact-dir requires an absolute path')
  const report = await runGoChecks({
    rootDir: process.cwd(), artifactDir,
    baseRef: process.env.COVERAGE_BASE_REF ?? process.env.PR_BASE_REF,
  })
  if (report.status === 'failed') {
    for (const failure of report.failures) console.error(failure)
    process.exit(1)
  }
}
