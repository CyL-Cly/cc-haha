import { describe, expect, test } from 'bun:test'
import { createHash } from 'node:crypto'
import { closeSync, constants, existsSync, lstatSync, mkdirSync, mkdtempSync, openSync, readFileSync, realpathSync, renameSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { setImmediate as nextTurn } from 'node:timers/promises'
import { goNativeTarget } from './go-platforms'
import { prepareGoDependencies } from './go-dependencies'
import {
  batchGoFormatCommands,
  collectGoChangedLines,
  collectGoDeclarationEvidence,
  collectGoSourcePaths,
  collectGoNoTestPackages,
  collectGoTestEvidence,
  createGoTestEnvironment,
  evaluateGoChangedCoverage,
  parseGoCoverageProfile,
  parseGoCoverageProfileFile,
  runGoChecks,
} from './run-go-tests'

const modulePath = 'example.test/backend'
const passingEvents = [
  { Action: 'run', Package: modulePath, Test: 'TestFixture' },
  { Action: 'pass', Package: modulePath, Test: 'TestFixture' },
  { Action: 'pass', Package: modulePath },
].map(event => JSON.stringify(event)).join('\n')

function tempRoot() {
  return mkdtempSync(join(tmpdir(), 'go-checks-regression-'))
}

describe('offline Go checks', () => {
  test.each([1, 2, 3, 7, 64 * 1024])('streams physical coverage with UTF-8, CRLF and record boundaries at %i bytes', async highWaterMark => {
    const root = tempRoot()
    try {
      const path = join(root, 'coverage.out')
      const name = '目录/🧪-\uFEFF.go'
      const profile = [
        '\uFEFF \r\n\tmode: atomic',
        `${modulePath}/${name}:3.1,6.1 2 0`,
        `${modulePath}/${name}:3.1,6.1 2 9`,
        `${modulePath}/${name}:5.2,7.4 1 0`,
        `${modulePath}/empty.go:0.0,0.0 0 0`,
        'go.mau.fi/libsignal/value.go:9.1,9.8 1 1',
        '',
        ' \t\u2028',
      ].join('\r\n')
      writeFileSync(path, profile)
      const modules = [{ path: 'go.mau.fi/libsignal', version: 'v0.2.1', directory: 'backend/third_party/libsignal-node' }]
      const coverage = await parseGoCoverageProfileFile(path, modulePath, modules, { highWaterMark })
      expect(coverage).toEqual(new Map([
        [`backend/${name}`, { executable: new Set([3, 4, 5, 6, 7]), covered: new Set([3, 4]) }],
        ['backend/empty.go', { executable: new Set(), covered: new Set() }],
        ['backend/third_party/libsignal-node/value.go', { executable: new Set([9]), covered: new Set([9]) }],
      ]))
      expect(coverage).toEqual(parseGoCoverageProfile(profile, modulePath, modules))
      const changed = new Map([[`backend/${name}`, new Set([3, 4, 5])]])
      expect(evaluateGoChangedCoverage(changed, coverage, 90)).toEqual(evaluateGoChangedCoverage(changed, parseGoCoverageProfile(profile, modulePath, modules), 90))
      expect(evaluateGoChangedCoverage(changed, coverage, 90).failures).toEqual(['Go changed-lines: coverage 66.67% is below the existing 90% minimum'])
    } finally { rmSync(root, { recursive: true, force: true }) }
  })

  test.each([
    '', '\uFEFF \r\n\t', 'mode: set\n', 'mode: atomic\n',
    `mode: atomic \n${modulePath}/file.go:1.1,1.8 1 1`,
    `mode: atomic\n\t\n${modulePath}/file.go:1.1,1.8 1 1`,
    `mode: atomic\n${modulePath}/file.go:1.1,1.8 1 1\r${modulePath}/file.go:2.1,2.8 1 1`,
    `mode: atomic\ninvalid\n`,
    `mode: atomic\nforeign.test/file.go:1.1,1.8 1 1`,
    `mode: atomic\n${modulePath}/../file.go:1.1,1.8 1 1`,
    `mode: atomic\n${modulePath}/file.go:0.1,1.8 1 1`,
    `mode: atomic\n${modulePath}/file.go:3.1,2.8 1 1`,
    `mode: atomic\n${modulePath}/file.go:1.1,1.8 1 1\n${modulePath}/file.go:1.1,1.8 2 0`,
  ])('keeps streamed coverage rejection and original error precedence for %#', async profile => {
    const root = tempRoot()
    try {
      const path = join(root, 'coverage.out')
      writeFileSync(path, profile)
      let originalError: string | undefined
      try { parseGoCoverageProfile(profile, modulePath) } catch (error) { originalError = (error as Error).message }
      expect(originalError).toBeDefined()
      for (const highWaterMark of [1, 7, 64 * 1024]) {
        let streamedError: string | undefined
        try { await parseGoCoverageProfileFile(path, modulePath, [], { highWaterMark }) } catch (error) { streamedError = (error as Error).message }
        expect(streamedError).toBe(originalError)
      }
    } finally { rmSync(root, { recursive: true, force: true }) }
  })

  test('streams trailing profile whitespace without trimming interior paths and handles read failures', async () => {
    const root = tempRoot()
    try {
      const path = join(root, 'coverage.out')
      const profiles = [
        `mode: atomic\n${modulePath}/file.go:1.1,1.8 1 1`,
        ` \nmode: atomic\n\n${modulePath}/file.go:1.1,1.8 1 1 \t\r\n\t\r\n`,
        `mode: atomic\n${modulePath}/marker.go:0.0,0.0 0 0\n`,
      ]
      for (const profile of profiles) {
        writeFileSync(path, profile)
        expect(await parseGoCoverageProfileFile(path, modulePath, [], { highWaterMark: 2 })).toEqual(parseGoCoverageProfile(profile, modulePath))
      }
      writeFileSync(path, `mode: atomic\n ${modulePath}/file.go:1.1,1.8 1 1\n`)
      await expect(parseGoCoverageProfileFile(path, modulePath)).rejects.toThrow('outside the backend module')
      await expect(parseGoCoverageProfileFile(join(root, 'absent'), modulePath)).rejects.toThrow('ENOENT')
    } finally { rmSync(root, { recursive: true, force: true }) }
  })

  test('formatting checks only exact module source files and reject empty or escaping inventory', () => {
    expect(collectGoSourcePaths('/fixture/backend/query.go\n/fixture/backend/query_test.go\n/fixture/backend/query.go\n', '/fixture/backend'))
      .toEqual(['/fixture/backend/query.go', '/fixture/backend/query_test.go'])
    expect(() => collectGoSourcePaths('', '/fixture/backend')).toThrow('no source files')
    expect(() => collectGoSourcePaths('/fixture/backend/../other.go', '/fixture/backend')).toThrow('outside')
    expect(() => collectGoSourcePaths('query.go', '/fixture/backend')).toThrow('outside')
  })
  test('formatting argv budgets count UTF-8 bytes and preserve boundary entries', () => {
    const prefixBytes = Buffer.byteLength('gofmt\0-l\0', 'utf8')
    const paths = ['/目录/🧪.go', '/second.go', '/目录/🧪.go']
    const budget = prefixBytes + Buffer.byteLength(paths[0], 'utf8') + 1
    expect(paths[0].length + prefixBytes + 1).toBeLessThan(budget)
    expect(batchGoFormatCommands(paths, budget)).toEqual(paths.map(path => ['gofmt', '-l', path]))
    expect(batchGoFormatCommands(paths, budget).flatMap(command => command.slice(2))).toEqual(paths)
    expect(() => batchGoFormatCommands([], budget)).toThrow('inventory is empty')
    for (const invalid of [0, prefixBytes, -1, 1.5, Number.NaN, Number.POSITIVE_INFINITY]) {
      expect(() => batchGoFormatCommands(paths, invalid)).toThrow('budget is invalid')
    }
    expect(() => batchGoFormatCommands(['/small.go', '/very-long.go'], prefixBytes + 11)).toThrow('source exceeds')
  })
  test.each(['formatted', 'unformatted', 'failed-batch'] as const)('formatting batches keep every long UTF-8 source path in the actual lane: %s', async mode => {
    const root = tempRoot()
    const commands: string[][] = []
    const budget = 96 * 1024
    try {
      mkdirSync(join(root, 'backend'), { recursive: true })
      mkdirSync(join(root, 'scripts/quality-gate'), { recursive: true })
      writeFileSync(join(root, 'backend/go.mod'), `module ${modulePath}\n\ngo 1.25.7\n`)
      writeFileSync(join(root, 'scripts/quality-gate/coverage-thresholds.json'), JSON.stringify({ changedLines: { minimumPercent: 90 } }))
      const names = Array.from({ length: 800 }, (_, index) => `源码🧪-${String(index).padStart(4, '0')}-${'目录'.repeat(28)}.go`)
      for (const [index, name] of names.entries()) writeFileSync(join(root, 'backend', name), `package fixture\nvar value${index} = 1\n`)
      writeFileSync(join(root, 'backend/query_test.go'), 'package fixture\n')
      const sources = [...names, 'query_test.go'].map(name => join(root, 'backend', name)).sort()
      expect(sources.reduce((sum, path) => sum + Buffer.byteLength(path, 'utf8') + 1, 0)).toBeGreaterThan(budget)
      let formattingBatches = 0
      const unformatted: string[] = []
      const report = await runGoChecks({ rootDir: root, artifactDir: join(root, 'artifacts'), sourceEnvironment: { PATH: '/fixture/bin' },
        async execute(command) {
          commands.push(command)
          let stdout = ''
          if (command[0] === 'gofmt') {
            if (command.reduce((sum, arg) => sum + Buffer.byteLength(arg, 'utf8') + 1, 0) > budget) return { exitCode: 7, stdout: '', stderr: 'fixture E2BIG: full UTF-8 argv exceeds formatting budget' }
            formattingBatches++
            if (mode === 'failed-batch' && formattingBatches === 2) return { exitCode: 9, stdout: '', stderr: 'fixture formatting batch failed' }
            if (mode === 'unformatted') {
              const path = command[2]!
              unformatted.push(path)
              stdout = `${path}\n`
            }
          } else if (command[1] === 'env') {
            const [GOOS, GOARCH] = goNativeTarget().split('/')
            stdout = JSON.stringify({ GOOS, GOARCH, GOVERSION: 'go1.25.7' })
          } else if (command[1] === 'mod') stdout = JSON.stringify({ Require: [] })
          else if (command.includes('-m')) stdout = JSON.stringify({ Path: modulePath, Dir: join(root, 'backend') })
          else if (command[1] === 'list' && command.includes('-e')) stdout = JSON.stringify({ ImportPath: modulePath, Dir: join(root, 'backend'), GoFiles: names, TestGoFiles: ['query_test.go'] })
          else if (command[1] === 'list') stdout = modulePath
          else if (command[1] === 'test') {
            stdout = passingEvents
            const profile = command.find(arg => arg.startsWith('-coverprofile='))
            if (profile) writeFileSync(profile.slice('-coverprofile='.length), 'mode: atomic\n' + names.map(name => `${modulePath}/${name}:2.1,2.20 1 1\n`).join(''))
          } else if (command.includes('merge-base')) stdout = 'a'.repeat(40)
          else if (command.includes('--cached')) stdout = ['backend/go.mod', 'scripts/quality-gate/coverage-thresholds.json', ...sources.map(path => 'backend/' + path.slice(join(root, 'backend').length + 1))].join('\0') + '\0'
          return { exitCode: 0, stdout, stderr: '' }
        },
      })
      const formatting = commands.filter(command => command[0] === 'gofmt')
      expect(formatting.length).toBeGreaterThan(1)
      const checkedSources = formatting.flatMap(command => command.slice(2))
      expect(checkedSources).toEqual(mode === 'failed-batch' ? sources.slice(0, checkedSources.length) : sources)
      expect(new Set(checkedSources).size).toBe(checkedSources.length)
      expect(formatting.every(command => command.reduce((bytes, arg) => bytes + Buffer.byteLength(arg, 'utf8') + 1, 0) <= budget)).toBe(true)
      expect(report.sources?.formatted.length).toBe(sources.length)
      if (mode === 'formatted') {
        expect(report.status).toBe('passed')
        expect(report.failures).toEqual([])
        expect(report.changedLines?.minimumPercent).toBe(90)
      } else {
        expect(report.status).toBe('failed')
        expect(commands.some(command => command[0] === 'go' && ['build', 'vet', 'test'].includes(command[1]!))).toBe(false)
        if (mode === 'unformatted') expect(report.failures).toEqual([`Go source is not formatted; run gofmt locally:\n${unformatted.join('\n')}`])
        else {
          expect(formattingBatches).toBe(2)
          expect(checkedSources.length).toBeLessThan(sources.length)
          expect(report.failures).toHaveLength(1)
          expect(report.failures[0]).toContain('exited 9: fixture formatting batch failed')
        }
      }
    } finally { rmSync(root, { recursive: true, force: true }) }
  })
  test('isolates all user and Go state and drops provider credentials and inherited Go config', () => {
    const home = tempRoot()
    try {
      const env = createGoTestEnvironment(home, {
        PATH: '/fixture/bin', HOME: '/private-user', CLAUDE_CONFIG_DIR: '/private-config',
        ANTHROPIC_API_KEY: 'must-not-leak', OPENAI_API_KEY: 'must-not-leak',
        GOENV: '/private/go/env', GOFLAGS: '-overlay=/private/config', GOPROXY: 'https://public.test',
      })
      expect(env.HOME).toBe(home)
      expect(env.CLAUDE_CONFIG_DIR).toBe(join(home, '.claude'))
      expect(env.GOENV).toBe('off')
      expect(env.GOTOOLCHAIN).toBe('local')
      expect(env.GOPROXY).toBe('off')
      expect(env.GOSUMDB).toBe('off')
      expect(env.GOWORK).toBe('off')
      expect(env.GOFLAGS).toBe('-mod=readonly')
      expect(env.GOCACHE).toBe(join(home, 'go-build'))
      expect(env.GOMODCACHE).toBe(join(home, 'go/pkg/mod'))
      expect(env.ANTHROPIC_API_KEY).toBeUndefined()
      expect(env.OPENAI_API_KEY).toBeUndefined()
    } finally {
      rmSync(home, { recursive: true, force: true })
    }
  })

  test('requires actual tests and terminal evidence for every discovered package', () => {
    expect(collectGoTestEvidence(passingEvents, [modulePath])).toEqual({ packages: [modulePath], passedTests: 1 })
    expect(() => collectGoTestEvidence(JSON.stringify({ Action: 'pass', Package: modulePath }), [modulePath])).toThrow('no passing tests')
    expect(() => collectGoTestEvidence(passingEvents, [modulePath, `${modulePath}/missing`])).toThrow('missing successful package results')
    expect(() => collectGoTestEvidence(`${passingEvents}\n${JSON.stringify({ Action: 'fail', Package: modulePath })}`, [modulePath])).toThrow('Go test failed')
    expect(() => collectGoTestEvidence('not JSON', [modulePath])).toThrow()
  })

  test('records actual instrumented no-test terminal receipts without counting them as passing tests', () => {
    const pkg = `${modulePath}/untested`
    // Actual Go1.25.7 -json/-coverpkg receipt shape from the physical nested
    // module regression below; only the disposable package name is replaced.
    const events = [
      { Action: 'start', Package: pkg },
      { Action: 'output', Package: pkg, Output: `\t${pkg}\t\tcoverage: 0.0% of statements\n` },
      { Action: 'pass', Package: pkg },
    ]
    const encoded = (rows = events) => `${passingEvents}\n${rows.map(row => JSON.stringify(row)).join('\n')}`
    expect(collectGoTestEvidence(encoded(), [modulePath, pkg], [pkg])).toEqual({
      packages: [modulePath], passedTests: 1, noTestPackages: [pkg],
    })
    expect(collectGoTestEvidence(encoded(events.map(row => row.Action === 'output'
      ? { ...row, Output: `\t${pkg}\t\tcoverage: [no statements]\n` } : row)), [modulePath, pkg], [pkg])).toMatchObject({ noTestPackages: [pkg] })
    expect(() => collectGoTestEvidence(events.map(row => JSON.stringify(row)).join('\n'), [pkg], [pkg])).toThrow('no passing tests')
    expect(() => collectGoTestEvidence(encoded(events.filter(row => row.Action !== 'start')), [modulePath, pkg], [pkg])).toThrow('missing successful package results')
    expect(() => collectGoTestEvidence(encoded(events.filter(row => row.Action !== 'output')), [modulePath, pkg], [pkg])).toThrow('missing successful package results')
    expect(() => collectGoTestEvidence(encoded(events.map(row => row.Action === 'output' ? { ...row, Output: '\tforeign.test/package\t\tcoverage: 0.0% of statements\n' } : row)), [modulePath, pkg], [pkg])).toThrow('missing successful package results')
    expect(() => collectGoTestEvidence(encoded([...events, { Action: 'skip', Package: pkg }]), [modulePath, pkg], [pkg])).toThrow('missing successful package results')
    expect(() => collectGoTestEvidence(encoded([...events, { Action: 'fail', Package: pkg }]), [modulePath, pkg], [pkg])).toThrow('Go test failed')
    // A declared no-test inventory cannot conceal a real test event.
    const actualSkippedTest = encoded() + '\n' + JSON.stringify({ Action: 'skip', Package: pkg, Test: 'TestSkipped' })
    expect(() => collectGoTestEvidence(actualSkippedTest, [modulePath, pkg], [pkg])).toThrow('missing successful package results')
  })

  test('records a compiled package without test files separately from passing tests', () => {
    const types = `${modulePath}/types`
    const noTests = [
      { Action: 'start', Package: types },
      { Action: 'output', Package: types, Output: `?   \t${types}\t[no test files]\n` },
      { Action: 'skip', Package: types },
    ].map(event => JSON.stringify(event)).join('\n')
    const output = `${passingEvents}\n${noTests}`
    expect(collectGoTestEvidence(output, [modulePath, types], [types])).toEqual({
      packages: [modulePath], passedTests: 1, noTestPackages: [types],
    })
    expect(() => collectGoTestEvidence(output, [modulePath, types])).toThrow('missing successful package results')
    expect(() => collectGoTestEvidence(noTests, [types], [types])).toThrow('no passing tests')
    expect(() => collectGoTestEvidence(`${output}\n${JSON.stringify({ Action: 'fail', Package: types })}`, [modulePath, types], [types])).toThrow('Go test failed')
    const missingCompile = `${passingEvents}\n${JSON.stringify({ Action: 'skip', Package: types })}`
    expect(() => collectGoTestEvidence(missingCompile, [modulePath, types], [types])).toThrow('missing successful package results')
    const skippedTest = `${passingEvents}\n${JSON.stringify({ Action: 'skip', Package: types, Test: 'TestSkipped' })}`
    expect(() => collectGoTestEvidence(skippedTest, [modulePath, types], [types])).toThrow('missing successful package results')
    const skippedWholeSuite = `${output}\n${JSON.stringify({ Action: 'run', Package: types, Test: 'TestSkipped' })}`
    expect(() => collectGoTestEvidence(skippedWholeSuite, [modulePath, types], [types])).toThrow('missing successful package results')
  })

  test('uses actual package test source inventory rather than accepting arbitrary skipped suites', () => {
    const types = `${modulePath}/types`
    const inventory = [
      { ImportPath: modulePath, TestGoFiles: ['query_test.go'] },
      { ImportPath: types, GoFiles: ['types.go'] },
      { ImportPath: `${modulePath}/external`, XTestGoFiles: ['external_test.go'] },
      { ImportPath: `${modulePath}/testdata/ignored`, Error: { Err: 'not in execution scope' } },
    ].map(pkg => JSON.stringify(pkg)).join('\n')
    expect(collectGoNoTestPackages(inventory, [modulePath, types, `${modulePath}/external`])).toEqual([types])
    expect(() => collectGoNoTestPackages(inventory, [modulePath, `${modulePath}/missing`])).toThrow('missing discovered packages')
    expect(() => collectGoNoTestPackages(`${inventory}\n${JSON.stringify({ ImportPath: types })}`, [types])).toThrow('Invalid Go test package inventory')
    expect(() => collectGoNoTestPackages(JSON.stringify({ ImportPath: types, TestGoFiles: 'invalid' }), [types])).toThrow('Invalid Go test source inventory')
    expect(() => collectGoNoTestPackages(JSON.stringify({ ImportPath: types, Error: { Err: 'compiler error' } }), [types])).toThrow('Invalid Go test package inventory')
  })

  test('maps covered blocks conservatively when a line intersects an uncovered branch', () => {
    const coverage = parseGoCoverageProfile([
      'mode: atomic',
      `${modulePath}/query.go:3.10,6.1 2 1`,
      `${modulePath}/query.go:5.2,7.4 1 0`,
    ].join('\n'), modulePath).get('backend/query.go')!
    expect([...coverage.executable]).toEqual([3, 4, 5, 6, 7])
    expect([...coverage.covered]).toEqual([3, 4])
    expect(() => parseGoCoverageProfile('mode: atomic\n', modulePath)).toThrow('no executable source blocks')
    expect(() => parseGoCoverageProfile('mode: set\n', modulePath)).toThrow('atomic')
    expect(() => parseGoCoverageProfile('mode: atomic\ninvalid', modulePath)).toThrow('Malformed')
    expect(() => parseGoCoverageProfile('mode: atomic\nelsewhere/query.go:1.1,2.2 1 1', modulePath)).toThrow('outside the backend module')
  })

  test('merges identical coverpkg blocks across package processes before measuring line coverage', () => {
    const coverage = parseGoCoverageProfile([
      'mode: atomic', `${modulePath}/query.go:3.1,4.9 2 0`, `${modulePath}/query.go:3.1,4.9 2 7`,
    ].join('\n'), modulePath)
    expect([...coverage.get('backend/query.go')!.covered]).toEqual([3, 4])
    expect(() => parseGoCoverageProfile([
      'mode: atomic', `${modulePath}/query.go:3.1,4.9 2 0`, `${modulePath}/query.go:3.1,4.9 3 7`,
    ].join('\n'), modulePath)).toThrow('Conflicting Go coverage statement count')
  })

  test('enforces the existing 90 percent minimum and fails missing source mappings', () => {
    const profile = `mode: atomic\n${modulePath}/query.go:3.1,11.2 9 1\n${modulePath}/query.go:12.1,12.5 1 0\n`
    const coverage = parseGoCoverageProfile(profile, modulePath)
    const changed = new Map([['backend/query.go', new Set(Array.from({ length: 10 }, (_, index) => index + 3))]])
    const atMinimum = evaluateGoChangedCoverage(changed, coverage, 90)
    expect(atMinimum.pct).toBe(90)
    expect(atMinimum.failures).toEqual([])
    const below = evaluateGoChangedCoverage(new Map([['backend/query.go', new Set([11, 12])]]), coverage, 90)
    expect(below.pct).toBe(50)
    expect(below.failures[0]).toContain('existing 90% minimum')
    const missing = evaluateGoChangedCoverage(new Map([['backend/missing.go', new Set([3])]]), coverage, 90)
    expect(missing.failures).toContain('backend/missing.go: missing Go coverage source mapping')
    expect(() => evaluateGoChangedCoverage(changed, coverage, Number.NaN)).toThrow('Missing or invalid')
  })

  test('reports compiler-proven declarations as zero executable lines while retaining missing mappings and the 90 percent gate', () => {
    const changed = new Map([
      ['backend/types.go', new Set([1, 2, 3])],
      ['backend/query.go', new Set([3, 4])],
      ['backend/initializer.go', new Set([3])],
    ])
    const coverage = parseGoCoverageProfile(`mode: atomic\n${modulePath}/query.go:3.1,3.9 1 1\n${modulePath}/query.go:4.1,4.9 1 0\n`, modulePath)
    const declarations = new Map([['backend/types.go', { file: 'backend/types.go', sha256: 'a'.repeat(64), declarationOnly: true, compilerCounters: 0, declarations: { imports: 1, types: 1, constants: 0, variables: 0 }, executable: [] }]])
    const result = evaluateGoChangedCoverage(changed, coverage, 90, undefined, declarations)
    expect(result.files.find(file => file.file === 'backend/types.go')).toMatchObject({ covered: 0, total: 0, missing: [], declarationOnly: { sha256: 'a'.repeat(64), compilerCounters: 0 } })
    expect(result.failures).not.toContain('backend/types.go: missing Go coverage source mapping')
    expect(result.failures).toContain('backend/initializer.go: missing Go coverage source mapping')
    expect(result.covered).toBe(1)
    expect(result.total).toBe(2)
    expect(result.pct).toBe(50)
    expect(result.failures).toContain('Go changed-lines: coverage 50.00% is below the existing 90% minimum')
  })

  test('keeps real package initializers unmapped even when Go cover emits no counters', () => {
    const file = 'backend/initializer.go'
    const declarations = new Map([[file, { file, sha256: 'b'.repeat(64), declarationOnly: false, compilerCounters: 0, declarations: { imports: 1, types: 0, constants: 0, variables: 1 }, executable: [{ kind: 'initializer-call', line: 3 }] }]])
    const coverage = parseGoCoverageProfile(`mode: atomic\n${modulePath}/query.go:3.1,3.9 1 1\n${modulePath}/query.go:4.1,4.9 1 0\n`, modulePath)
    const result = evaluateGoChangedCoverage(new Map([[file, new Set([3])], ['backend/query.go', new Set([3, 4])]]), coverage, 90, undefined, declarations)
    expect(result.unmappedExecutable).toEqual([{ file, sha256: 'b'.repeat(64), compilerCounters: 0, forms: [{ kind: 'initializer-call', line: 3 }] }])
    expect(result.files.some(row => row.file === file)).toBe(false)
    expect(result.covered).toBe(1)
    expect(result.total).toBe(2)
    expect(result.pct).toBe(50)
    expect(result.failures).toEqual([`${file}: missing Go coverage source mapping`, 'Go changed-lines: coverage 50.00% is below the existing 90% minimum'])
  })

  test('uses actual Go AST and cover instrumentation without running inspected initializer expressions', async () => {
    const root = tempRoot()
    const home = tempRoot()
    try {
      mkdirSync(join(root, 'backend'))
      writeFileSync(join(root, 'backend/go.mod'), 'module example.test/declarations\n\ngo 1.25.7\n')
      const cases = {
        'ordinary.go': 'package fixture\nimport "context"\ntype Example interface { Run(context.Context) error }\nconst text = "func fake() { invoke() }"\nvar zero string\n',
        'static.go': 'package fixture\nvar table = map[string]string{"a":"b"}\nvar list = []struct{ Name string; Value int }{{Name:"example",Value:-2}}\n',
        'embedded.go': 'package fixture\nimport _ "embed"\n//go:embed data.txt\nvar data string\n',
        'empty.go': 'package fixture\nfunc empty() {}\n',
        'normal.go': 'package fixture\nfunc ordinary() int { return 1 }\n',
        'init.go': 'package fixture\nfunc init() {}\n',
        'initializer.go': 'package fixture\nimport "os"\nvar effect = os.WriteFile("MUST_NOT_EXECUTE",nil,0600)\n',
        'closure.go': 'package fixture\nvar deferred = func() int { return 2 }\n',
        'invoked.go': 'package fixture\nvar value = func() int { return 2 }()\n',
        'dynamic.go': 'package fixture\nvar first = 1\nvar copy = first\n',
        'map-key.go': 'package fixture\nvar dynamicKey = "name"\nvar tableKey = map[string]int{dynamicKey:1}\n',
        'external.go': 'package fixture\nfunc assemblyImplementation()\n',
      }
      writeFileSync(join(root, 'backend/data.txt'), 'embedded-only source\n')
      for (const [file, source] of Object.entries(cases)) writeFileSync(join(root, 'backend', file), source)
      const files = Object.keys(cases).map(file => `backend/${file}`)
      const snapshot = files.map(file => ({ file, sha256: createHash('sha256').update(readFileSync(join(root, file))).digest('hex') }))
      const env = createGoTestEnvironment(home, { PATH: process.env.PATH })
      const evidence = await collectGoDeclarationEvidence(root, files, snapshot, env)
      for (const file of ['ordinary.go', 'static.go', 'embedded.go']) {
        expect(evidence.get(`backend/${file}`)).toMatchObject({ declarationOnly: true, compilerCounters: 0, executable: [] })
      }
      for (const file of ['empty.go', 'normal.go', 'init.go', 'initializer.go', 'closure.go', 'invoked.go', 'dynamic.go', 'map-key.go', 'external.go']) {
        expect(evidence.get(`backend/${file}`)?.declarationOnly).toBe(false)
        expect(evidence.get(`backend/${file}`)?.executable.length).toBeGreaterThan(0)
      }
      expect(evidence.get('backend/initializer.go')?.executable).toEqual([{ kind: 'initializer-call', line: 3 }])
      expect(evidence.get('backend/initializer.go')?.compilerCounters).toBe(0)
      expect(evidence.get('backend/empty.go')?.compilerCounters).toBeGreaterThan(0)
      expect(evidence.get('backend/closure.go')?.compilerCounters).toBeGreaterThan(0)
      expect(evidence.get('backend/invoked.go')?.executable.map(form => form.kind)).toEqual(['initializer-call', 'initializer-function-literal'])
      expect(existsSync(join(root, 'backend/MUST_NOT_EXECUTE'))).toBe(false)
      // Evidence is bound to its exact current module snapshot, not a name or
      // substring such as "types.go" or the text "func" inside a constant.
      const stale = snapshot.map(row => ({ ...row, sha256: 'f'.repeat(64) }))
      await expect(collectGoDeclarationEvidence(root, ['backend/ordinary.go'], stale, env)).rejects.toThrow('Invalid or stale')
      writeFileSync(join(root, 'backend/broken.go'), 'package fixture\ntype Broken struct {\n')
      const broken = { file: 'backend/broken.go', sha256: createHash('sha256').update(readFileSync(join(root, 'backend/broken.go'))).digest('hex') }
      await expect(collectGoDeclarationEvidence(root, [broken.file], [broken], env)).rejects.toThrow('AST/cover inspection failed')
    } finally {
      rmSync(root, { recursive: true, force: true })
      rmSync(home, { recursive: true, force: true })
    }
  }, 120000)

  test.each(['missing', 'extra', 'stale', 'nonzero', 'body', 'false-pure', 'uninspected-executable', 'negative-executable'] as const)('fails malformed declaration compiler evidence: %s', async scenario => {
    const root = tempRoot()
    try {
      mkdirSync(join(root, 'backend'))
      const file = 'backend/ordinary.go'
      const row = { file, sha256: 'a'.repeat(64), declarationOnly: true, compilerCounters: 0, declarations: { imports: 0, types: 1, constants: 0, variables: 0 }, executable: [] as Array<{ kind: string; line: number }> }
      if (scenario === 'extra') row.file = 'backend/unrequested.go'
      if (scenario === 'stale') row.sha256 = 'b'.repeat(64)
      if (scenario === 'nonzero') row.compilerCounters = 1
      if (scenario === 'body') row.executable = [{ kind: 'function-body', line: 3 }]
      if (scenario === 'false-pure') row.declarationOnly = false
      if (scenario === 'uninspected-executable') { row.declarationOnly = false; row.executable = [{ kind: 'initializer-call', line: 3 }]; row.compilerCounters = null as unknown as number }
      if (scenario === 'negative-executable') { row.declarationOnly = false; row.executable = [{ kind: 'initializer-call', line: 3 }]; row.compilerCounters = -1 }
      await expect(collectGoDeclarationEvidence(root, [file], [{ file, sha256: 'a'.repeat(64) }], {}, async () => ({ exitCode: 0, stdout: JSON.stringify(scenario === 'missing' ? [] : [row]), stderr: '' }))).rejects.toThrow()
    } finally { rmSync(root, { recursive: true, force: true }) }
  })

  test('accepts only a validated helper artifact when process stdout is empty and rejects conflicting receipts', async () => {
    const root = tempRoot()
    try {
      mkdirSync(join(root, 'backend'))
      const file = 'backend/ordinary.go'
      const row = { file, sha256: 'a'.repeat(64), declarationOnly: true, compilerCounters: 0, declarations: { imports: 0, types: 1, constants: 0, variables: 0 }, executable: [] }
      const snapshot = [{ file, sha256: row.sha256 }]
      const evidence = await collectGoDeclarationEvidence(root, [file], snapshot, {}, async command => {
        const input = JSON.parse(readFileSync(command.at(-1)!, 'utf8'))
        writeFileSync(input.outputFile, JSON.stringify([row]))
        return { exitCode: 0, stdout: '', stderr: '' }
      })
      expect(evidence.get(file)).toEqual(row)
      await expect(collectGoDeclarationEvidence(root, [file], snapshot, {}, async command => {
        const input = JSON.parse(readFileSync(command.at(-1)!, 'utf8'))
        writeFileSync(input.outputFile, JSON.stringify([row]))
        return { exitCode: 0, stdout: '[]', stderr: '' }
      })).rejects.toThrow('artifact disagrees')
      await expect(collectGoDeclarationEvidence(root, [file], snapshot, {}, async () => ({ exitCode: 0, stdout: '', stderr: '' }))).rejects.toThrow('returned no evidence')
    } finally { rmSync(root, { recursive: true, force: true }) }
  })

  test('maps actual consumer counters from only controlled local modules and keeps uncovered changes below 90 percent', () => {
    const modules = [{ path: 'go.mau.fi/libsignal', version: 'v0.2.1', directory: 'backend/third_party/libsignal-node' }]
    const file = 'backend/third_party/libsignal-node/session/source_node_sessions.go'
    const coverage = parseGoCoverageProfile('mode: atomic\ngo.mau.fi/libsignal/session/source_node_sessions.go:3.1,3.9 1 1\ngo.mau.fi/libsignal/session/source_node_sessions.go:4.1,4.9 1 0\n', modulePath, modules)
    expect([...coverage.get(file)!.covered]).toEqual([3])
    const result = evaluateGoChangedCoverage(new Map([[file, new Set([3, 4])]]), coverage, 90, { active: [file], excluded: [], unknown: [], formatted: [file] })
    expect(result).toMatchObject({ minimumPercent: 90, covered: 1, total: 2, pct: 50 })
    expect(result.failures[0]).toContain('below the existing 90% minimum')
    expect(() => parseGoCoverageProfile('mode: atomic\nunreviewed.test/session.go:3.1,3.9 1 1\n', modulePath, modules)).toThrow('outside')
    expect(() => parseGoCoverageProfile('mode: atomic\ngo.mau.fi/libsignal/../escape.go:3.1,3.9 1 1\n', modulePath, modules)).toThrow('Invalid')
    expect(() => parseGoCoverageProfile('mode: atomic\ngo.mau.fi/libsignal/session.go:3.1,3.9 1 1\n', modulePath, [{ ...modules[0], directory: 'backend/elsewhere' }])).toThrow('controlled')
  })

  test('runs parent and controlled fork test roots with physical coverage and separate no-test receipts', async () => {
    // Match Go's physical module identity inside an aliased macOS TMPDIR.
    const root = realpathSync(tempRoot())
    try {
      const backend = join(root, 'backend'), fork = join(backend, 'third_party/libsignal-node'), seed = join(root, 'seed'), cache = join(root, 'empty-cache')
      mkdirSync(fork, { recursive: true })
      mkdirSync(cache)
      mkdirSync(join(root, 'scripts/quality-gate'), { recursive: true })
      writeFileSync(join(backend, 'go.mod'), 'module example.test/backend\n\ngo 1.25.7\n\nrequire go.mau.fi/libsignal v0.2.1\nreplace go.mau.fi/libsignal => ./third_party/libsignal-node\n')
      writeFileSync(join(backend, 'go.sum'), 'go.mau.fi/libsignal v0.2.1 h1:vRZG4EzTn70XY6Oh/pVKrQGuMHBkAWlGRC22/85m9L0=\ngo.mau.fi/libsignal v0.2.1/go.mod h1:iVvjrHyfQqWajOUaMEsIfo3IqgVMrhWcPiiEzk7NgoU=\n')
      writeFileSync(join(backend, 'query.go'), 'package backend\n\nimport signal "go.mau.fi/libsignal"\n\nfunc Query() string { return signal.Value() }\n')
      writeFileSync(join(backend, 'query_test.go'), 'package backend\n\nimport "testing"\n\nfunc TestQuery(t *testing.T) {\n\tif Query() != "actual-fork-consumer" {\n\t\tt.Fatal(Query())\n\t}\n}\n')
      writeFileSync(join(fork, 'go.mod'), 'module go.mau.fi/libsignal\n\ngo 1.25.7\n')
      writeFileSync(join(fork, 'value.go'), 'package libsignal\n\nfunc Value() string { return "actual-fork-consumer" }\n')
      mkdirSync(join(fork, 'unused'))
      mkdirSync(join(fork, 'untested'))
      writeFileSync(join(fork, 'unused/value.go'), 'package unused\n\nfunc Value() int { return 7 }\n')
      writeFileSync(join(fork, 'unused/value_test.go'), 'package unused\n\nimport \"testing\"\n\nfunc TestUnusedValue(t *testing.T) {\n\tif Value() != 7 {\n\t\tt.Fatal(\"independent nested module test was not executed\")\n\t}\n}\n')
      writeFileSync(join(fork, 'untested/value.go'), 'package untested\n\nfunc NeverCalled() int { return 8 }\n')
      writeFileSync(join(fork, 'LICENSE'), 'private fixture license\n')
      writeFileSync(join(root, 'scripts/quality-gate/coverage-thresholds.json'), '{"changedLines":{"minimumPercent":90}}')
      const sourceEnvironment = { PATH: process.env.PATH, CC_HAHA_GO_DEPENDENCY_SEED: seed }
      await prepareGoDependencies({ rootDir: root, seedDir: seed, sourceCache: cache, sourceEnvironment })
      const coverageOnlyHome = tempRoot()
      try {
        const env = createGoTestEnvironment(coverageOnlyHome, { PATH: process.env.PATH })
        const p = Bun.spawn(['go', 'test', '-covermode=atomic', '-coverpkg=./...,go.mau.fi/libsignal/...', '-coverprofile=' + join(root, 'parent-only.out'), './...'], { cwd: backend, env, stdout: 'pipe', stderr: 'pipe', stdin: 'ignore' })
        const [exitCode, stdout, stderr] = await Promise.all([p.exited, new Response(p.stdout).text(), new Response(p.stderr).text()])
        expect(exitCode, stdout + stderr).toBe(0)
        expect(stdout).not.toContain('TestUnusedValue')
        expect(parseGoCoverageProfile(readFileSync(join(root, 'parent-only.out'), 'utf8'), modulePath, [{ path: 'go.mau.fi/libsignal', version: 'v0.2.1', directory: 'backend/third_party/libsignal-node' }]).has('backend/third_party/libsignal-node/unused/value.go')).toBe(false)
      } finally { rmSync(coverageOnlyHome, { recursive: true, force: true }) }
      const report = await runGoChecks({ rootDir: root, artifactDir: join(root, 'artifacts'), sourceEnvironment,
        execute: async (command, options) => {
          if (command[0] === 'git') return { exitCode: 0, stderr: '', stdout: command.includes('--deleted') ? '' : command.includes('merge-base') ? 'a'.repeat(40) : command.includes('ls-files') ? 'backend/query.go\0backend/third_party/libsignal-node/value.go\0backend/third_party/libsignal-node/unused/value.go\0' : '' }
          const child = Bun.spawn(command, { ...options, stdout: 'pipe', stderr: 'pipe', stdin: 'ignore' })
          const [exitCode, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
          return { exitCode, stdout, stderr }
        },
      })
      expect(report.failures).toEqual([])
      expect(report.status).toBe('passed')
      expect(report.packages).toEqual(['example.test/backend', 'go.mau.fi/libsignal', 'go.mau.fi/libsignal/untested', 'go.mau.fi/libsignal/unused'])
      expect(report.unit?.packages).toEqual(['example.test/backend', 'go.mau.fi/libsignal/unused'])
      expect(report.unit?.noTestPackages).toEqual(['go.mau.fi/libsignal', 'go.mau.fi/libsignal/untested'])
      expect(report.race?.packages).toEqual(['example.test/backend', 'go.mau.fi/libsignal/unused'])
      expect(report.race?.noTestPackages).toEqual(['go.mau.fi/libsignal', 'go.mau.fi/libsignal/untested'])
      const physicalCoverage = parseGoCoverageProfile(readFileSync(join(root, 'artifacts/coverage.out'), 'utf8'), modulePath, report.controlledModules)
      expect([...physicalCoverage.get('backend/third_party/libsignal-node/unused/value.go')!.covered]).toEqual([3])
      expect([...physicalCoverage.get('backend/third_party/libsignal-node/untested/value.go')!.covered]).toEqual([])
      expect([...physicalCoverage.get('backend/third_party/libsignal-node/untested/value.go')!.executable]).toEqual([3])
      expect(report.controlledModules).toEqual([{ path: 'go.mau.fi/libsignal', version: 'v0.2.1', directory: 'backend/third_party/libsignal-node' }])
      expect(report.testTargets).toEqual(['./...', 'go.mau.fi/libsignal/...'])
      expect(report.coveragePackages).toEqual(['./...', 'go.mau.fi/libsignal/...'])
      expect(report.sources?.unknown).toEqual([])
      expect(report.changedLines).toMatchObject({ minimumPercent: 90, total: 3, covered: 3, pct: 100 })
      expect(report.changedLines?.files.map(file => file.file)).toEqual(['backend/query.go', 'backend/third_party/libsignal-node/unused/value.go', 'backend/third_party/libsignal-node/value.go'])
      expect(report.sourceSnapshot?.filter(file => file.file.startsWith('backend/third_party/'))).toHaveLength(6)
    } finally { rmSync(root, { recursive: true, force: true }) }
  }, 60000)

  test('does not add test fixtures to the production coverage denominator', () => {
    const coverage = parseGoCoverageProfile(`mode: atomic\n${modulePath}/query.go:3.1,3.9 1 1\n`, modulePath)
    const changed = new Map([
      ['backend/query_test.go', new Set([3])], ['backend/testdata/sample.go', new Set([3])],
      ['backend/query.go', new Set([1, 3])],
    ])
    const result = evaluateGoChangedCoverage(changed, coverage, 90)
    expect(result.total).toBe(1)
    expect(result.covered).toBe(1)
    expect(result.files.map(file => file.file)).toEqual(['backend/query.go'])
  })

  test('collects committed, staged and untracked changes and rejects unavailable diffs', async () => {
    const root = tempRoot()
    try {
      mkdirSync(join(root, 'backend'))
      writeFileSync(join(root, 'backend/new.go'), 'package fixture\nfunc value() int { return 1 }\n')
      const changed = await collectGoChangedLines(root, {}, 'base', async command => ({
        exitCode: 0, stderr: '',
        stdout: command.includes('ls-files') ? 'backend/new.go\0'
          : command.includes('merge-base') ? 'a'.repeat(40)
            : '+++ b/backend/query.go\n@@ -1 +1,3 @@\n+package fixture\n+func value() int { return 2 }\n+func next() int { return 3 }\n',
      }))
      expect([...changed.get('backend/query.go')!]).toEqual([1, 2, 3])
      expect([...changed.get('backend/new.go')!]).toEqual([1, 2, 3])
      await expect(collectGoChangedLines(root, {}, 'missing', async () => ({ exitCode: 1, stdout: '', stderr: 'bad revision' }))).rejects.toThrow('Could not determine Go changed lines')
    } finally {
      rmSync(root, { recursive: true, force: true })
    }
  })

  test.each(['format', 'empty-sources', 'build', 'empty-packages', 'empty-tests', 'failed-test-receipts', 'coverage', 'resource-mutation', 'source-oracle-mutation', 'fixture-mutation', 'declarations', 'success'] as const)('does not turn incomplete evidence into a passing lane: %s', async scenario => {
    const root = tempRoot()
    const homes: string[] = []
    const commands: string[][] = []
    try {
      mkdirSync(join(root, 'backend'), { recursive: true })
      mkdirSync(join(root, 'scripts/quality-gate'), { recursive: true })
      writeFileSync(join(root, 'backend/go.mod'), `module ${modulePath}\n\ngo 1.25.7\n`)
      const sourcePath = join(root, 'backend/query.go')
      const originalSource = 'package fixture\nfunc  query() int{return 1}\n'
      writeFileSync(sourcePath, originalSource)
      const testSourcePath = join(root, 'backend/query_test.go')
      writeFileSync(testSourcePath, 'package fixture\nimport "testing"\nfunc TestQuery(t *testing.T) { if query() != 1 { t.Fatal("query") } }\n')
      const declarationPath = join(root, 'backend/types.go')
      if (scenario === 'declarations') writeFileSync(declarationPath, 'package fixture\ntype Message struct { Content string }\n')
      writeFileSync(join(root, 'backend/spec.json'), '{"description":"before"}')
      mkdirSync(join(root, 'src'), { recursive: true })
      mkdirSync(join(root, 'scripts/quality-gate/go-parity'), { recursive: true })
      writeFileSync(join(root, 'src/reference.ts'), 'export const reference = 1\n')
      writeFileSync(join(root, 'scripts/quality-gate/go-parity/reference.fixture.ts'), 'export const fixture = 1\n')
      writeFileSync(join(root, 'scripts/quality-gate/coverage-thresholds.json'), JSON.stringify({ changedLines: { minimumPercent: 90 } }))
      const report = await runGoChecks({
        rootDir: root,
        artifactDir: join(root, 'artifacts'),
        baseRef: 'base',
        sourceEnvironment: { PATH: '/fixture/bin', ANTHROPIC_API_KEY: 'must-not-leak' },
        async execute(command, options) {
          commands.push(command)
          homes.push(options.env.HOME)
          expect(options.env.ANTHROPIC_API_KEY).toBeUndefined()
          expect(options.env.GOPROXY).toBe('off')
          if (command[0] === 'go' && command[1] === 'test') {
            expect(options.env.CC_HAHA_SOURCE_BUN).toBe(process.execPath)
            expect(options.env.CC_HAHA_PINNED_BUN).toBe(process.execPath)
          }
          let stdout = ''
          if (command[1] === 'env') {
            const [GOOS, GOARCH] = goNativeTarget().split('/')
            stdout = JSON.stringify({ GOOS, GOARCH, GOVERSION: 'go1.25.7' })
          }
          else if (command[1] === 'mod' && command[2] === 'edit') stdout = JSON.stringify({ Require: [] })
          else if (command.includes('-m')) stdout = JSON.stringify({ Path: modulePath, Dir: join(root, 'backend') })
          else if (command[1] === 'list' && command.includes('-e')) stdout = scenario === 'empty-sources' ? '' : JSON.stringify({ ImportPath: modulePath, Dir: join(root, 'backend'), GoFiles: scenario === 'declarations' ? ['query.go', 'types.go'] : ['query.go'], TestGoFiles: ['query_test.go'] })
          else if (command[1] === 'list') stdout = scenario === 'empty-packages' ? '' : modulePath
          else if (command[0] === 'gofmt') {
            expect(command).toEqual(['gofmt', '-l', sourcePath, testSourcePath, ...(scenario === 'declarations' ? [declarationPath] : [])])
            stdout = scenario === 'format' ? `${sourcePath}\n` : ''
          }
          else if (command[1] === 'build' && scenario === 'build') return { exitCode: 1, stdout: '', stderr: 'fixture compiler error' }
          else if (command[1] === 'test') {
            if (scenario === 'failed-test-receipts') return { exitCode: 1, stdout: [
              JSON.stringify({ Action: 'output', Package: modulePath, Test: 'TestActualBrokenBoundary', Output: 'actual failure detail retained in the physical log' }),
              JSON.stringify({ Action: 'fail', Package: modulePath, Test: 'TestActualBrokenBoundary' }),
              JSON.stringify({ Action: 'fail', Package: modulePath }),
            ].join('\n'), stderr: '' }
            if (scenario === 'resource-mutation' && command.includes('-race')) writeFileSync(join(root, 'backend/spec.json'), '{"description":"after"}')
            if (scenario === 'source-oracle-mutation' && command.includes('-race')) writeFileSync(join(root, 'src/reference.ts'), 'export const reference = 2\n')
            if (scenario === 'fixture-mutation' && command.includes('-race')) writeFileSync(join(root, 'scripts/quality-gate/go-parity/reference.fixture.ts'), 'export const fixture = 2\n')
            stdout = scenario === 'empty-tests' ? JSON.stringify({ Action: 'pass', Package: modulePath }) : passingEvents
            const profile = command.find(arg => arg.startsWith('-coverprofile='))
            if (profile) writeFileSync(profile.slice('-coverprofile='.length), scenario === 'coverage' ? 'mode: atomic\n' : `mode: atomic\n${modulePath}/query.go:3.1,3.9 1 1\n`)
          }
          else if (command[1] === 'run') stdout = JSON.stringify([{ file: 'backend/types.go', sha256: createHash('sha256').update(readFileSync(declarationPath)).digest('hex'), declarationOnly: true, compilerCounters: 0, declarations: { imports: 0, types: 1, constants: 0, variables: 0 }, executable: [] }])
          else if (command.includes('merge-base')) stdout = 'a'.repeat(40)
          else if (command.includes('--cached')) stdout = ['backend/query.go', 'src/reference.ts', 'scripts/quality-gate/go-parity/reference.fixture.ts'].join('\0') + '\0'
          else if (scenario === 'declarations' && command.includes('ls-files') && !command.includes('--deleted')) stdout = 'backend/types.go\0'
          return { exitCode: 0, stdout, stderr: '' }
        },
      })
      expect(report.status).toBe(scenario === 'success' || scenario === 'declarations' ? 'passed' : 'failed')
      if (scenario === 'success' || scenario === 'declarations') {
        expect(commands.some(command => command[0] === 'gofmt')).toBe(true)
        expect(commands.some(command => command[1] === 'vet')).toBe(true)
        expect(commands.some(command => command.includes('-race'))).toBe(true)
        const packageTests = commands.filter(command => command[1] === 'test')
        expect(packageTests).toHaveLength(2)
        for (const command of packageTests) {
          expect(command).toContain('-p=4')
          expect(command).toContain('-timeout=30m')
          expect(command).toContain('./...')
        }
        expect(report.changedLines?.minimumPercent).toBe(90)
        expect(report.scope).toBe('current-target')
        expect(report.allTargets).toBe('not-run')
        if (scenario === 'declarations') {
          expect(report.declarations).toHaveLength(1)
          expect(report.changedLines?.files).toMatchObject([{ file: 'backend/types.go', covered: 0, total: 0, declarationOnly: { compilerCounters: 0 } }])
          expect(readFileSync(join(root, 'artifacts/report.md'), 'utf8')).toContain('Declaration-only source (Go AST and cover instrumenter; zero executable lines)')
        }
      } else expect(report.failures.length).toBeGreaterThan(0)
      if (scenario === 'format') {
        expect(report.failures[0]).toContain('not formatted')
        expect(commands.some(command => command[1] === 'build')).toBe(false)
      }
      if (scenario === 'resource-mutation') expect(report.failures[0]).toContain('module inputs or thresholds changed')
      if (scenario === 'failed-test-receipts') {
        expect(report.failures[0]).toContain('2 failed test/package receipts')
        expect(report.failures[0]).toContain('TestActualBrokenBoundary')
        expect(report.failures[0]).toContain('Complete physical output: checks.log')
        expect(report.failures[0]).not.toContain('actual failure detail retained in the physical log')
        expect(readFileSync(join(root, 'artifacts/checks.log'), 'utf8')).toContain('actual failure detail retained in the physical log')
        expect(report.race).toBeUndefined()
      }
      if (scenario === 'source-oracle-mutation' || scenario === 'fixture-mutation') expect(report.failures[0]).toContain('Corpus inputs changed')
      expect(readFileSync(sourcePath, 'utf8')).toBe(originalSource)
      expect(JSON.parse(readFileSync(join(root, 'artifacts/report.json'), 'utf8')).status).toBe(report.status)
      expect(homes.length).toBeGreaterThan(0)
      expect(homes.every(home => !existsSync(home))).toBe(true)
    } finally {
      rmSync(root, { recursive: true, force: true })
    }
  })
})

{
  const modules = [{ path: 'go.mau.fi/whatsmeow', version: 'v0.0.0-20260218135554-9cbe80fb25a4', directory: 'backend/third_party/whatsmeow-node' }]
  const zeroProfile = `mode: atomic
go.mau.fi/whatsmeow/proto/extra.go:41.68,41.69 0 0
go.mau.fi/whatsmeow/proto/extra.go:42.68,42.69 0 0
go.mau.fi/whatsmeow/proto/extra.go:43.68,43.69 0 0
go.mau.fi/whatsmeow/proto/instamadilloAddMessage/extra.go:3.54,3.55 0 0
go.mau.fi/whatsmeow/proto/instamadilloDeleteMessage/extra.go:3.57,3.58 0 0
go.mau.fi/whatsmeow/proto/instamadilloSupplementMessage/extra.go:3.61,3.62 0 0
go.mau.fi/whatsmeow/proto/waArmadilloApplication/extra.go:3.46,3.47 0 0
go.mau.fi/whatsmeow/proto/waMultiDevice/extra.go:3.48,3.49 0 0
go.mau.fi/whatsmeow/proto/extra.go:41.68,41.69 0 0
go.mau.fi/whatsmeow/proto/extra.go:42.68,42.69 0 0
go.mau.fi/whatsmeow/proto/extra.go:43.68,43.69 0 0
go.mau.fi/whatsmeow/proto/instamadilloAddMessage/extra.go:3.54,3.55 0 0
go.mau.fi/whatsmeow/proto/instamadilloDeleteMessage/extra.go:3.57,3.58 0 0
go.mau.fi/whatsmeow/proto/instamadilloSupplementMessage/extra.go:3.61,3.62 0 0
go.mau.fi/whatsmeow/proto/waArmadilloApplication/extra.go:3.46,3.47 0 0
go.mau.fi/whatsmeow/proto/waMultiDevice/extra.go:3.48,3.49 0 0
go.mau.fi/whatsmeow/proto/extra.go:41.68,41.69 0 0
go.mau.fi/whatsmeow/proto/extra.go:42.68,42.69 0 0
go.mau.fi/whatsmeow/proto/extra.go:43.68,43.69 0 0
go.mau.fi/whatsmeow/proto/instamadilloAddMessage/extra.go:3.54,3.55 0 0
go.mau.fi/whatsmeow/proto/instamadilloDeleteMessage/extra.go:3.57,3.58 0 0
go.mau.fi/whatsmeow/proto/instamadilloSupplementMessage/extra.go:3.61,3.62 0 0
go.mau.fi/whatsmeow/proto/waArmadilloApplication/extra.go:3.46,3.47 0 0
go.mau.fi/whatsmeow/proto/waMultiDevice/extra.go:3.48,3.49 0 0
go.mau.fi/whatsmeow/proto/extra.go:41.68,41.69 0 0
go.mau.fi/whatsmeow/proto/extra.go:42.68,42.69 0 0
go.mau.fi/whatsmeow/proto/extra.go:43.68,43.69 0 0
go.mau.fi/whatsmeow/proto/instamadilloAddMessage/extra.go:3.54,3.55 0 0
go.mau.fi/whatsmeow/proto/instamadilloDeleteMessage/extra.go:3.57,3.58 0 0
go.mau.fi/whatsmeow/proto/instamadilloSupplementMessage/extra.go:3.61,3.62 0 0
go.mau.fi/whatsmeow/proto/waArmadilloApplication/extra.go:3.46,3.47 0 0
go.mau.fi/whatsmeow/proto/waMultiDevice/extra.go:3.48,3.49 0 0
go.mau.fi/whatsmeow/proto/extra.go:41.68,41.69 0 0
go.mau.fi/whatsmeow/proto/extra.go:42.68,42.69 0 0
go.mau.fi/whatsmeow/proto/extra.go:43.68,43.69 0 0
go.mau.fi/whatsmeow/proto/instamadilloAddMessage/extra.go:3.54,3.55 0 0
go.mau.fi/whatsmeow/proto/instamadilloDeleteMessage/extra.go:3.57,3.58 0 0
go.mau.fi/whatsmeow/proto/instamadilloSupplementMessage/extra.go:3.61,3.62 0 0
go.mau.fi/whatsmeow/proto/waArmadilloApplication/extra.go:3.46,3.47 0 0
go.mau.fi/whatsmeow/proto/waMultiDevice/extra.go:3.48,3.49 0 0
go.mau.fi/whatsmeow/proto/extra.go:41.68,41.69 0 0
go.mau.fi/whatsmeow/proto/extra.go:42.68,42.69 0 0
go.mau.fi/whatsmeow/proto/extra.go:43.68,43.69 0 0
go.mau.fi/whatsmeow/proto/instamadilloAddMessage/extra.go:3.54,3.55 0 0
go.mau.fi/whatsmeow/proto/instamadilloDeleteMessage/extra.go:3.57,3.58 0 0
go.mau.fi/whatsmeow/proto/instamadilloSupplementMessage/extra.go:3.61,3.62 0 0
go.mau.fi/whatsmeow/proto/waArmadilloApplication/extra.go:3.46,3.47 0 0
go.mau.fi/whatsmeow/proto/waMultiDevice/extra.go:3.48,3.49 0 0
go.mau.fi/whatsmeow/proto/extra.go:41.68,41.69 0 0
go.mau.fi/whatsmeow/proto/extra.go:42.68,42.69 0 0
go.mau.fi/whatsmeow/proto/extra.go:43.68,43.69 0 0
go.mau.fi/whatsmeow/proto/instamadilloAddMessage/extra.go:3.54,3.55 0 0
go.mau.fi/whatsmeow/proto/instamadilloDeleteMessage/extra.go:3.57,3.58 0 0
go.mau.fi/whatsmeow/proto/instamadilloSupplementMessage/extra.go:3.61,3.62 0 0
go.mau.fi/whatsmeow/proto/waArmadilloApplication/extra.go:3.46,3.47 0 0
go.mau.fi/whatsmeow/proto/waMultiDevice/extra.go:3.48,3.49 0 0
`
  const zeroFiles = ["backend/third_party/whatsmeow-node/proto/extra.go", "backend/third_party/whatsmeow-node/proto/instamadilloAddMessage/extra.go", "backend/third_party/whatsmeow-node/proto/instamadilloDeleteMessage/extra.go", "backend/third_party/whatsmeow-node/proto/instamadilloSupplementMessage/extra.go", "backend/third_party/whatsmeow-node/proto/waArmadilloApplication/extra.go", "backend/third_party/whatsmeow-node/proto/waMultiDevice/extra.go"]
  const baseProfile = 'mode: atomic\nexample.test/backend/query.go:3.1,3.10 1 1\nexample.test/backend/query.go:4.1,4.10 1 0\n'
  const combined = baseProfile + zeroProfile.slice('mode: atomic\n'.length)
  describe('actual P616 zero-statement source mappings', () => {
    test.each(zeroFiles)('retains original profile presence with no executable or covered lines: %s', file => {
      const coverage = parseGoCoverageProfile(combined, 'example.test/backend', modules)
      expect(coverage.has(file)).toBe(true)
      expect([...coverage.get(file)!.executable]).toEqual([])
      expect([...coverage.get(file)!.covered]).toEqual([])
      const result = evaluateGoChangedCoverage(new Map([[file, new Set([3, 41, 42, 43])]]), coverage, 90)
      expect(result.failures).toEqual([])
      expect(result.files).toEqual([{ file, covered: 0, total: 0, missing: [] }])
      expect(result.pct).toBeNull()
    })

    test('accepts the exact six zero-only profile sources without claiming executable coverage', () => {
      const coverage = parseGoCoverageProfile(zeroProfile, 'example.test/backend', modules)
      expect([...coverage.keys()].sort()).toEqual([...zeroFiles].sort())
      expect([...coverage.values()].every(file => file.executable.size === 0 && file.covered.size === 0)).toBe(true)
    })

    test('still rejects a genuinely absent file and retains the existing 90 percent failure', () => {
      const changed = new Map([
        ...zeroFiles.map(file => [file, new Set([3, 41])] as const),
        ['backend/query.go', new Set([3, 4])],
        ['backend/absent.go', new Set([1])],
      ])
      const result = evaluateGoChangedCoverage(changed, parseGoCoverageProfile(combined, 'example.test/backend', modules), 90)
      expect(result.failures).toEqual([
        'backend/absent.go: missing Go coverage source mapping',
        'Go changed-lines: coverage 50.00% is below the existing 90% minimum',
      ])
      expect({ covered: result.covered, total: result.total, pct: result.pct, minimumPercent: result.minimumPercent })
        .toEqual({ covered: 1, total: 2, pct: 50, minimumPercent: 90 })
    })

    test('zero-only duplicates never alter real block counters or executable line totals', () => {
      const original = parseGoCoverageProfile(baseProfile, 'example.test/backend', modules)
      const revised = parseGoCoverageProfile(combined + zeroProfile.slice('mode: atomic\n'.length), 'example.test/backend', modules)
      expect(revised.get('backend/query.go')).toEqual(original.get('backend/query.go'))
      expect([...revised.values()].reduce((n, file) => n + file.executable.size, 0)).toBe(2)
      expect([...revised.values()].reduce((n, file) => n + file.covered.size, 0)).toBe(1)
    })

    test('zero statements never grant an unknown module or escaping path an exclusion', () => {
      expect(() => parseGoCoverageProfile('mode: atomic\nforeign.test/marker.go:1.1,1.2 0 0\n', 'example.test/backend', modules)).toThrow('outside')
      expect(() => parseGoCoverageProfile('mode: atomic\nexample.test/backend/../marker.go:1.1,1.2 0 0\n', 'example.test/backend', modules)).toThrow('Invalid coverage source path')
      expect(() => parseGoCoverageProfile('mode: atomic\n', 'example.test/backend', modules)).toThrow('no executable source blocks')
    })
  })
}


type QualityCacheFixtureControls = {
  command?: (command: string[]) => void
  unit?: (path: string, env: Record<string, string>, artifact: string) => void | Promise<void>
  clean?: (env: Record<string, string>, artifact: string) => Promise<{ exitCode: number, stdout: string, stderr: string }> | { exitCode: number, stdout: string, stderr: string }
  expectedFailure?: string
}

async function runLosslessQualityFixture(mode: 'regular' | 'gzip-fifo', fault?: 'no-producer' | 'unit-failure', controls: QualityCacheFixtureControls = {}) {
  const root = realpathSync(tempRoot())
  const commands: string[][] = []
  const homes: string[] = []
  try {
    mkdirSync(join(root, 'backend'))
    mkdirSync(join(root, 'scripts/quality-gate'), { recursive: true })
    writeFileSync(join(root, 'backend/go.mod'), `module ${modulePath}\n\ngo 1.25.7\n`)
    writeFileSync(join(root, 'backend/query.go'), 'package fixture\nfunc query() int { return 1 }\n')
    writeFileSync(join(root, 'backend/query_test.go'), 'package fixture\nimport "testing"\nfunc TestQuery(t *testing.T) { if query()!=1 { t.Fatal("query") } }\n')
    writeFileSync(join(root, 'scripts/quality-gate/coverage-thresholds.json'), '{"changedLines":{"minimumPercent":90}}')
    const artifact = join(root, 'artifacts')
    const report = await runGoChecks({ rootDir: root, artifactDir: artifact,
      sourceEnvironment: { PATH: process.env.PATH, CC_HAHA_GO_PROFILE_STORAGE: mode },
      async execute(command, options) {
        commands.push(command)
        controls.command?.(command)
        homes.push(options.env.HOME)
        expect(options.env.CC_HAHA_GO_PROFILE_STORAGE).toBeUndefined()
        let stdout = ''
        if (command[1] === 'env') {
          const [GOOS, GOARCH] = goNativeTarget().split('/')
          stdout = JSON.stringify({ GOOS, GOARCH, GOVERSION: 'go1.25.7' })
        } else if (command[1] === 'mod' && command[2] === 'edit') stdout = JSON.stringify({ Require: [] })
        else if (command.includes('-m')) stdout = JSON.stringify({ Path: modulePath, Dir: join(root, 'backend') })
        else if (command[1] === 'list' && command.includes('-e')) stdout = JSON.stringify({ ImportPath: modulePath, Dir: join(root, 'backend'), GoFiles: ['query.go'], TestGoFiles: ['query_test.go'] })
        else if (command[1] === 'list') stdout = modulePath
        else if (command[1] === 'test') {
          const path = command.find(value => value.startsWith('-coverprofile='))?.slice('-coverprofile='.length)
          if (path && fault !== 'no-producer') writeFileSync(path, `mode: atomic\n${modulePath}/query.go:2.1,2.30 1 1\n`)
          if (path) await controls.unit?.(path, options.env, artifact)
          if (path && fault === 'unit-failure') return { exitCode: 7, stdout: 'original owned unit failure', stderr: 'original nonzero producer' }
          stdout = passingEvents
        } else if (command[1] === 'clean' && controls.clean) {
          return controls.clean(options.env, artifact)
        } else if (command.includes('merge-base')) stdout = 'a'.repeat(40)
        else if (command.includes('--cached')) stdout = 'backend/query.go\0backend/query_test.go\0scripts/quality-gate/coverage-thresholds.json\0'
        return { exitCode: 0, stdout, stderr: '' }
      },
    })
    if (controls.expectedFailure) {
      expect(report.status).toBe('failed')
      expect(report.failures.join('\n')).toContain(controls.expectedFailure)
      expect(report.race).toBeUndefined()
      expect(report.childCoverage).toBeUndefined()
      expect(report.changedLines).toBeUndefined()
    } else if (!fault) {
      expect(report.status).toBe('passed')
      expect(report.failures).toEqual([])
      expect(report.changedLines?.minimumPercent).toBe(90)
      expect(report.coverage?.total).toBe(1)
      expect(report.coverage?.covered).toBe(1)
      if (mode === 'gzip-fifo') {
        expect(existsSync(join(artifact, 'coverage.out'))).toBe(false)
        expect(report.coverageStorage).toMatchObject({ mode: 'gzip-fifo', ownedStreams: 0, ownedAnchors: 0 })
        expect(report.coverageStorage!.profiles.every(row => row.status === 'complete')).toBe(true)
        expect(readFileSync(join(artifact, 'report.md'), 'utf8')).toContain('verified gzip with explicit restore mapping')
      } else {
        expect(report.coverageStorage).toBeUndefined()
        expect(existsSync(join(artifact, 'coverage-storage'))).toBe(false)
        expect(readFileSync(join(artifact, 'coverage.out'), 'utf8')).toContain('mode: atomic')
      }
    } else {
      expect(report.status).toBe('failed')
      expect(report.race).toBeUndefined()
      expect(report.childCoverage).toBeUndefined()
      expect(report.changedLines).toBeUndefined()
      expect(report.failures.join('\n')).toContain(fault === 'no-producer' ? 'did not emit a complete atomic profile' : 'original nonzero producer')
      expect(commands.some(command => command[1] === 'clean')).toBe(false)
      expect(commands.some(command => command.includes('-race'))).toBe(false)
      expect(report.coverageStorage).toMatchObject({ ownedStreams: 0, ownedAnchors: 0, profiles: [{ status: 'failed' }] })
    }
    expect(homes.length).toBeGreaterThan(0)
    expect(homes.every(path => !existsSync(path))).toBe(true)
    return { commands: commands.map(command => command.map(value => value.replaceAll(root, '<root>'))), coverage: report.coverage, unit: report.unit, race: report.race }
  } finally { rmSync(root, { recursive: true, force: true }) }
}

test.skipIf(process.platform === 'win32')('keeps the original quality Go commands and evidence through opt-in lossless storage', async () => {
  const regular = await runLosslessQualityFixture('regular')
  const stored = await runLosslessQualityFixture('gzip-fifo')
  expect(stored).toEqual(regular)
  const tests = stored.commands.filter(command => command[0] === 'go' && command[1] === 'test')
  expect(tests).toHaveLength(2)
  expect(tests[0]).toEqual(['go', 'test', '-p=4', '-json', '-count=1', '-timeout=30m', '-covermode=atomic', '-outputdir=<root>/artifacts/child-coverage', '-coverpkg=./...', '-coverprofile=<root>/artifacts/coverage.out', './...'])
  expect(tests[1]).toEqual(['go', 'test', '-p=4', '-race', '-json', '-count=1', '-timeout=30m', './...'])
}, 10000)

for (const fault of ['no-producer', 'unit-failure'] as const) {
  test.skipIf(process.platform === 'win32')(`does not turn incomplete quality storage into a passing lane: ${fault}`, async () => {
    await runLosslessQualityFixture('gzip-fifo', fault)
  }, 10000)
}


describe('private Go cache retirement between unit and race', () => {
  test.skipIf(process.platform === 'win32').each(['regular', 'gzip-fifo'] as const)('awaits the actual unit producer before retiring its cache: %s', async mode => {
    const unitEntered = Promise.withResolvers<void>()
    const releaseUnit = Promise.withResolvers<void>()
    const commands: string[][] = []
    const running = runLosslessQualityFixture(mode, undefined, {
      command(command) { commands.push(command) },
      async unit() {
        unitEntered.resolve()
        await releaseUnit.promise
      },
    })
    try {
      await unitEntered.promise
      await nextTurn()
      expect(commands.some(command => command[1] === 'clean' || command.includes('-race'))).toBe(false)
    } finally {
      releaseUnit.resolve()
      await running
    }
    const stages = commands.filter(command => command[1] === 'test' || command[1] === 'clean')
    expect(stages.map(command => command[1] === 'clean' ? 'clean' : command.includes('-race') ? 'race' : 'unit')).toEqual(['unit', 'clean', 'race'])
    expect(stages[1]).toEqual(['go', 'clean', '-cache'])
  }, 10000)

  test.skipIf(process.platform === 'win32')('awaits real FIFO EOF after unit returns before clean or race', async () => {
    const producerReturned = Promise.withResolvers<void>()
    const commands: string[][] = []
    let writer: number | undefined
    const running = runLosslessQualityFixture('gzip-fifo', undefined, {
      command(command) { commands.push(command) },
      unit(path) {
        writer = openSync(path, constants.O_WRONLY | constants.O_NONBLOCK | constants.O_NOFOLLOW)
        producerReturned.resolve()
      },
    })
    try {
      await producerReturned.promise
      await nextTurn()
      expect(commands.some(command => command[1] === 'clean' || command.includes('-race'))).toBe(false)
    } finally {
      if (writer !== undefined) closeSync(writer)
      await running
    }
    expect(commands.filter(command => command[1] === 'test' || command[1] === 'clean').map(command => command[1] === 'clean' ? 'clean' : command.includes('-race') ? 'race' : 'unit')).toEqual(['unit', 'clean', 'race'])
  }, 10000)

  test('preserves a clean failure cause and never starts race or merge', async () => {
    let cleanCalls = 0
    await runLosslessQualityFixture('regular', undefined, {
      clean() {
        cleanCalls++
        return { exitCode: 23, stdout: '', stderr: 'owned cache retirement failed' }
      },
      expectedFailure: 'go clean -cache exited 23: owned cache retirement failed',
    })
    expect(cleanCalls).toBe(1)
  })

  test.skipIf(process.platform === 'win32').each(['different-root', 'symlink', 'replaced-inode', 'replaced-during-clean'] as const)('rejects a changed private cache without deleting a sibling: %s', async scenario => {
    const sibling = realpathSync(tempRoot())
    const marker = join(sibling, 'keep')
    writeFileSync(marker, 'protected sibling')
    let cleanCalls = 0
    try {
      await runLosslessQualityFixture('regular', undefined, {
        unit(_path, env) {
          if (scenario === 'different-root') env.GOCACHE = sibling
          else if (scenario !== 'replaced-during-clean') {
            renameSync(env.GOCACHE, env.GOCACHE + '.original')
            if (scenario === 'symlink') symlinkSync(sibling, env.GOCACHE, 'dir')
            else mkdirSync(env.GOCACHE)
          }
        },
        clean(env) {
          cleanCalls++
          renameSync(env.GOCACHE, env.GOCACHE + '.original')
          mkdirSync(env.GOCACHE)
          return { exitCode: 0, stdout: '', stderr: '' }
        },
        expectedFailure: scenario === 'different-root' || scenario === 'symlink'
          ? 'Go build cache retirement requires the captured private sandbox cache'
          : scenario === 'replaced-inode' ? 'Private Go build cache identity changed before retirement' : 'Private Go build cache identity changed during retirement',
      })
      expect(cleanCalls).toBe(scenario === 'replaced-during-clean' ? 1 : 0)
      expect(readFileSync(marker, 'utf8')).toBe('protected sibling')
    } finally { rmSync(sibling, { recursive: true, force: true }) }
  })

  test('rejects a protected module cache placed inside this private Go build cache', async () => {
    const commands: string[][] = []
    const payload = 'protected module bytes'
    let cleanCalls = 0
    let sentinelWasReadBack = false
    await runLosslessQualityFixture('regular', undefined, {
      command(command) { commands.push(command) },
      unit(_path, env) {
        const moduleCache = join(realpathSync(env.GOCACHE), '00', 'protected-modules')
        mkdirSync(moduleCache, { recursive: true })
        const sentinel = join(moduleCache, 'keep')
        writeFileSync(sentinel, payload)
        expect(lstatSync(moduleCache).isSymbolicLink()).toBe(false)
        expect(readFileSync(sentinel, 'utf8')).toBe(payload)
        sentinelWasReadBack = true
        env.GOMODCACHE = moduleCache
      },
      clean() {
        cleanCalls++
        return { exitCode: 0, stdout: '', stderr: '' }
      },
      expectedFailure: 'Go build cache retirement would remove protected inputs or evidence:',
    })
    expect(sentinelWasReadBack).toBe(true)
    expect(cleanCalls).toBe(0)
    expect(commands.some(command => command[1] === 'clean' || command.includes('-race'))).toBe(false)
    // The rejected clean never runs. The original finally still removes its entire disposable HOME.
  })

  test('real Go clean only retires private cache entries and preserves root, modules and evidence', async () => {
    let actuallyCleaned = false
    const identity = (path: string) => {
      const info = lstatSync(path)
      return { dev: info.dev, ino: info.ino, uid: info.uid, gid: info.gid, mode: info.mode & 0o777 }
    }
    await runLosslessQualityFixture('regular', undefined, {
      unit(path, env, artifact) {
        mkdirSync(join(env.GOCACHE, '00'))
        writeFileSync(join(env.GOCACHE, '00/item'), 'regenerable compiled bytes')
        writeFileSync(join(env.GOMODCACHE, 'private-module-sentinel'), 'pinned modules')
        const kept = join(artifact, 'preserved')
        mkdirSync(kept)
        for (const role of ['seed', 'child-bin', 'raw-counter']) writeFileSync(join(kept, role), role)
        expect(readFileSync(path, 'utf8')).toContain('mode: atomic')
      },
      async clean(env, artifact) {
        const cacheRoot = identity(env.GOCACHE)
        const kept = join(artifact, 'preserved')
        const paths = [join(env.GOMODCACHE, 'private-module-sentinel'), ...['seed', 'child-bin', 'raw-counter'].map(role => join(kept, role)), join(artifact, 'coverage.out'), join(artifact, 'child-coverage/manifest.json')]
        const preserved = (path: string) => {
          const info = lstatSync(path)
          return { path, ...identity(path), size: info.size, nlink: info.nlink, mtimeMs: info.mtimeMs, bytes: readFileSync(path) }
        }
        const before = paths.map(preserved)
        const child = Bun.spawn(['go', 'clean', '-cache'], { cwd: join(artifact, '..', 'backend'), env, stdin: 'ignore', stdout: 'pipe', stderr: 'pipe' })
        const [exitCode, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
        expect(exitCode).toBe(0)
        expect(existsSync(join(env.GOCACHE, '00/item'))).toBe(false)
        expect(identity(env.GOCACHE)).toEqual(cacheRoot)
        expect(paths.map(preserved)).toEqual(before)
        actuallyCleaned = true
        return { exitCode, stdout, stderr }
      },
    })
    expect(actuallyCleaned).toBe(true)
  }, 30000)
})
