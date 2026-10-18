import { describe, expect, test } from 'bun:test'
import { chmodSync, cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import {
  createHermeticGoEnvironment, defaultGoDependencySeed, goDependencyLock,
  hydrateGoDependencies, parseGoJsonStream, prepareGoDependencies,
} from './go-dependencies'

const dependency = { Path: 'example.test/dependency', Version: 'v1.2.3' }
const sum = `h1:${'A'.repeat(43)}=`
const goModSum = `h1:${'B'.repeat(43)}=`

function fixtureRoot(dependencies = true) {
  const root = mkdtempSync(join(tmpdir(), 'go-dependency-regression-'))
  mkdirSync(join(root, 'backend'))
  writeFileSync(join(root, 'backend/go.mod'), `module example.test/backend\n\ngo 1.25.7\n${dependencies ? `\nrequire ${dependency.Path} ${dependency.Version}\n` : ''}`)
  if (dependencies) writeFileSync(join(root, 'backend/go.sum'), `${dependency.Path} ${dependency.Version} ${sum}\n${dependency.Path} ${dependency.Version}/go.mod ${goModSum}\n`)
  return root
}

function fakeGo(root: string, scenario = 'success') {
  const commands: string[][] = []
  const homes: string[] = []
  const environments: Record<string, string>[] = []
  const execute = async (command: string[], options: { cwd: string; env: Record<string, string> }) => {
    // Dependency preparation invokes Go before the build/test command. Its
    // first process must already observe a physical private opt-out.
    expect(options.env.GOTELEMETRY).toBe('off')
    expect(readFileSync(join(options.env.TEST_TELEMETRY_DIR, 'mode'), 'utf8')).toBe('off\n')
    commands.push(command)
    homes.push(options.env.HOME)
    environments.push(options.env)
    let stdout = ''
    if (command[1] === 'mod' && command[2] === 'edit') {
      const required = readFileSync(join(root, 'backend/go.mod'), 'utf8').includes('require') ? [dependency] : []
      stdout = JSON.stringify({ Require: required, Replace: scenario === 'replace' ? [{ Old: dependency, New: { Path: '../private' } }] : [] })
    } else if (command[1] === 'mod' && command[2] === 'download') {
      const directory = join(options.env.GOMODCACHE, `${dependency.Path}@${dependency.Version}`)
      const zip = join(options.env.GOMODCACHE, 'cache/module.zip')
      const mod = join(options.env.GOMODCACHE, 'cache/module.mod')
      mkdirSync(directory, { recursive: true })
      mkdirSync(join(options.env.GOMODCACHE, 'cache'), { recursive: true })
      writeFileSync(join(directory, 'code.go'), 'package dependency\n')
      writeFileSync(zip, 'fixture zip')
      writeFileSync(mod, `module ${dependency.Path}\n`)
      chmodSync(directory, 0o500)
      if (scenario === 'drift') writeFileSync(join(options.cwd, 'go.sum'), 'changed')
      const entry = { ...dependency, Dir: directory, Zip: zip, GoMod: mod, Sum: sum, GoModSum: scenario === 'checksum' ? sum : goModSum }
      stdout = scenario === 'empty' ? '' : JSON.stringify(scenario === 'incomplete' ? { ...entry, Dir: undefined } : entry)
    } else if (command[1] === 'mod' && command[2] === 'verify') {
      if (scenario === 'verify') return { exitCode: 1, stdout: '', stderr: 'modified dependency' }
      stdout = 'all modules verified.\n'
    } else if (command[1] === 'version') stdout = 'go version go1.25.7 fixture/fixture\n'
    return { exitCode: 0, stdout, stderr: '' }
  }
  return { commands, homes, environments, execute }
}

describe('Go dependency preparation and offline hydration', () => {
  test('parses Go JSON object streams with nested and escaped string braces, rejecting truncation', () => {
    expect(parseGoJsonStream('{"value":"}\\\"{","nested":{"a":1}}\n {"second":true}'))
      .toEqual([{ value: '}"{', nested: { a: 1 } }, { second: true }])
    expect(parseGoJsonStream('')).toEqual([])
    expect(() => parseGoJsonStream('{"x":')).toThrow('Incomplete')
    expect(() => parseGoJsonStream('not json')).toThrow('JSON object stream')
  })

  test('separates explicit network preparation from offline execution and rejects authenticated proxies or non-temporary homes', () => {
    const home = mkdtempSync(join(tmpdir(), 'go-dependency-env-'))
    try {
      const source = { HTTPS_PROXY: 'http://127.0.0.1:1234', ANTHROPIC_API_KEY: 'private', GOPRIVATE: 'private.test' }
      const preparation = createHermeticGoEnvironment(home, source, true)
      expect(preparation).toMatchObject({ GOPROXY: 'https://proxy.golang.org', GOSUMDB: 'sum.golang.org', HTTPS_PROXY: source.HTTPS_PROXY, GOAUTH: 'off', GOVCS: '*:off', GOTOOLCHAIN: 'local' })
      const offline = createHermeticGoEnvironment(home, source)
      expect(offline).toMatchObject({ GOPROXY: 'off', GOSUMDB: 'off', GOFLAGS: '-mod=readonly', GOWORK: 'off' })
      expect(offline.HTTPS_PROXY).toBeUndefined()
      expect(offline.ANTHROPIC_API_KEY).toBeUndefined()
      expect(offline.GOPRIVATE).toBe('')
      expect(() => createHermeticGoEnvironment(home, { HTTPS_PROXY: 'https://user:password@proxy.invalid' }, true)).toThrow('authenticated')
      expect(() => createHermeticGoEnvironment('/')).toThrow('temporary HOME')
    } finally { rmSync(home, { recursive: true, force: true }) }
  })

  test('forwards only explicit physical fixture opt-ins and a real private Poppler directory', () => {
    const home = mkdtempSync(join(tmpdir(), 'go-physical-env-'))
    const root = fixtureRoot(false)
    const bin = join(root, 'artifacts/private-poppler/bin')
    mkdirSync(bin, { recursive: true })
    try {
      const physical = createHermeticGoEnvironment(home, { CC_HAHA_TEST_POPPLER_BIN: bin, CC_HAHA_SANDBOX_PHYSICAL_BROKER: '1', CC_HAHA_OTHER_PRIVATE: 'must-not-inherit' }, false, root)
      expect(physical.CC_HAHA_TEST_POPPLER_BIN).toBe(realpathSync(bin))
      expect(physical.CC_HAHA_SANDBOX_PHYSICAL_BROKER).toBe('1')
      expect(physical.CC_HAHA_OTHER_PRIVATE).toBeUndefined()
      expect(createHermeticGoEnvironment(home, {}, false, root).CC_HAHA_SANDBOX_PHYSICAL_BROKER).toBeUndefined()
      expect(() => createHermeticGoEnvironment(home, { CC_HAHA_SANDBOX_PHYSICAL_BROKER: 'true' }, false, root)).toThrow('explicit 1')
      expect(() => createHermeticGoEnvironment(home, { CC_HAHA_TEST_POPPLER_BIN: 'relative' }, false, root)).toThrow('absolute')
      expect(() => createHermeticGoEnvironment(home, { CC_HAHA_TEST_POPPLER_BIN: join(bin, 'missing') }, false, root)).toThrow('real private directory')
      expect(() => createHermeticGoEnvironment(home, { CC_HAHA_TEST_POPPLER_BIN: '/usr/bin' }, false, root)).toThrow('private')
      const link = join(root, 'artifacts/poppler-link')
      symlinkSync(bin, link)
      expect(() => createHermeticGoEnvironment(home, { CC_HAHA_TEST_POPPLER_BIN: link }, false, root)).toThrow('symlink')
    } finally { rmSync(home, { recursive: true, force: true }); rmSync(root, { recursive: true, force: true }) }
  })

  test.skipIf(!process.env.CC_HAHA_MCPB_SOURCE_SDK)('loads the actual pinned private MCPB SDK through the hermetic child environment', async () => {
    const home = mkdtempSync(join(tmpdir(), 'go-mcpb-env-'))
    const root = fixtureRoot(false)
    const sdk = join(root, 'artifacts/private-mcpb')
    cpSync(process.env.CC_HAHA_MCPB_SOURCE_SDK!, sdk, { recursive: true })
    const code = `
      import {readFileSync} from 'node:fs'
      import {join} from 'node:path'
      import {pathToFileURL} from 'node:url'
      const sdk = process.env.CC_HAHA_MCPB_SOURCE_SDK
      const {McpbManifestSchema} = await import(pathToFileURL(join(sdk, 'dist/schemas/latest.js')).href)
      const {getMcpConfigForManifest} = await import(pathToFileURL(join(sdk, 'dist/shared/config.js')).href)
      const manifest = McpbManifestSchema.parse({manifest_version:'0.3',name:'private-peer',version:'1.0',description:'private',author:{name:'private'},server:{type:'node',entry_point:'server.js',mcp_config:{command:'node',args:['\u0024{__dirname}/server.js','\u0024{user_config.flags}'],env:{FEATURE:'\u0024{user_config.enabled}'}}},user_config:{flags:{type:'string',title:'flags',description:'flags',multiple:true},enabled:{type:'boolean',title:'enabled',description:'enabled'}}})
      const config = await getMcpConfigForManifest({manifest,extensionPath:join(process.env.HOME,'extension'),systemDirs:{},pathSeparator:'/',userConfig:{flags:['alpha','beta'],enabled:true}})
      process.stdout.write(JSON.stringify({sdkVersion:JSON.parse(readFileSync(join(sdk,'package.json'),'utf8')).version,zodVersion:JSON.parse(readFileSync(join(sdk,'node_modules/zod/package.json'),'utf8')).version,config,rejectsUnknown:!McpbManifestSchema.safeParse({...manifest,unknown:true}).success,secret:process.env.ANTHROPIC_API_KEY??null}))
    `
    try {
      const env = createHermeticGoEnvironment(home, { CC_HAHA_MCPB_SOURCE_SDK: sdk, ANTHROPIC_API_KEY: 'must-not-inherit' }, false, root)
      const child = Bun.spawn([process.execPath, '--no-env-file', '--eval', code], { cwd: home, env, stdin: 'ignore', stdout: 'pipe', stderr: 'pipe' })
      const [exit, output, error] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      expect({ exit, error }).toEqual({ exit: 0, error: '' })
      expect(JSON.parse(output)).toEqual({ sdkVersion: '1.2.0', zodVersion: '3.25.67', config: { command: 'node', args: [join(home, 'extension/server.js'), 'alpha', 'beta'], env: { FEATURE: 'true' } }, rejectsUnknown: true, secret: null })
    } finally { rmSync(home, { recursive: true, force: true }); rmSync(root, { recursive: true, force: true }) }
  })

  test.each(['relative', 'missing', 'outside-private-root', 'directory-link', 'ancestor-link', 'manifest-link', 'nested-zod-link', 'wrong-sdk-name', 'wrong-sdk-version', 'wrong-zod-version', 'missing-reference'])('rejects invalid MCPB Source preparation before invoking a child: %s', scenario => {
    const home = mkdtempSync(join(tmpdir(), 'go-mcpb-boundary-'))
    const root = fixtureRoot(false)
    const sdk = join(root, 'artifacts/reference-sdk')
    const manifest = join(sdk, 'package.json')
    const zod = join(sdk, 'node_modules/zod/package.json')
    const latest = join(sdk, 'dist/schemas/latest.js')
    const config = join(sdk, 'dist/shared/config.js')
    for (const path of ['node_modules/zod', 'dist/schemas', 'dist/shared']) mkdirSync(join(sdk, path), { recursive: true })
    writeFileSync(manifest, JSON.stringify({ name: '@anthropic-ai/mcpb', version: '1.2.0' }))
    writeFileSync(zod, JSON.stringify({ name: 'zod', version: '3.25.67' }))
    writeFileSync(latest, 'export {}')
    writeFileSync(config, 'export {}')
    let path = sdk
    try {
      if (scenario === 'relative') path = 'relative'
      if (scenario === 'missing') path = join(sdk, 'missing')
      if (scenario === 'outside-private-root') path = '/usr/bin'
      if (scenario === 'directory-link') { path = join(root, 'artifacts/sdk-link'); symlinkSync(sdk, path, 'dir') }
      if (scenario === 'ancestor-link') { const alias = join(root, 'artifact-alias'); symlinkSync(join(root, 'artifacts'), alias, 'dir'); path = join(alias, 'reference-sdk') }
      if (scenario === 'manifest-link') { const original = join(sdk, 'package.original.json'); writeFileSync(original, readFileSync(manifest)); rmSync(manifest); symlinkSync(original, manifest) }
      if (scenario === 'nested-zod-link') { const original = join(sdk, 'zod.original.json'); writeFileSync(original, readFileSync(zod)); rmSync(zod); symlinkSync(original, zod) }
      if (scenario === 'wrong-sdk-name') writeFileSync(manifest, JSON.stringify({ name: 'foreign', version: '1.2.0' }))
      if (scenario === 'wrong-sdk-version') writeFileSync(manifest, JSON.stringify({ name: '@anthropic-ai/mcpb', version: '1.1.0' }))
      if (scenario === 'wrong-zod-version') writeFileSync(zod, JSON.stringify({ name: 'zod', version: '4.0.0' }))
      if (scenario === 'missing-reference') rmSync(config)
      const expected = scenario.includes('link') ? 'symlink' : scenario.startsWith('wrong-') ? 'pinned' : scenario === 'relative' ? 'absolute' : scenario === 'missing-reference' ? 'reference files' : 'private'
      expect(() => createHermeticGoEnvironment(home, { CC_HAHA_MCPB_SOURCE_SDK: path }, false, root)).toThrow(expected)
    } finally { rmSync(home, { recursive: true, force: true }); rmSync(root, { recursive: true, force: true }) }
  })

  test('binds a verified seed to both lock files, hydrates independent private copies, and never writes the seed or original module', async () => {
    const root = fixtureRoot()
    const home = mkdtempSync(join(tmpdir(), 'go-dependency-hydration-'))
    try {
      const go = fakeGo(root)
      const before = goDependencyLock(root)
      const prepared = await prepareGoDependencies({ rootDir: root, execute: go.execute, sourceEnvironment: {} })
      expect(prepared.seed).toBe(defaultGoDependencySeed(root))
      expect(prepared.manifest.modules).toEqual([{ path: dependency.Path, version: dependency.Version, sum, goModSum }])
      expect(prepared.manifest.files.length).toBe(3)
      expect(goDependencyLock(root)).toEqual(before)
      expect(go.homes.every(path => !existsSync(path))).toBe(true)
      const env = createHermeticGoEnvironment(home, {})
      const evidence = await hydrateGoDependencies(root, env, { execute: go.execute })
      expect(evidence).toEqual({ modules: 1, seed: prepared.seed })
      const source = `${dependency.Path}@${dependency.Version}/code.go`
      writeFileSync(join(env.GOMODCACHE, source), 'private scratch')
      expect(readFileSync(join(prepared.seed, 'modules', source), 'utf8')).toBe('package dependency\n')
      expect(go.commands.filter(command => command.includes('download')).length).toBe(1)
      expect(go.environments.at(-1)?.GOPROXY).toBe('off')
    } finally {
      // The artifact copy preserves Go's read-only module-directory permission.
      const directory = join(defaultGoDependencySeed(root), `modules/${dependency.Path}@${dependency.Version}`)
      if (existsSync(directory)) chmodSync(directory, 0o700)
      rmSync(root, { recursive: true, force: true })
      rmSync(home, { recursive: true, force: true })
    }
  })

  test.each(['empty', 'incomplete', 'checksum', 'replace', 'verify', 'drift'])('rejects incomplete or unlocked preparation evidence: %s', async scenario => {
    const root = fixtureRoot()
    try {
      const go = fakeGo(root, scenario)
      const before = goDependencyLock(root)
      await expect(prepareGoDependencies({ rootDir: root, execute: go.execute, sourceEnvironment: {} })).rejects.toThrow()
      expect(goDependencyLock(root)).toEqual(before)
      expect(go.homes.every(home => !existsSync(home))).toBe(true)
      expect(existsSync(defaultGoDependencySeed(root))).toBe(false)
    } finally { rmSync(root, { recursive: true, force: true }) }
  })

  test('imports an explicit disposable seed cache offline without writing the source cache', async () => {
    const root = fixtureRoot()
    try {
      const go = fakeGo(root)
      const original = await prepareGoDependencies({ rootDir: root, execute: go.execute, sourceEnvironment: {} })
      const sourceCache = join(original.seed, 'modules')
      const sourceText = readFileSync(join(sourceCache, `${dependency.Path}@${dependency.Version}/code.go`), 'utf8')
      const imported = await prepareGoDependencies({ rootDir: root, seedDir: join(root, 'artifacts/go-dependencies/imported-seed'), sourceCache, execute: go.execute, sourceEnvironment: {} })
      expect(imported.manifest.modules).toEqual(original.manifest.modules)
      expect(go.environments.slice(-4).every(env => env.GOPROXY === 'off' && env.GOSUMDB === 'off')).toBe(true)
      expect(readFileSync(join(sourceCache, `${dependency.Path}@${dependency.Version}/code.go`), 'utf8')).toBe(sourceText)
      chmodSync(join(sourceCache, `${dependency.Path}@${dependency.Version}`), 0o700)
      chmodSync(join(imported.seed, `modules/${dependency.Path}@${dependency.Version}`), 0o700)
    } finally { rmSync(root, { recursive: true, force: true }) }
  })

  test('rejects a non-private environment before invoking Go, including stdlib-only modules', async () => {
    const root = fixtureRoot(false)
    const home = mkdtempSync(join(tmpdir(), 'go-dependency-private-env-'))
    try {
      const go = fakeGo(root)
      const env = createHermeticGoEnvironment(home, {})
      await expect(hydrateGoDependencies(root, { ...env, HOME: '/' }, { execute: go.execute })).rejects.toThrow('temporary HOME')
      await expect(hydrateGoDependencies(root, { ...env, GOCACHE: root }, { execute: go.execute })).rejects.toThrow('private sandbox GOCACHE')
      await expect(hydrateGoDependencies(root, { ...env, GOAUTH: 'netrc' }, { execute: go.execute })).rejects.toThrow('offline hermetic')
      expect(go.commands).toEqual([])
    } finally { rmSync(root, { recursive: true, force: true }); rmSync(home, { recursive: true, force: true }) }
  })

  test('missing seed fails without downloading; a stdlib-only module still uses the ordinary real checks', async () => {
    const root = fixtureRoot()
    const home = mkdtempSync(join(tmpdir(), 'go-dependency-missing-'))
    try {
      const go = fakeGo(root)
      const env = createHermeticGoEnvironment(home, {})
      await expect(hydrateGoDependencies(root, env, { execute: go.execute })).rejects.toThrow('Missing prepared')
      expect(go.commands.some(command => command.includes('download'))).toBe(false)
      writeFileSync(join(root, 'backend/go.mod'), 'module example.test/backend\n\ngo 1.25.7\n')
      expect(await hydrateGoDependencies(root, env, { execute: go.execute })).toEqual({ modules: 0, seed: null })
    } finally { rmSync(root, { recursive: true, force: true }); rmSync(home, { recursive: true, force: true }) }
  })

  test.each(['cache', 'lock', 'inventory', 'destination-symlink', 'manifest-symlink'])('fails offline hydration integrity and isolation: %s', async scenario => {
    const root = fixtureRoot()
    const home = mkdtempSync(join(tmpdir(), 'go-dependency-negative-'))
    try {
      const go = fakeGo(root)
      const prepared = await prepareGoDependencies({ rootDir: root, execute: go.execute, sourceEnvironment: {} })
      const env = createHermeticGoEnvironment(home, {})
      if (scenario === 'cache') writeFileSync(join(prepared.seed, 'modules/cache/module.mod'), 'tampered')
      if (scenario === 'lock') writeFileSync(join(root, 'backend/go.sum'), 'different lock\n')
      if (scenario === 'inventory') {
        prepared.manifest.modules = []
        writeFileSync(join(prepared.seed, 'manifest.json'), JSON.stringify(prepared.manifest))
      }
      if (scenario === 'destination-symlink') {
        rmSync(env.GOMODCACHE, { recursive: true })
        symlinkSync(join(prepared.seed, 'modules'), env.GOMODCACHE, 'dir')
      }
      if (scenario === 'manifest-symlink') {
        const manifest = join(prepared.seed, 'manifest.json')
        writeFileSync(join(root, 'redirected.json'), readFileSync(manifest))
        rmSync(manifest)
        symlinkSync(join(root, 'redirected.json'), manifest)
      }
      await expect(hydrateGoDependencies(root, env, { seedDir: prepared.seed, execute: go.execute })).rejects.toThrow()
      expect(go.commands.filter(command => command.includes('download')).length).toBe(1)
    } finally {
      const seed = join(root, 'artifacts/go-dependencies')
      if (existsSync(seed)) {
        const makeWritable = (path: string) => {
          chmodSync(path, 0o700)
          for (const entry of require('node:fs').readdirSync(path, { withFileTypes: true })) if (entry.isDirectory()) makeWritable(join(path, entry.name))
        }
        makeWritable(seed)
      }
      rmSync(root, { recursive: true, force: true })
      rmSync(home, { recursive: true, force: true })
    }
  })
})

describe('controlled repository local Go forks', () => {
  test('real Go prepares and hydrates a pinned local fork offline while preserving the repository and private HOME', async () => {
    const root = fixtureRoot(false)
    const home = mkdtempSync(join(tmpdir(), 'go-local-fork-physical-'))
    const sourceCache = join(root, 'empty-source-cache')
    mkdirSync(sourceCache)
    const fork = join(root, 'backend/third_party/libsignal-node')
    mkdirSync(fork, { recursive: true })
    writeFileSync(join(root, 'backend/go.mod'), 'module example.test/backend\n\ngo 1.25.7\n\nrequire go.mau.fi/libsignal v0.2.1\n\nreplace go.mau.fi/libsignal => ./third_party/libsignal-node\n')
    writeFileSync(join(root, 'backend/go.sum'), `go.mau.fi/libsignal v0.2.1 ${sum}\ngo.mau.fi/libsignal v0.2.1/go.mod ${goModSum}\n`)
    writeFileSync(join(fork, 'go.mod'), 'module go.mau.fi/libsignal\n\ngo 1.25.7\n')
    writeFileSync(join(fork, 'LICENSE'), 'Private tiny module license fixture\n')
    writeFileSync(join(fork, 'value.go'), 'package libsignal\nfunc Value() string { return "local-fork-physical-value" }\n')
    writeFileSync(join(root, 'backend/fork_test.go'), 'package backend\nimport ("testing"; signal "go.mau.fi/libsignal")\nfunc TestActualLocalReplacement(t *testing.T) { if signal.Value() != "local-fork-physical-value" { t.Fatal(signal.Value()) } }\n')
    const commands: string[][] = []
    const execute = async (command: string[], options: { cwd: string; env: Record<string, string> }) => {
      commands.push(command)
      expect(options.env.GOPROXY).toBe('off')
      expect(options.env.GOSUMDB).toBe('off')
      expect(options.env.GOENV).toBe('off')
      expect(options.env.ANTHROPIC_API_KEY).toBeUndefined()
      const child = Bun.spawn(command, { ...options, stdin: 'ignore', stdout: 'pipe', stderr: 'pipe' })
      const [exitCode, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      return { exitCode, stdout, stderr }
    }
    try {
      const before = goDependencyLock(root)
      const prepared = await prepareGoDependencies({ rootDir: root, sourceCache, execute, sourceEnvironment: { PATH: process.env.PATH, ANTHROPIC_API_KEY: 'must-not-inherit-private-secret' } })
      expect(prepared.manifest.lock).toEqual(before)
      expect(prepared.manifest.modules).toEqual([])
      const env = createHermeticGoEnvironment(home, { PATH: process.env.PATH })
      expect(await hydrateGoDependencies(root, env, { seedDir: prepared.seed, execute })).toEqual({ modules: 0, seed: prepared.seed })
      const result = await execute(['go', 'test', '-race', '-count=1', './...'], { cwd: join(root, 'backend'), env })
      expect(result.exitCode).toBe(0)
      expect(result.stdout).toContain('ok')
      expect(goDependencyLock(root)).toEqual(before)
      expect(commands.some(command => command[2] === 'download')).toBe(true)
    } finally { rmSync(root, { recursive: true, force: true }); rmSync(home, { recursive: true, force: true }) }
  }, 90_000)
})

function controlledForkFixture() {
  const root = fixtureRoot()
  const fork = join(root, 'backend/third_party/libsignal-node')
  mkdirSync(fork, { recursive: true })
  mkdirSync(join(root, 'empty-cache'))
  writeFileSync(join(root, 'backend/go.mod'), `module example.test/backend\n\ngo 1.25.7\n\nrequire (\n ${dependency.Path} ${dependency.Version}\n go.mau.fi/libsignal v0.2.1\n)\n\nreplace (\n "go.mau.fi/libsignal" v0.2.1 => "./third_party/libsignal-node" // controlled fork\n)\n`)
  writeFileSync(join(root, 'backend/go.sum'), `${dependency.Path} ${dependency.Version} ${sum}\n${dependency.Path} ${dependency.Version}/go.mod ${goModSum}\ngo.mau.fi/libsignal v0.2.1 ${sum}\ngo.mau.fi/libsignal v0.2.1/go.mod ${goModSum}\n`)
  writeFileSync(join(fork, 'go.mod'), `module go.mau.fi/libsignal\n\ngo 1.25.7\n\nrequire ${dependency.Path} v1.0.0\n`)
  writeFileSync(join(fork, 'LICENSE'), 'Private fixture license bytes\n')
  writeFileSync(join(fork, 'value.go'), 'package libsignal\n')
  const go = fakeGo(root)
  const execute = async (command: string[], options: { cwd: string; env: Record<string, string> }) => {
    if (command[1] === 'mod' && command[2] === 'edit') {
      expect(options.env.GOPROXY).toBe('off')
      const child = Bun.spawn(command, { ...options, stdin: 'ignore', stdout: 'pipe', stderr: 'pipe' })
      const [exitCode, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      return { exitCode, stdout, stderr }
    }
    return go.execute(command, options)
  }
  return { root, fork, go, execute }
}

function removeForkFixture(root: string) {
  const writable = (path: string) => {
    if (!existsSync(path) || require('node:fs').lstatSync(path).isSymbolicLink()) return
    if (require('node:fs').lstatSync(path).isDirectory()) {
      chmodSync(path, 0o700)
      for (const entry of require('node:fs').readdirSync(path, { withFileTypes: true })) if (entry.isDirectory()) writable(join(path, entry.name))
    }
  }
  writable(root)
  rmSync(root, { recursive: true, force: true })
}

describe('local fork policy, complete source integrity and dependency inventory', () => {
  test('hashes LICENSE, resources, directory additions and nested go.mod; old seeds keep the exact lock shape', async () => {
    const fixture = controlledForkFixture()
    const home = mkdtempSync(join(tmpdir(), 'go-fork-hydration-'))
    try {
      const before = goDependencyLock(fixture.root)
      expect(before.localForks?.[0].files.map(file => file.path)).toEqual(['go.mod', 'LICENSE', 'value.go'].sort((a, b) => a.localeCompare(b)))
      const prepared = await prepareGoDependencies({ rootDir: fixture.root, sourceCache: join(fixture.root, 'empty-cache'), execute: fixture.execute, sourceEnvironment: { PATH: process.env.PATH } })
      expect(prepared.manifest.modules).toEqual([{ path: dependency.Path, version: dependency.Version, sum, goModSum }])
      const env = createHermeticGoEnvironment(home, { PATH: process.env.PATH })
      expect(await hydrateGoDependencies(fixture.root, env, { execute: fixture.execute })).toEqual({ modules: 1, seed: prepared.seed })
      const beforeCopy = readFileSync(join(prepared.seed, 'modules', `${dependency.Path}@${dependency.Version}/code.go`), 'utf8')
      writeFileSync(join(env.GOMODCACHE, `${dependency.Path}@${dependency.Version}/code.go`), 'sandbox mutation')
      expect(readFileSync(join(prepared.seed, 'modules', `${dependency.Path}@${dependency.Version}/code.go`), 'utf8')).toBe(beforeCopy)
      for (const path of ['LICENSE', 'value.go', 'go.mod']) {
        const original = readFileSync(join(fixture.fork, path))
        writeFileSync(join(fixture.fork, path), Buffer.concat([original, Buffer.from('\n')]))
        expect(goDependencyLock(fixture.root).key).not.toBe(before.key)
        const scratch = mkdtempSync(join(tmpdir(), 'go-fork-tamper-'))
        try {
          const env = createHermeticGoEnvironment(scratch, { PATH: process.env.PATH })
          await expect(hydrateGoDependencies(fixture.root, env, { seedDir: prepared.seed, execute: fixture.execute })).rejects.toThrow('match current')
          expect(require('node:fs').readdirSync(env.GOMODCACHE)).toEqual([])
        } finally { rmSync(scratch, { recursive: true, force: true }) }
        writeFileSync(join(fixture.fork, path), original)
      }
      mkdirSync(join(fixture.fork, 'new-empty-directory'))
      expect(goDependencyLock(fixture.root).key).not.toBe(before.key)
      rmSync(join(fixture.fork, 'new-empty-directory'), { recursive: true })
      const plain = fixtureRoot()
      try { expect(Object.keys(goDependencyLock(plain))).toEqual(['goMod', 'goSum', 'key']) } finally { rmSync(plain, { recursive: true, force: true }) }
    } finally { removeForkFixture(fixture.root); rmSync(home, { recursive: true, force: true }) }
  }, 90_000)

  test.each(['unknown-module', 'wrong-old-version', 'absolute', 'escape', 'path-alias', 'remote-version', 'file-symlink', 'directory-symlink', 'ancestor-symlink', 'nonlocked-fork', 'missing-checksum', 'nested-replace', 'nested-exclude', 'wrong-module-identity', 'nested-unlocked', 'nested-higher-version'])('rejects %s before dependency downloading', async scenario => {
    const fixture = controlledForkFixture()
    try {
      const main = join(fixture.root, 'backend/go.mod')
      let text = readFileSync(main, 'utf8')
      if (scenario === 'unknown-module') text = text.replace('"go.mau.fi/libsignal" v0.2.1 =>', '"example.test/unreviewed" v0.2.1 =>')
      if (scenario === 'wrong-old-version') text = text.replace('v0.2.1 =>', 'v0.2.2 =>')
      if (scenario === 'absolute') text = text.replace('./third_party/libsignal-node', fixture.fork)
      if (scenario === 'escape') text = text.replace('./third_party/libsignal-node', '../../../outside')
      if (scenario === 'path-alias') text = text.replace('./third_party/libsignal-node', './third_party/../third_party/libsignal-node')
      if (scenario === 'remote-version') text = text.replace('"./third_party/libsignal-node"', '"go.mau.fi/libsignal" v0.2.1')
      if (scenario === 'nonlocked-fork') text = text.replace('go.mau.fi/libsignal v0.2.1\n', 'go.mau.fi/libsignal v0.2.0\n')
      writeFileSync(main, text)
      if (scenario === 'missing-checksum') writeFileSync(join(fixture.root, 'backend/go.sum'), `${dependency.Path} ${dependency.Version} ${sum}\n${dependency.Path} ${dependency.Version}/go.mod ${goModSum}\n`)
      if (scenario === 'file-symlink') symlinkSync(join(fixture.root, 'backend/go.sum'), join(fixture.fork, 'file-link'))
      if (scenario === 'directory-symlink') symlinkSync(join(fixture.root, 'empty-cache'), join(fixture.fork, 'directory-link'), 'dir')
      if (scenario === 'ancestor-symlink') {
        const original = join(fixture.root, 'backend/original-forks')
        require('node:fs').renameSync(join(fixture.root, 'backend/third_party'), original)
        symlinkSync(original, join(fixture.root, 'backend/third_party'), 'dir')
      }
      const mod = join(fixture.fork, 'go.mod')
      if (scenario === 'nested-replace') writeFileSync(mod, readFileSync(mod, 'utf8') + `\nreplace ${dependency.Path} => ../../../../elsewhere\n`)
      if (scenario === 'nested-exclude') writeFileSync(mod, readFileSync(mod, 'utf8') + `\nexclude ${dependency.Path} v1.2.3\n`)
      if (scenario === 'wrong-module-identity') writeFileSync(mod, 'module example.test/unreviewed\n\ngo 1.25.7\n')
      if (scenario === 'nested-unlocked') writeFileSync(mod, 'module go.mau.fi/libsignal\n\ngo 1.25.7\n\nrequire example.test/unlocked v1.0.0\n')
      if (scenario === 'nested-higher-version') writeFileSync(mod, `module go.mau.fi/libsignal\n\ngo 1.25.7\n\nrequire ${dependency.Path} v1.3.0\n`)
      const expected = scenario.includes('symlink') ? 'symlink'
        : ['nonlocked-fork', 'missing-checksum'].includes(scenario) ? 'upstream identity'
        : ['nested-replace', 'nested-exclude', 'wrong-module-identity'].includes(scenario) ? 'invalid module identity'
        : ['nested-unlocked', 'nested-higher-version'].includes(scenario) ? 'explicitly locked root dependency'
        : 'explicitly controlled pinned'
      await expect(prepareGoDependencies({ rootDir: fixture.root, sourceCache: join(fixture.root, 'empty-cache'), execute: fixture.execute, sourceEnvironment: { PATH: process.env.PATH } })).rejects.toThrow(expected)
      expect(fixture.go.commands.some(command => command.includes('download'))).toBe(false)
      expect(fixture.go.homes.every(home => !existsSync(home))).toBe(true)
    } finally { removeForkFixture(fixture.root) }
  }, 90_000)

  test('rejects a changed private source snapshot and a hydration race without fetching or mutating source cache', async () => {
    const fixture = controlledForkFixture()
    const home = mkdtempSync(join(tmpdir(), 'go-fork-race-'))
    try {
      const raceCopy = async (command: string[], options: { cwd: string; env: Record<string, string> }) => {
        const result = await fixture.execute(command, options)
        if (command[2] === 'verify' && options.cwd !== join(fixture.root, 'backend')) writeFileSync(join(options.cwd, 'third_party/libsignal-node/value.go'), 'tampered private copy\n')
        return result
      }
      await expect(prepareGoDependencies({ rootDir: fixture.root, sourceCache: join(fixture.root, 'empty-cache'), execute: raceCopy, sourceEnvironment: { PATH: process.env.PATH } })).rejects.toThrow('raced')
      const prepared = await prepareGoDependencies({ rootDir: fixture.root, sourceCache: join(fixture.root, 'empty-cache'), execute: fixture.execute, sourceEnvironment: { PATH: process.env.PATH } })
      const raceRoot = async (command: string[], options: { cwd: string; env: Record<string, string> }) => {
        const result = await fixture.execute(command, options)
        if (command[2] === 'verify') writeFileSync(join(fixture.fork, 'LICENSE'), 'changed before hydrate completion\n')
        return result
      }
      await expect(hydrateGoDependencies(fixture.root, createHermeticGoEnvironment(home, { PATH: process.env.PATH }), { seedDir: prepared.seed, execute: raceRoot })).rejects.toThrow('raced')
      expect(require('node:fs').readdirSync(join(fixture.root, 'empty-cache'))).toEqual([])
    } finally { removeForkFixture(fixture.root); rmSync(home, { recursive: true, force: true }) }
  }, 90_000)
})
