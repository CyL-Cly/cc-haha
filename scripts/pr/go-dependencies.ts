import { createHash } from 'node:crypto'
import { chmodSync, cpSync, existsSync, lstatSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, realpathSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { isAbsolute, join, relative, resolve, sep } from 'node:path'
import { compare as compareVersions, valid as validVersion } from 'semver'
import { createSandboxedTestEnvironment } from './test-environment'

type CommandResult = { exitCode: number; stdout: string; stderr: string }
type CommandRunner = (command: string[], options: { cwd: string; env: Record<string, string> }) => Promise<CommandResult>
type LocalFork = {
  path: string; version: string; directory: string
  treeSha256: string
  directories: string[]
  files: Array<{ path: string; sha256: string }>
}
type Lock = { goMod: string; goSum: string | null; key: string; localForks?: LocalFork[] }
type ModuleReplacement = { Old: { Path: string; Version?: string }; New: { Path: string; Version?: string } }

// These repository paths and upstream versions are reviewed production forks,
// not a caller-provided replacement allowance or an alternate download source.
export const GO_CONTROLLED_LOCAL_FORKS = [
  { path: 'go.mau.fi/libsignal', version: 'v0.2.1', directory: 'backend/third_party/libsignal-node' },
  { path: 'go.mau.fi/whatsmeow', version: 'v0.0.0-20260218135554-9cbe80fb25a4', directory: 'backend/third_party/whatsmeow-node' },
] as const
type DependencyManifest = {
  schemaVersion: 1
  lock: Lock
  toolchain: string
  modules: Array<{ path: string; version: string; sum: string; goModSum: string }>
  files: Array<{ path: string; sha256: string }>
}

export const GO_DEPENDENCY_SEED_ENV = 'CC_HAHA_GO_DEPENDENCY_SEED'
const hash = (value: string | Buffer) => createHash('sha256').update(value).digest('hex')
const temporaryRoots = () => [tmpdir(), ...(existsSync('/tmp') ? ['/tmp'] : [])].map(path => realpathSync(path))

// Lock discovery recognizes only line-oriented go.mod replacement directives.
// Go's own mod edit -json validates the full grammar and exact same inventory
// before any preparation download or hydration copy. Quoted paths are decoded
// without treating comment text or a module name as a replacement declaration.
function replacementsInGoMod(source: string): ModuleReplacement[] {
  const lines: string[][] = []
  let tokens: string[] = []
  for (let i = 0; i < source.length;) {
    const c = source[i]
    if (c === '\n') { lines.push(tokens); tokens = []; i++; continue }
    if (/\s/.test(c)) { i++; continue }
    if (source.startsWith('//', i)) { while (i < source.length && source[i] !== '\n') i++; continue }
    if (c === '"' || c === '`') {
      const start = i++, quote = c
      while (i < source.length && source[i] !== quote) {
        if (quote === '"' && source[i] === '\\') i++
        i++
      }
      if (i === source.length) throw new Error('Unterminated quoted Go module token')
      const raw = source.slice(start, ++i)
      tokens.push(quote === '`' ? raw.slice(1, -1) : JSON.parse(raw))
      continue
    }
    if (c === '(' || c === ')') { tokens.push(c); i++; continue }
    const start = i
    while (i < source.length && !/\s/.test(source[i]) && !'()'.includes(source[i]) && !source.startsWith('//', i)) i++
    tokens.push(source.slice(start, i))
  }
  lines.push(tokens)
  let block = false
  const replacements: ModuleReplacement[] = []
  for (const line of lines) {
    if (!line.length) continue
    if (block && line.length === 1 && line[0] === ')') { block = false; continue }
    if (line[0] === 'replace') {
      if (line.length === 2 && line[1] === '(') { block = true; continue }
      line.shift()
    } else if (!block) continue
    const arrow = line.indexOf('=>')
    if (![1, 2].includes(arrow) || ![arrow + 2, arrow + 3].includes(line.length)) throw new Error('Unsupported Go module replacement declaration')
    replacements.push({ Old: { Path: line[0], ...(arrow === 2 ? { Version: line[1] } : {}) }, New: { Path: line[arrow + 1], ...(line.length === arrow + 3 ? { Version: line[arrow + 2] } : {}) } })
  }
  if (block) throw new Error('Unterminated Go module replacement block')
  return replacements
}

function controlledReplacement(replacement: ModuleReplacement) {
  const spec = GO_CONTROLLED_LOCAL_FORKS.find(spec => spec.path === replacement.Old?.Path)
  if (!spec || replacement.New?.Version || replacement.Old.Version && replacement.Old.Version !== spec.version
    || replacement.New.Path !== `./${spec.directory.slice('backend/'.length)}`) throw new Error('Go module replacement is not an explicitly controlled pinned repository local fork')
  return spec
}

function localForkDirectory(rootDir: string, directory: string) {
  const root = realpathSync(rootDir)
  let current = root
  for (const component of directory.split('/')) {
    if (!component || component === '.' || component === '..') throw new Error('Go local fork path must be repository relative')
    current = join(current, component)
    const info = lstatSync(current)
    if (info.isSymbolicLink() || !info.isDirectory()) throw new Error('Go local fork must stay in real repository directories without symlinks')
  }
  if (!realpathSync(current).startsWith(`${root}${sep}`)) throw new Error('Go local fork escaped its repository root')
  return current
}

function localForks(root: string, replacements: ModuleReplacement[]): LocalFork[] {
  const seen = new Set<string>()
  return replacements.map(replacement => {
    const spec = controlledReplacement(replacement)
    if (seen.has(spec.path)) throw new Error('Duplicate controlled Go module replacement')
    seen.add(spec.path)
    const directory = localForkDirectory(root, spec.directory)
    const files = cacheFiles(directory)
    const directories: string[] = []
    const visit = (current: string) => {
      for (const entry of readdirSync(current, { withFileTypes: true })) if (entry.isDirectory()) {
        const path = join(current, entry.name)
        directories.push(relative(directory, path).split(sep).join('/'))
        visit(path)
      }
    }
    visit(directory)
    directories.sort((a, b) => a.localeCompare(b))
    if (!files.some(file => file.path === 'go.mod')) throw new Error('Controlled Go local fork has no module file')
    return { ...spec, treeSha256: hash(JSON.stringify({ directories, files })), directories, files }
  }).sort((a, b) => a.path.localeCompare(b.path))
}

export function goDependencyLock(rootDir: string): Lock {
  const bytes = readFileSync(join(rootDir, 'backend/go.mod'))
  const text = bytes.toString('utf8')
  const goMod = hash(bytes)
  const sumPath = join(rootDir, 'backend/go.sum')
  const goSum = existsSync(sumPath) ? hash(readFileSync(sumPath)) : null
  const forks = localForks(rootDir, replacementsInGoMod(text))
  const material = `${goMod}\n${goSum ?? '<absent>'}`
  return forks.length ? { goMod, goSum, key: hash(`${material}\n${JSON.stringify(forks)}`), localForks: forks } : { goMod, goSum, key: hash(material) }
}

export function defaultGoDependencySeed(rootDir: string) {
  return join(rootDir, 'artifacts/go-dependencies', goDependencyLock(rootDir).key)
}

function disposableCachePath(path: string, rootDir: string) {
  if (!isAbsolute(path)) throw new Error('Go dependency cache path must be absolute')
  let existing = resolve(path)
  while (!existsSync(existing)) {
    const parent = resolve(existing, '..')
    if (parent === existing) throw new Error('Go dependency cache path has no existing ancestor')
    existing = parent
  }
  const canonical = join(realpathSync(existing), relative(existing, resolve(path)))
  const artifactRoot = join(realpathSync(rootDir), 'artifacts/go-dependencies')
  const roots = [artifactRoot, ...temporaryRoots()]
  if (!roots.some(root => canonical.startsWith(`${root}${sep}`))) throw new Error('Go dependency cache must stay under temporary storage or project dependency artifacts')
}

function assertTemporaryHome(home: string) {
  if (!isAbsolute(home) || !existsSync(home) || lstatSync(home).isSymbolicLink() || !lstatSync(home).isDirectory()
    || !temporaryRoots().some(root => realpathSync(home).startsWith(`${root}${sep}`))) throw new Error('Go checks require a real disposable temporary HOME')
}

export function createHermeticGoEnvironment(home: string, source: NodeJS.ProcessEnv = process.env, preparation = false) {
  assertTemporaryHome(home)
  const env = createSandboxedTestEnvironment(home, {
    GOENV: 'off', GOTOOLCHAIN: 'local', GOWORK: 'off', GOFLAGS: '-mod=readonly',
    GOPROXY: preparation ? 'https://proxy.golang.org' : 'off',
    GOSUMDB: preparation ? 'sum.golang.org' : 'off',
    GOAUTH: 'off', GOVCS: '*:off', GOPRIVATE: '', GONOPROXY: 'none', GONOSUMDB: 'none',
    GOPATH: join(home, 'go'), GOMODCACHE: join(home, 'go/pkg/mod'), GOCACHE: join(home, 'go-build'),
    CGO_ENABLED: '1',
  }, source)
  if (preparation) {
    for (const name of ['HTTP_PROXY', 'HTTPS_PROXY', 'http_proxy', 'https_proxy']) {
      const value = source[name]
      if (!value) continue
      const proxy = new URL(value)
      if (!['http:', 'https:'].includes(proxy.protocol) || proxy.username || proxy.password) throw new Error(`Unsupported or authenticated dependency preparation proxy: ${name}`)
      env[name] = value
    }
  }
  mkdirSync(env.GOMODCACHE, { recursive: true })
  mkdirSync(env.GOCACHE, { recursive: true })
  const telemetry = join(home, 'telemetry')
  mkdirSync(telemetry, { recursive: true, mode: 0o700 })
  writeFileSync(join(telemetry, 'mode'), 'off\n', { mode: 0o600 })
  env.GOTELEMETRY = 'off'
  env.TEST_TELEMETRY_DIR = telemetry
  return env
}

export function parseGoJsonStream(output: string): any[] {
  const values: any[] = []
  let start = -1, depth = 0, quoted = false, escaped = false
  for (let index = 0; index < output.length; index++) {
    const character = output[index]
    if (start === -1) {
      if (/\s/.test(character)) continue
      if (character !== '{') throw new Error('Go dependency command did not return a JSON object stream')
      start = index
    }
    if (quoted) {
      if (escaped) escaped = false
      else if (character === '\\') escaped = true
      else if (character === '"') quoted = false
    } else if (character === '"') quoted = true
    else if (character === '{') depth++
    else if (character === '}') {
      depth--
      if (depth === 0) {
        values.push(JSON.parse(output.slice(start, index + 1)))
        start = -1
      }
    }
  }
  if (start !== -1 || quoted || depth !== 0) throw new Error('Incomplete Go dependency JSON stream')
  return values
}

function cacheFiles(directory: string): DependencyManifest['files'] {
  const files: DependencyManifest['files'] = []
  const visit = (current: string) => {
    for (const entry of readdirSync(current, { withFileTypes: true })) {
      const path = join(current, entry.name)
      if (entry.isSymbolicLink()) throw new Error(`Go dependency cache contains a symlink: ${path}`)
      if (entry.isDirectory()) visit(path)
      else if (entry.isFile()) files.push({ path: relative(directory, path).split(sep).join('/'), sha256: hash(readFileSync(path)) })
      else throw new Error(`Go dependency cache contains a non-regular file: ${path}`)
    }
  }
  if (!lstatSync(directory).isDirectory() || lstatSync(directory).isSymbolicLink()) throw new Error('Go dependency cache must be a real directory')
  visit(directory)
  return files.sort((a, b) => a.path.localeCompare(b.path))
}

function writableDirectories(path: string) {
  if (!existsSync(path) || !lstatSync(path).isDirectory() || lstatSync(path).isSymbolicLink()) return
  chmodSync(path, 0o700)
  for (const entry of readdirSync(path, { withFileTypes: true })) {
    if (entry.isDirectory()) writableDirectories(join(path, entry.name))
  }
}

function copyCache(source: string, destination: string) {
  cpSync(source, destination, { recursive: true })
  // Go marks extracted module directories read-only. A disposable copy needs
  // writable directories for normal teardown; this never changes the seed.
  writableDirectories(destination)
}

async function executeCommand(command: string[], options: { cwd: string; env: Record<string, string> }): Promise<CommandResult> {
  const child = Bun.spawn(command, { ...options, stdin: 'ignore', stdout: 'pipe', stderr: 'pipe' })
  const timer = setTimeout(() => child.kill('SIGKILL'), 180_000)
  try {
    const [exitCode, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    return { exitCode, stdout, stderr }
  } finally { clearTimeout(timer) }
}

async function checked(execute: CommandRunner, args: string[], cwd: string, env: Record<string, string>) {
  const result = await execute(['go', ...args], { cwd, env })
  if (result.exitCode !== 0) throw new Error(`Go dependency ${args.join(' ')} failed: ${result.stderr || result.stdout}`)
  return result.stdout
}

function dependencySums(cwd: string) {
  const file = join(cwd, 'go.sum')
  const sums = new Map<string, string>()
  for (const line of (existsSync(file) ? readFileSync(file, 'utf8') : '').trim().split(/\r?\n/).filter(Boolean)) {
    const fields = line.trim().split(/\s+/)
    if (fields.length !== 3 || !/^h1:[A-Za-z0-9+/]{43}=$/.test(fields[2])) throw new Error('Malformed locked Go dependency checksum')
    const key = `${fields[0]} ${fields[1]}`
    if (sums.has(key) && sums.get(key) !== fields[2]) throw new Error(`Conflicting locked Go dependency checksum: ${key}`)
    sums.set(key, fields[2])
  }
  return sums
}

async function validateLocalForks(module: any, lock: Lock, execute: CommandRunner, cwd: string, env: Record<string, string>) {
  const replacements = module.Replace ?? []
  if (!Array.isArray(replacements) || JSON.stringify(localForks(resolve(cwd, '..'), replacements)) !== JSON.stringify(lock.localForks ?? [])) throw new Error('Go replacement inventory does not match its complete local source lock')
  const sums = dependencySums(cwd)
  for (const fork of lock.localForks ?? []) {
    const requirement = module.Require?.find((entry: any) => entry.Path === fork.path)
    if (!requirement || requirement.Version !== fork.version || !sums.has(`${fork.path} ${fork.version}`) || !sums.has(`${fork.path} ${fork.version}/go.mod`)) throw new Error(`Controlled Go fork upstream identity is not explicitly pinned: ${fork.path} ${fork.version}`)
    const parsed = JSON.parse(await checked(execute, ['mod', 'edit', '-json'], join(cwd, fork.directory.slice('backend/'.length)), env))
    if (parsed.Module?.Path !== fork.path || parsed.Replace?.length || parsed.Exclude?.length) throw new Error(`Controlled Go fork has an invalid module identity or nested replacement/exclusion: ${fork.path}`)
    for (const nested of parsed.Require ?? []) {
      const selected = module.Require?.find((entry: any) => entry.Path === nested.Path)
      if (!selected || !validVersion(selected.Version) || !validVersion(nested.Version) || compareVersions(selected.Version, nested.Version) < 0
        || !sums.has(`${selected.Path} ${selected.Version}`) || !sums.has(`${selected.Path} ${selected.Version}/go.mod`)) throw new Error(`Controlled Go fork requires an explicitly locked root dependency: ${nested.Path} ${nested.Version}`)
    }
  }
  const replaced = new Set((lock.localForks ?? []).map(fork => fork.path))
  return (module.Require ?? []).filter((entry: any) => !replaced.has(entry.Path))
}

export async function prepareGoDependencies(options: {
  rootDir: string
  seedDir?: string
  sourceCache?: string
  execute?: CommandRunner
  sourceEnvironment?: NodeJS.ProcessEnv
}) {
  const root = resolve(options.rootDir)
  const lock = goDependencyLock(root)
  const seed = resolve(options.seedDir ?? defaultGoDependencySeed(root))
  disposableCachePath(seed, root)
  if (existsSync(seed)) throw new Error(`Go dependency seed already exists; use a fresh preparation destination: ${seed}`)
  const home = mkdtempSync(join(tmpdir(), 'go-dependency-preparation-'))
  const env = createHermeticGoEnvironment(home, options.sourceEnvironment, !options.sourceCache)
  const cwd = join(home, 'repository/backend')
  const execute = options.execute ?? executeCommand
  try {
    mkdirSync(cwd, { recursive: true })
    cpSync(join(root, 'backend/go.mod'), join(cwd, 'go.mod'))
    if (lock.goSum) cpSync(join(root, 'backend/go.sum'), join(cwd, 'go.sum'))
    for (const fork of lock.localForks ?? []) cpSync(localForkDirectory(root, fork.directory), join(cwd, fork.directory.slice('backend/'.length)), { recursive: true })
    if (options.sourceCache) {
      if (!isAbsolute(options.sourceCache)) throw new Error('Explicit Go dependency source cache must be absolute')
      disposableCachePath(options.sourceCache, root)
      cacheFiles(options.sourceCache)
      copyCache(options.sourceCache, env.GOMODCACHE)
    }
    const module = JSON.parse(await checked(execute, ['mod', 'edit', '-json'], cwd, env))
    const requirements = await validateLocalForks(module, lock, execute, cwd, env)
    const downloaded = parseGoJsonStream(await checked(execute, ['mod', 'download', '-json'], cwd, env))
    const sums = dependencySums(cwd)
    const modules: DependencyManifest['modules'] = []
    for (const entry of downloaded) {
      if (entry.Error || !entry.Path || !entry.Version || !entry.Sum || !entry.GoModSum || !entry.Dir || !entry.Zip || !entry.GoMod) {
        throw new Error(`Go dependency download is incomplete: ${entry.Path ?? 'unknown'} ${entry.Error ?? ''}`)
      }
      for (const file of [entry.Dir, entry.Zip, entry.GoMod]) {
        if (!isAbsolute(file) || !existsSync(file) || !realpathSync(file).startsWith(`${realpathSync(env.GOMODCACHE)}${sep}`)) throw new Error('Go dependency download escaped its private module cache')
      }
      if (sums.get(`${entry.Path} ${entry.Version}`) !== entry.Sum || sums.get(`${entry.Path} ${entry.Version}/go.mod`) !== entry.GoModSum) {
        throw new Error(`Go dependency checksum is not pinned in go.sum: ${entry.Path} ${entry.Version}`)
      }
      modules.push({ path: entry.Path, version: entry.Version, sum: entry.Sum, goModSum: entry.GoModSum })
    }
    if (modules.length !== requirements.length) throw new Error('Go dependency download did not cover every explicit locked requirement')
    if (requirements.some((requirement: any) => modules.filter(entry => entry.path === requirement.Path && entry.version === requirement.Version).length !== 1)) {
      throw new Error('Go dependency download has duplicate or missing locked modules')
    }
    await checked(execute, ['mod', 'verify'], cwd, env)
    if (hash(readFileSync(join(cwd, 'go.mod'))) !== lock.goMod || (existsSync(join(cwd, 'go.sum')) ? hash(readFileSync(join(cwd, 'go.sum'))) : null) !== lock.goSum
      || JSON.stringify(localForks(resolve(cwd, '..'), module.Replace ?? [])) !== JSON.stringify(lock.localForks ?? [])
      || JSON.stringify(goDependencyLock(root)) !== JSON.stringify(lock)) throw new Error('Go dependency preparation changed or raced the committed lock files')
    const toolchain = (await checked(execute, ['version'], cwd, env)).trim()
    const files = cacheFiles(env.GOMODCACHE)
    mkdirSync(seed, { recursive: true })
    cpSync(env.GOMODCACHE, join(seed, 'modules'), { recursive: true })
    const manifest: DependencyManifest = { schemaVersion: 1, lock, toolchain, modules: modules.sort((a, b) => a.path.localeCompare(b.path)), files }
    writeFileSync(join(seed, 'manifest.json'), JSON.stringify(manifest, null, 2) + '\n')
    return { seed, manifest }
  } catch (error) {
    writableDirectories(seed)
    rmSync(seed, { recursive: true, force: true })
    throw error
  } finally {
    writableDirectories(home)
    rmSync(home, { recursive: true, force: true })
  }
}

export async function hydrateGoDependencies(rootDir: string, env: Record<string, string>, options: {
  seedDir?: string
  sourceEnvironment?: NodeJS.ProcessEnv
  execute?: CommandRunner
} = {}) {
  const root = resolve(rootDir)
  const execute = options.execute ?? executeCommand
  if (env.GOPROXY !== 'off' || env.GOSUMDB !== 'off' || env.GOTOOLCHAIN !== 'local' || env.GOWORK !== 'off' || env.GOFLAGS !== '-mod=readonly'
    || env.GOENV !== 'off' || env.GOAUTH !== 'off' || env.GOVCS !== '*:off') {
    throw new Error('Go dependency hydration requires the offline hermetic environment')
  }
  assertTemporaryHome(env.HOME)
  const canonicalHome = realpathSync(env.HOME)
  for (const name of ['GOPATH', 'GOMODCACHE', 'GOCACHE']) {
    const path = env[name]
    if (!path || !isAbsolute(path) || !existsSync(path) || lstatSync(path).isSymbolicLink()
      || !realpathSync(path).startsWith(`${canonicalHome}${sep}`)) throw new Error(`Go dependency hydration requires a private sandbox ${name}`)
  }
  const lock = goDependencyLock(root)
  const module = JSON.parse(await checked(execute, ['mod', 'edit', '-json'], join(root, 'backend'), env))
  const requirements = await validateLocalForks(module, lock, execute, join(root, 'backend'), env)
  if (!module.Require?.length) return { modules: 0, seed: null }
  const seed = resolve(options.seedDir ?? options.sourceEnvironment?.[GO_DEPENDENCY_SEED_ENV] ?? defaultGoDependencySeed(root))
  disposableCachePath(seed, root)
  if (!existsSync(join(seed, 'manifest.json'))) throw new Error(`Missing prepared Go dependencies. Run bun run prepare:go first; expected seed ${seed}`)
  if (lstatSync(seed).isSymbolicLink() || lstatSync(join(seed, 'manifest.json')).isSymbolicLink()) throw new Error('Go dependency seed must not be a symlink')
  const manifest = JSON.parse(readFileSync(join(seed, 'manifest.json'), 'utf8')) as DependencyManifest
  if (manifest.schemaVersion !== 1 || JSON.stringify(manifest.lock) !== JSON.stringify(lock)) throw new Error('Prepared Go dependency seed does not match current go.mod/go.sum')
  const cache = join(seed, 'modules')
  if (!Array.isArray(manifest.files) || JSON.stringify(cacheFiles(cache)) !== JSON.stringify(manifest.files)) throw new Error('Prepared Go dependency cache integrity check failed')
  if (!Array.isArray(manifest.modules) || manifest.modules.length !== requirements.length) throw new Error('Prepared Go dependency inventory is incomplete')
  const sums = dependencySums(join(root, 'backend'))
  for (const requirement of requirements) {
    const dependency = manifest.modules.find(entry => entry.path === requirement.Path && entry.version === requirement.Version)
    if (!dependency || sums.get(`${dependency.path} ${dependency.version}`) !== dependency.sum || sums.get(`${dependency.path} ${dependency.version}/go.mod`) !== dependency.goModSum) throw new Error('Prepared Go dependency does not match a pinned module checksum')
  }
  const destination = resolve(env.GOMODCACHE)
  if (lstatSync(env.HOME).isSymbolicLink() || lstatSync(destination).isSymbolicLink()
    || !realpathSync(destination).startsWith(`${canonicalHome}${sep}`) || realpathSync(destination) === realpathSync(cache)
    || readdirSync(destination).length > 0) throw new Error('Go dependency hydration requires an empty private sandbox module cache')
  copyCache(cache, destination)
  await checked(execute, ['mod', 'verify'], join(root, 'backend'), env)
  if (JSON.stringify(goDependencyLock(root)) !== JSON.stringify(lock)) throw new Error('Go dependency hydration raced the committed lock files or local fork sources')
  return { modules: manifest.modules.length, seed }
}

export async function createGoBuildEnvironment(rootDir: string, home: string, source: NodeJS.ProcessEnv = process.env) {
  const env = createHermeticGoEnvironment(home, source)
  await hydrateGoDependencies(rootDir, env, { sourceEnvironment: source })
  return env
}

if (import.meta.main) {
  const args = process.argv.slice(2)
  const value = (name: string) => {
    const index = args.indexOf(name)
    if (index === -1) return undefined
    if (!args[index + 1] || args[index + 1].startsWith('--') || !isAbsolute(args[index + 1])) throw new Error(`${name} requires an absolute path`)
    return args[index + 1]
  }
  for (let index = 0; index < args.length; index += 2) if (!['--seed-dir', '--source-cache'].includes(args[index])) throw new Error(`Unknown Go dependency preparation argument: ${args[index]}`)
  const result = await prepareGoDependencies({ rootDir: process.cwd(), seedDir: value('--seed-dir'), sourceCache: value('--source-cache') })
  console.log(`Prepared ${result.manifest.modules.length} pinned Go dependencies: ${result.seed}`)
}
