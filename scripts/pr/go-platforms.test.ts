import { describe, expect, test } from 'bun:test'
import { mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { collectGoSourceScope, GO_PLATFORMS, goNativeTarget, goPlatformEvidence, goSourceDirectories, goSourceSnapshot, scanGoSources } from './go-platforms'
import { createGoTestEnvironment, evaluateGoChangedCoverage, parseGoCoverageProfile } from './run-go-tests'
import { goDependencyLock } from './go-dependencies'
import { evaluateGoPlatformClosure, type GoMatrixInput } from './run-go-matrix'

describe('native Go platform source and coverage scope', () => {
  test('snapshots raw module resources, embed data, fixtures and additions without treating them as Go source', () => {
    const root = mkdtempSync(join(tmpdir(), 'go-input-snapshot-'))
    try {
      mkdirSync(join(root, 'backend/testdata'), { recursive: true })
      mkdirSync(join(root, 'backend/vendor'), { recursive: true })
      mkdirSync(join(root, 'scripts/quality-gate'), { recursive: true })
      writeFileSync(join(root, 'backend/query.go'), 'package fixture\n')
      writeFileSync(join(root, 'backend/go.mod'), 'module fixture\n')
      writeFileSync(join(root, 'backend/spec.json'), '{"description":"first"}\n')
      writeFileSync(join(root, 'backend/testdata/input.json'), Buffer.from([0xff, 0x00, 0x0a]))
      writeFileSync(join(root, 'backend/vendor/resource.go'), 'package vendor\n')
      writeFileSync(join(root, 'scripts/quality-gate/coverage-thresholds.json'), '{}')
      const sources = scanGoSources(join(root, 'backend'))
      const original = goSourceSnapshot(root, sources)
      expect(original.map(entry => entry.file)).toEqual(['backend/go.mod', 'backend/query.go', 'backend/spec.json', 'backend/testdata/input.json', 'backend/vendor/resource.go', 'scripts/quality-gate/coverage-thresholds.json'])
      expect(sources).toEqual([join(root, 'backend/query.go')])
      writeFileSync(join(root, 'backend/spec.json'), '{"description":"second"}\r\n')
      expect(goSourceSnapshot(root, sources)).not.toEqual(original)
      const crlf = goSourceSnapshot(root, sources)
      writeFileSync(join(root, 'backend/spec.json'), '{"description":"second"}\n')
      expect(goSourceSnapshot(root, sources)).not.toEqual(crlf)
      writeFileSync(join(root, 'backend/testdata/new.bin'), Buffer.from([1]))
      expect(goSourceSnapshot(root, sources)).toHaveLength(original.length + 1)
      symlinkSync(join(root, 'backend/spec.json'), join(root, 'backend/testdata/link.json'))
      expect(() => goSourceSnapshot(root, sources)).toThrow('symlink')
    } finally { rmSync(root, { recursive: true, force: true }) }
  })

  test('inventories a controlled nested module through its real Go import path without removing format or snapshot inputs', () => {
    // Go reports the physical cwd; macOS temporary paths may use /var aliases.
    const root = realpathSync(mkdtempSync(join(tmpdir(), 'go-controlled-source-scope-')))
    const home = mkdtempSync(join(tmpdir(), 'go-controlled-source-home-'))
    try {
      const backend = join(root, 'backend'), fork = join(backend, 'third_party/libsignal-node')
      mkdirSync(fork, { recursive: true })
      mkdirSync(join(root, 'scripts/quality-gate'), { recursive: true })
      writeFileSync(join(backend, 'go.mod'), 'module example.test/backend\n\ngo 1.25.7\n\nrequire go.mau.fi/libsignal v0.2.1\nreplace go.mau.fi/libsignal => ./third_party/libsignal-node\n')
      writeFileSync(join(backend, 'query.go'), 'package backend\nimport signal "go.mau.fi/libsignal"\nfunc Query() string { return signal.Value() }\n')
      writeFileSync(join(backend, 'query_test.go'), 'package backend\nimport "testing"\nfunc TestQuery(t *testing.T) { if Query() != "actual-controlled-module" { t.Fatal(Query()) } }\n')
      writeFileSync(join(fork, 'go.mod'), 'module go.mau.fi/libsignal\n\ngo 1.25.7\n')
      writeFileSync(join(fork, 'value.go'), 'package libsignal\nfunc Value() string { return "actual-controlled-module" }\n')
      writeFileSync(join(fork, 'LICENSE'), 'private fixture license bytes\n')
      writeFileSync(join(root, 'scripts/quality-gate/coverage-thresholds.json'), '{"changedLines":{"minimumPercent":90}}')
      const sources = scanGoSources(backend), modules = goDependencyLock(root).localForks!
      const directories = goSourceDirectories(sources, backend, modules)
      expect(directories).toEqual(['./', 'go.mau.fi/libsignal'])
      const result = Bun.spawnSync(['go', 'list', '-e', '-json', ...directories], { cwd: backend, env: createGoTestEnvironment(home), stdout: 'pipe', stderr: 'pipe' })
      expect(result.exitCode).toBe(0)
      const scope = collectGoSourceScope(Buffer.from(result.stdout).toString(), sources, backend)
      expect(scope.active).toEqual(['backend/query.go', 'backend/third_party/libsignal-node/value.go'])
      expect(scope.unknown).toEqual([])
      expect(scope.formatted).toHaveLength(3)
      const profile = join(home, 'coverage.out')
      const consumer = Bun.spawnSync(['go', 'test', '-count=1', '-covermode=atomic', '-coverpkg=./...,go.mau.fi/libsignal/...', `-coverprofile=${profile}`, './...'], {
        cwd: backend, env: createGoTestEnvironment(home), stdout: 'pipe', stderr: 'pipe',
      })
      expect(consumer.exitCode).toBe(0)
      const coverage = parseGoCoverageProfile(readFileSync(profile, 'utf8'), 'example.test/backend', modules)
      const forkCoverage = coverage.get('backend/third_party/libsignal-node/value.go')!
      expect([...forkCoverage.covered]).toContain(2)
      expect(evaluateGoChangedCoverage(new Map([['backend/third_party/libsignal-node/value.go', new Set([2])]]), coverage, 90, scope)).toMatchObject({ total: 1, covered: 1, pct: 100, failures: [] })
      expect(goSourceSnapshot(root, sources).filter(row => row.file.startsWith('backend/third_party/')).map(row => row.file)).toEqual([
        'backend/third_party/libsignal-node/go.mod', 'backend/third_party/libsignal-node/LICENSE', 'backend/third_party/libsignal-node/value.go',
      ])
      expect(() => goSourceDirectories(sources, backend, [{ path: 'unreviewed', version: 'v0.2.1', directory: 'backend/third_party/libsignal-node' }])).toThrow('controlled')
    } finally { rmSync(root, { recursive: true, force: true }); rmSync(home, { recursive: true, force: true }) }
  })

  test('requires the declared target and Go toolchain target to match the actual host', () => {
    expect(goNativeTarget('win32', 'x64')).toBe('windows/amd64')
    expect(() => goNativeTarget('linux', 'ia32')).toThrow('Unsupported')
    expect(() => goPlatformEvidence({ GOOS: 'windows', GOARCH: 'amd64', GOVERSION: 'go1.25.7' }, 'windows/amd64', 'darwin/arm64')).toThrow('natively')
    const arm = goPlatformEvidence({ GOOS: 'windows', GOARCH: 'arm64', GOVERSION: 'go1.25.7' }, 'windows/arm64', 'windows/arm64')
    expect(arm).toMatchObject({ native: true, race: 'unsupported' })
    expect(arm.reason).toContain('Go 1.25.7')
    expect(() => goPlatformEvidence({ GOOS: 'windows', GOARCH: 'arm64', GOVERSION: 'go1.26.0' }, 'windows/arm64', 'windows/arm64')).toThrow('Review')
    for (const rule of GO_PLATFORMS.filter(rule => rule.race)) {
      const [GOOS, GOARCH] = rule.target.split('/')
      expect(goPlatformEvidence({ GOOS, GOARCH, GOVERSION: 'go1.25.7' }, rule.target, rule.target).race).toBe('required')
    }
  })

  test('formats all source names and uses Go ignored-source evidence even in a fully excluded directory', () => {
    const root = mkdtempSync(join(tmpdir(), 'go-source-platform-'))
    try {
      mkdirSync(join(root, 'backend/windows'), { recursive: true })
      mkdirSync(join(root, 'backend/testdata'), { recursive: true })
      for (const file of ['query.go', 'query_test.go', '_unwired.go', 'windows/command_windows.go', 'testdata/fixture.go']) writeFileSync(join(root, 'backend', file), 'package fixture\n')
      const sources = scanGoSources(join(root, 'backend'))
      const scope = collectGoSourceScope([
        JSON.stringify({ Dir: join(root, 'backend'), GoFiles: ['query.go'], TestGoFiles: ['query_test.go'] }),
        JSON.stringify({ Dir: join(root, 'backend/windows'), IgnoredGoFiles: ['command_windows.go'], Error: { Err: 'build constraints exclude all Go files' } }),
        JSON.stringify({ Dir: join(root, 'backend/testdata'), GoFiles: ['fixture.go'] }),
      ].join('\n'), sources, join(root, 'backend'))
      expect(scope.excluded).toEqual(['backend/windows/command_windows.go'])
      expect(scope.unknown).toEqual(['backend/_unwired.go'])
      expect(scope.formatted).toHaveLength(5)
      expect(goSourceDirectories(sources, join(root, 'backend'))).toEqual(['./', './testdata', './windows'])
      expect(() => collectGoSourceScope('', sources, join(root, 'backend'))).toThrow('empty')
      const changed = new Map([['backend/windows/command_windows.go', new Set([2])]])
      const result = evaluateGoChangedCoverage(changed, new Map(), 90, scope)
      expect(result.files).toEqual([])
      expect(result.skipped[0].reason).toContain('another supported native target')
      expect(result.pct).toBeNull()
      expect(evaluateGoChangedCoverage(new Map([['backend/_unwired.go', new Set([2])]]), new Map(), 90, scope).failures[0]).toContain('unknown or unwired')
      expect(evaluateGoChangedCoverage(new Map([['backend/query.go', new Set([2])]]), new Map(), 90, scope).failures[0]).toContain('missing Go coverage source mapping')
      symlinkSync(join(root, 'backend/query.go'), join(root, 'backend/escape.go'))
      expect(() => scanGoSources(join(root, 'backend'))).toThrow('symlink')
    } finally { rmSync(root, { recursive: true, force: true }) }
  })
})

const expected = {
  minimumPercent: 90,
  goSources: ['query.go', 'command_unix.go', 'command_windows.go'].map(file => `backend/${file}`).sort(),
  sourceSnapshot: ['query.go', 'command_unix.go', 'command_windows.go'].map(file => ({ file: `backend/${file}`, sha256: 'a'.repeat(64) })),
  changed: ['command_unix.go', 'command_windows.go', 'query.go'].map(file => ({ file: `backend/${file}`, lines: [2, 3] })),
}

function reports(): GoMatrixInput[] {
  return GO_PLATFORMS.map(rule => {
    const active = ['backend/query.go', `backend/command_${rule.target.startsWith('windows/') ? 'windows' : 'unix'}.go`]
    const excluded = [`backend/command_${rule.target.startsWith('windows/') ? 'unix' : 'windows'}.go`]
    const evidence = { packages: ['fixture'], passedTests: 1 }
    return {
      schemaVersion: 1, status: 'passed', scope: 'current-target', allTargets: 'not-run',
      platform: { target: rule.target, toolchain: 'go1.25.7', native: true, race: rule.race ? 'required' : 'unsupported', ...(!rule.race ? { reason: 'Go 1.25.7 unsupported windows/arm64' } : {}) },
      sources: { active, excluded, unknown: [], formatted: expected.goSources },
      sourceSnapshot: expected.sourceSnapshot, changed: expected.changed, packages: ['fixture'], unit: evidence,
      ...(rule.race ? { race: evidence } : {}), failures: [],
      changedLines: { minimumPercent: 90, covered: 4, total: 4, pct: 100, failures: [], files: active.map(file => ({ file, covered: 2, total: 2, missing: [] })), skipped: excluded.map(file => ({ file, reason: 'Required on another supported native target' })) },
    }
  })
}

describe('all-target Go coverage closure', () => {
  test('zero-line declaration mappings require the same snapshot-bound AST and Go cover proof', () => {
    const input = reports()
    for (const report of input) {
      report.declarations = [{ file: 'backend/query.go', sha256: 'a'.repeat(64), declarationOnly: true, compilerCounters: 0, declarations: { imports: 0, types: 1, constants: 0, variables: 0 }, executable: [] }]
      const file = report.changedLines!.files.find(file => file.file === 'backend/query.go')!
      file.covered = 0
      file.total = 0
      file.declarationOnly = { sha256: 'a'.repeat(64), compilerCounters: 0 }
      report.changedLines!.covered = 2
      report.changedLines!.total = 2
    }
    expect(evaluateGoPlatformClosure(input, expected).status).toBe('passed')
    for (const scenario of ['missing', 'stale', 'executable', 'nonzero'] as const) {
      const invalid = structuredClone(input)
      if (scenario === 'missing') invalid[0].declarations = []
      if (scenario === 'stale') invalid[0].declarations![0].sha256 = 'b'.repeat(64)
      if (scenario === 'executable') invalid[0].declarations![0].executable = [{ kind: 'initializer-call', line: 3 }]
      if (scenario === 'nonzero') invalid[0].changedLines!.files[0].declarationOnly!.compilerCounters = 1
      expect(evaluateGoPlatformClosure(invalid, expected).status).toBe('failed')
    }
  })
  test('resource bytes participate in freshness while only Go sources require formatting and coverage', () => {
    const complete = { ...expected, sourceSnapshot: [...expected.sourceSnapshot, { file: 'backend/spec.json', sha256: 'c'.repeat(64) }, { file: 'backend/vendor/asset.go', sha256: 'd'.repeat(64) }] }
    const input = structuredClone(reports())
    for (const report of input) report.sourceSnapshot = structuredClone(complete.sourceSnapshot)
    expect(evaluateGoPlatformClosure(input, complete).status).toBe('passed')
    input[0].sourceSnapshot!.find(entry => entry.file === 'backend/spec.json')!.sha256 = 'e'.repeat(64)
    expect(evaluateGoPlatformClosure(input, complete).failures.some(failure => failure.includes('snapshot'))).toBe(true)
  })

  test('accepts all six actual native results with independent coverage and the explicit Windows ARM race limitation', () => {
    const result = evaluateGoPlatformClosure(reports(), expected)
    expect(result.status).toBe('passed')
    expect(result.scope).toBe('all-supported-native-targets')
    expect(result.targets).toHaveLength(6)
  })

  test('keeps compiled no-test packages distinct and rejects unclassified or fabricated skips', () => {
    const input = structuredClone(reports())
    for (const report of input) {
      report.packages.push('fixture/types')
      report.packagesWithoutTestFiles = ['fixture/types']
      report.unit!.noTestPackages = ['fixture/types']
      if (report.race) report.race.noTestPackages = ['fixture/types']
    }
    expect(evaluateGoPlatformClosure(input, expected).status).toBe('passed')
    const undeclared = structuredClone(input)
    undeclared[0].packagesWithoutTestFiles = []
    expect(evaluateGoPlatformClosure(undeclared, expected).status).toBe('failed')
    const fabricatedPass = structuredClone(input)
    fabricatedPass[0].unit!.packages.push('fixture/types')
    expect(evaluateGoPlatformClosure(fabricatedPass, expected).status).toBe('failed')
    const noTests = structuredClone(input)
    noTests[0].unit!.passedTests = 0
    expect(evaluateGoPlatformClosure(noTests, expected).status).toBe('failed')
    const absentRaceCompile = structuredClone(input)
    absentRaceCompile[0].race!.noTestPackages = []
    expect(evaluateGoPlatformClosure(absentRaceCompile, expected).status).toBe('failed')
  })

  test.each(['missing-target', 'duplicate-target', 'crosscompile', 'no-unit', 'no-race', 'fake-arm-race', 'below', 'lower-minimum', 'stale', 'unformatted', 'missing-mapping', 'zero-exclusion', 'never-active', 'counts'] as const)('rejects incomplete or misleading evidence: %s', scenario => {
    const input = structuredClone(reports())
    if (scenario === 'missing-target') input.pop()
    if (scenario === 'duplicate-target') input.push(input[0])
    if (scenario === 'crosscompile') input[0].platform!.native = false as true
    if (scenario === 'no-unit') input[0].unit = undefined
    if (scenario === 'no-race') input[0].race = undefined
    if (scenario === 'fake-arm-race') input[5].race = input[0].race
    if (scenario === 'below') { input[0].changedLines!.covered = 2; input[0].changedLines!.pct = 50; input[0].changedLines!.files[0].covered = 0; input[0].changedLines!.files[0].missing = [2, 3] }
    if (scenario === 'lower-minimum') input[0].changedLines!.minimumPercent = 80
    if (scenario === 'stale') input[0].sourceSnapshot![0].sha256 = 'b'.repeat(64)
    if (scenario === 'unformatted') input[0].sources!.formatted.pop()
    if (scenario === 'missing-mapping') input[0].changedLines!.files.pop()
    if (scenario === 'zero-exclusion') input[0].changedLines!.files.push({ file: input[0].sources!.excluded[0], total: 0, covered: 0, missing: [] })
    if (scenario === 'never-active') {
      for (const report of input) {
        report.sources!.active = report.sources!.active.filter(file => file !== 'backend/query.go')
        report.sources!.excluded.push('backend/query.go')
        report.changedLines!.files = report.changedLines!.files.filter(file => file.file !== 'backend/query.go')
        report.changedLines!.covered = 2; report.changedLines!.total = 2
        report.changedLines!.skipped.push({ file: 'backend/query.go', reason: 'Required on another supported native target' })
      }
    }
    if (scenario === 'counts') input[0].changedLines!.files[0].missing = [2]
    const result = evaluateGoPlatformClosure(input, expected)
    expect(result.status).toBe('failed')
    expect(result.failures.length).toBeGreaterThan(0)
    if (scenario === 'never-active') expect(result.failures.some(failure => failure.includes('never active'))).toBe(true)
  })
})
