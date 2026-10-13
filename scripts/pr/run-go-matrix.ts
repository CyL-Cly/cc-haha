import { existsSync, lstatSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { isAbsolute, join, resolve } from 'node:path'
import { GO_PLATFORMS, goSourceSnapshot, scanGoSources, type GoPlatformEvidence, type GoSourceScope } from './go-platforms'
import { collectGoChangedLines, createGoTestEnvironment, type GoDeclarationEvidence } from './run-go-tests'
import { collectCorpusSnapshot } from '../quality-gate/go-parity/manifest'

type Changed = Array<{ file: string; lines: number[] }>
type Coverage = {
  minimumPercent: number; covered: number; total: number; pct: number | null; failures: string[]
  files: Array<{ file: string; covered: number; total: number; missing: number[]; declarationOnly?: { sha256: string; compilerCounters: number } }>
  skipped: Array<{ file: string; reason: string }>
}
export type GoMatrixInput = {
  schemaVersion: number; status: string; scope: string; allTargets: string
  platform?: GoPlatformEvidence; sources?: GoSourceScope
  sourceSnapshot?: ReturnType<typeof goSourceSnapshot>; changed?: Changed
  repositoryInputs?: Awaited<ReturnType<typeof collectCorpusSnapshot>>
  packages: string[]; packagesWithoutTestFiles?: string[]
  unit?: { packages: string[]; passedTests: number; noTestPackages?: string[] }
  race?: { packages: string[]; passedTests: number; noTestPackages?: string[] }
  changedLines?: Coverage; failures: string[]
  declarations?: GoDeclarationEvidence[]
}

const isProduction = (file: string) => file.startsWith('backend/') && file.endsWith('.go') && !file.endsWith('_test.go') && !/\/(fixtures|testdata|vendor)\//.test(file)
const equal = (left: unknown, right: unknown) => JSON.stringify(left) === JSON.stringify(right)

export function evaluateGoPlatformClosure(reports: GoMatrixInput[], expected: {
  minimumPercent: number; sourceSnapshot: ReturnType<typeof goSourceSnapshot>; repositoryInputs: Awaited<ReturnType<typeof collectCorpusSnapshot>>; goSources: string[]; changed: Changed
}) {
  const failures: string[] = []
  const evidence = new Map<string, GoMatrixInput>()
  const sources = [...expected.goSources].sort()
  if (!sources.length || new Set(sources).size !== sources.length || sources.some(file => !file.startsWith('backend/') || !file.endsWith('.go') || !expected.sourceSnapshot.some(entry => entry.file === file))) throw new Error('Expected Go source inventory is missing or outside the module input snapshot')
  const changed = expected.changed.filter(entry => isProduction(entry.file) && entry.lines.length > 0)
  if (!Number.isFinite(expected.minimumPercent) || expected.minimumPercent < 0 || expected.minimumPercent > 100) throw new Error('Existing Go changed-line threshold is missing or invalid')
  for (const report of reports) {
    const target = report.platform?.target ?? 'unknown'
    const rule = GO_PLATFORMS.find(platform => platform.target === target)
    if (!rule || evidence.has(target)) { failures.push(`Unknown or duplicate Go target report: ${target}`); continue }
    evidence.set(target, report)
    const fail = (message: string) => failures.push(`${target}: ${message}`)
    if (report.schemaVersion !== 1 || report.status !== 'passed' || report.scope !== 'current-target' || report.allTargets !== 'not-run' || report.failures?.length !== 0) fail('current-target checks did not pass with the expected report schema')
    if (report.platform?.native !== true) fail('missing actual native execution evidence')
    if (!equal(report.sourceSnapshot, expected.sourceSnapshot) || !equal(report.changed, expected.changed)) fail('source/threshold snapshot or changed-line coordinates differ from the final diff')
    if (!equal(report.repositoryInputs, expected.repositoryInputs)) fail('repository input bytes, Source fixtures or dependency locks differ from the final diff')
    if (!report.sources || !equal([...report.sources.formatted].sort(), sources)) fail('formatting did not inventory every backend Go source')
    if (report.sources) {
      const sets = [report.sources.active, report.sources.excluded, report.sources.unknown]
      if (sets.some(set => !Array.isArray(set) || set.some(file => !sources.includes(file)) || new Set(set).size !== set.length)
        || report.sources.active.some(file => report.sources!.excluded.includes(file))) fail('invalid Go source scope classification')
    }
    const completeExecution = (execution: GoMatrixInput['unit']) => {
      if (!report.packages?.length || !execution || execution.passedTests < 1) return false
      const withoutTests = execution.noTestPackages ?? []
      if (withoutTests.some(pkg => !report.packagesWithoutTestFiles?.includes(pkg) || execution.packages.includes(pkg))
        || new Set(withoutTests).size !== withoutTests.length) return false
      return report.packages.every(pkg => execution.packages.includes(pkg) || withoutTests.includes(pkg))
    }
    if (!completeExecution(report.unit)) fail('unit execution evidence is incomplete')
    if (rule.race) {
      if (report.platform?.race !== 'required' || !completeExecution(report.race)) fail('required race tests did not execute successfully')
    } else if (report.platform?.toolchain !== 'go1.25.7' || report.platform.race !== 'unsupported' || !report.platform.reason?.includes('Go 1.25.7') || report.race) fail('Windows ARM race limitation must be explicit, with no fabricated passing result')
    const coverage = report.changedLines
    if (!coverage || coverage.minimumPercent !== expected.minimumPercent || coverage.failures.length) { fail('coverage evidence or unchanged existing threshold is missing'); continue }
    if (!Number.isInteger(coverage.covered) || !Number.isInteger(coverage.total) || coverage.covered < 0 || coverage.total < coverage.covered) fail('invalid changed-line coverage counts')
    const percentage = coverage.total > 0 ? coverage.covered * 100 / coverage.total : null
    if (coverage.pct !== percentage || percentage !== null && percentage + Number.EPSILON < expected.minimumPercent) fail('changed executable coverage is below the existing target minimum or has inconsistent counts')
    if (coverage.files.reduce((total, file) => total + file.total, 0) !== coverage.total || coverage.files.reduce((total, file) => total + file.covered, 0) !== coverage.covered) fail('coverage file totals do not match this target')
    for (const entry of changed) {
      const mapping = coverage.files.filter(file => file.file === entry.file)
      const skipped = coverage.skipped.filter(file => file.file === entry.file)
      if (report.sources?.active.includes(entry.file)) {
        if (mapping.length !== 1 || skipped.length) { fail(`${entry.file}: active changed source has no unique coverage mapping`); continue }
        const file = mapping[0]
        if (!Number.isInteger(file.total) || !Number.isInteger(file.covered) || file.total < 0 || file.covered < 0 || file.covered > file.total
          || file.covered !== file.total - file.missing.length || new Set(file.missing).size !== file.missing.length || file.missing.some(line => !entry.lines.includes(line))) fail(`${entry.file}: invalid mapped coverage evidence`)
        if (file.declarationOnly) {
          const declarations = report.declarations?.filter(row => row.file === entry.file) ?? []
          const source = expected.sourceSnapshot.find(source => source.file === entry.file)
          if (file.total !== 0 || file.covered !== 0 || file.missing.length !== 0 || file.declarationOnly.compilerCounters !== 0
            || !source || file.declarationOnly.sha256 !== source.sha256 || declarations.length !== 1
            || declarations[0].declarationOnly !== true || declarations[0].compilerCounters !== 0
            || declarations[0].sha256 !== source.sha256 || declarations[0].executable.length !== 0) fail(`${entry.file}: missing or inconsistent declaration AST/cover evidence`)
        }
      } else if (report.sources?.excluded.includes(entry.file)) {
        if (mapping.length || skipped.length !== 1 || !skipped[0].reason.includes('another supported native target')) fail(`${entry.file}: platform exclusion is missing or fabricates zero-line coverage`)
      } else fail(`${entry.file}: changed source is unknown or unwired`)
    }
  }
  for (const rule of GO_PLATFORMS) if (!evidence.has(rule.target)) failures.push(`Missing supported native target evidence: ${rule.target}`)
  for (const entry of changed) {
    if (![...evidence.values()].some(report => report.sources?.active.includes(entry.file) && report.changedLines?.files.some(file => file.file === entry.file))) failures.push(`${entry.file}: changed source is never active and mapped on any supported target`)
  }
  return { status: failures.length ? 'failed' : 'passed', scope: 'all-supported-native-targets', targets: GO_PLATFORMS.map(platform => platform.target), minimumPercent: expected.minimumPercent, failures }
}

function reportFiles(directory: string): string[] {
  if (!existsSync(directory) || lstatSync(directory).isSymbolicLink()) throw new Error('Go matrix report directory must exist and must not be a symlink')
  return readdirSync(directory, { withFileTypes: true }).flatMap(entry => {
    const path = join(directory, entry.name)
    if (entry.isSymbolicLink()) throw new Error('Go matrix evidence must not follow symlinks')
    return entry.isDirectory() ? reportFiles(path) : entry.isFile() && entry.name === 'report.json' ? [path] : []
  }).sort()
}

if (import.meta.main) {
  const index = process.argv.indexOf('--reports-dir')
  const directory = index === -1 ? undefined : process.argv[index + 1]
  if (!directory || !isAbsolute(directory)) throw new Error('--reports-dir requires an absolute downloaded-evidence directory')
  const root = process.cwd()
  const home = mkdtempSync(join(tmpdir(), 'go-matrix-closure-'))
  try {
    const changed = await collectGoChangedLines(root, createGoTestEnvironment(home), process.env.COVERAGE_BASE_REF ?? process.env.PR_BASE_REF)
    const thresholds = JSON.parse(readFileSync(join(root, 'scripts/quality-gate/coverage-thresholds.json'), 'utf8'))
    const result = evaluateGoPlatformClosure(reportFiles(resolve(directory)).map(path => JSON.parse(readFileSync(path, 'utf8'))), {
      minimumPercent: thresholds.changedLines?.minimumPercent ?? Number.NaN,
      sourceSnapshot: goSourceSnapshot(root, scanGoSources(join(root, 'backend'))),
      repositoryInputs: await collectCorpusSnapshot(root, createGoTestEnvironment(home)),
      goSources: scanGoSources(join(root, 'backend')).map(path => `backend/${path.slice(join(root, 'backend').length + 1).replace(/\\/g, '/')}`),
      changed: [...changed].sort(([a], [b]) => a.localeCompare(b)).map(([file, lines]) => ({ file, lines: [...lines].sort((a, b) => a - b) })),
    })
    const artifact = join(root, 'artifacts/go-matrix')
    mkdirSync(artifact, { recursive: true })
    writeFileSync(join(artifact, 'report.json'), JSON.stringify(result, null, 2) + '\n')
    console.log(`Go all-target closure: ${result.status}`)
    for (const failure of result.failures) console.error(failure)
    if (result.status !== 'passed') process.exitCode = 1
  } finally { rmSync(home, { recursive: true, force: true }) }
}
