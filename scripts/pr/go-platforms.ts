import { createHash } from 'node:crypto'
import { lstatSync, readFileSync, readdirSync } from 'node:fs'
import { dirname, join, relative, resolve, sep } from 'node:path'
import { GO_CONTROLLED_LOCAL_FORKS, parseGoJsonStream } from './go-dependencies'

// Release/dev desktop matrices define these shipped targets. Runner labels name
// native hosts; a cross-compiled binary is never test or coverage evidence.
export const GO_PLATFORMS = [
  { target: 'darwin/amd64', runner: 'macos-15-intel', race: true },
  { target: 'darwin/arm64', runner: 'macos-15', race: true },
  { target: 'linux/amd64', runner: 'ubuntu-22.04', race: true },
  { target: 'linux/arm64', runner: 'ubuntu-22.04-arm', race: true },
  { target: 'windows/amd64', runner: 'windows-2025', race: true },
  { target: 'windows/arm64', runner: 'windows-11-arm', race: false },
] as const

export type GoSourceScope = { active: string[]; excluded: string[]; unknown: string[]; formatted: string[] }
export type GoPlatformEvidence = { target: string; toolchain: string; native: true; race: 'required' | 'unsupported'; reason?: string }

export function goNativeTarget(platform: string = process.platform, arch: string = process.env.GITHUB_ACTIONS === 'true' ? process.env.RUNNER_ARCH?.toLowerCase() ?? process.arch : process.arch) {
  const operatingSystem = { darwin: 'darwin', linux: 'linux', win32: 'windows' }[platform]
  // Bun may itself run under x64 emulation on Windows ARM. GitHub's runner
  // hardware architecture is authoritative there; Go is explicitly installed
  // for that architecture and its actual native test binaries must match it.
  const architecture = { x64: 'amd64', arm64: 'arm64' }[arch.toLowerCase()]
  if (!operatingSystem || !architecture) throw new Error(`Unsupported native Go test host: ${platform}/${arch}`)
  return `${operatingSystem}/${architecture}`
}

export function goPlatformEvidence(environment: { GOOS?: string; GOARCH?: string; GOVERSION?: string }, expected: string, native = goNativeTarget()): GoPlatformEvidence {
  const target = `${environment.GOOS}/${environment.GOARCH}`
  const rule = GO_PLATFORMS.find(platform => platform.target === target)
  if (!rule || target !== expected || target !== native) throw new Error(`Go test target must execute natively: expected ${expected}, native host ${native}, Go toolchain ${target}`)
  if (!environment.GOVERSION) throw new Error('Go toolchain version evidence is missing')
  if (!rule.race && environment.GOVERSION !== 'go1.25.7') throw new Error('Review Windows ARM race support for this Go toolchain before accepting unsupported evidence')
  return {
    target, toolchain: environment.GOVERSION, native: true,
    race: rule.race ? 'required' : 'unsupported',
    ...(!rule.race ? { reason: 'Go 1.25.7 does not support the race detector on windows/arm64 (internal/platform.RaceDetectorSupported)' } : {}),
  }
}

export function scanGoSources(backendDir: string) {
  const root = resolve(backendDir)
  const files: string[] = []
  const visit = (directory: string) => {
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      if (entry.name === 'vendor') continue
      const path = join(directory, entry.name)
      if (entry.isSymbolicLink()) throw new Error(`Go source inventory cannot follow a symlink: ${path}`)
      if (entry.isDirectory()) visit(path)
      else if (entry.isFile() && entry.name.endsWith('.go')) files.push(path)
    }
  }
  if (lstatSync(root).isSymbolicLink()) throw new Error('Go backend source root must not be a symlink')
  visit(root)
  if (!files.length) throw new Error('Go source inventory is empty')
  return files.sort()
}

export type GoControlledModule = { path: string; version: string; directory: string }

export function goControlledModules(modules: GoControlledModule[]): GoControlledModule[] {
  const seen = new Set<string>()
  return modules.map(module => {
    const spec = GO_CONTROLLED_LOCAL_FORKS.find(spec => spec.path === module.path)
    if (!spec || spec.version !== module.version || spec.directory !== module.directory || seen.has(module.path)) {
      throw new Error('Go source module mapping is not an explicitly controlled pinned repository local fork')
    }
    seen.add(module.path)
    return { path: module.path, version: module.version, directory: module.directory }
  }).sort((left, right) => left.path.localeCompare(right.path))
}

export function goSourceDirectories(sources: string[], backendDir: string, localModules: GoControlledModule[] = []) {
  const modules = goControlledModules(localModules).map(module => ({ ...module, root: resolve(backendDir, '..', module.directory) }))
  return [...new Set(sources.map(source => {
    const directory = resolve(dirname(source))
    const module = modules.find(module => directory === module.root || directory.startsWith(`${module.root}${sep}`))
    if (module) {
      const suffix = relative(module.root, directory).split(sep).join('/')
      return module.path + (suffix ? `/${suffix}` : '')
    }
    return `./${relative(backendDir, directory).split(sep).join('/')}`
  }))].sort()
}

export function collectGoSourceScope(output: string, sources: string[], backendDir: string): GoSourceScope {
  const root = resolve(backendDir)
  const sourceSet = new Set(sources.map(source => resolve(source)))
  const active = new Set<string>(), excluded = new Set<string>(), formatted = new Set<string>()
  const packages = parseGoJsonStream(output)
  if (!packages.length) throw new Error('Go package source inventory is empty')
  for (const pkg of packages) {
    if (!pkg.Dir || !resolve(pkg.Dir).startsWith(`${root}${sep}`) && resolve(pkg.Dir) !== root) throw new Error('Go package inventory escaped the backend module')
    for (const [field, destination] of [['GoFiles', active], ['CgoFiles', active], ['IgnoredGoFiles', excluded], ['TestGoFiles', formatted], ['XTestGoFiles', formatted]] as const) {
      for (const name of pkg[field] ?? []) {
        const path = resolve(pkg.Dir, name)
        if (!sourceSet.has(path)) throw new Error(`Go package inventory contains an undiscovered source: ${path}`)
        destination.add(path)
        formatted.add(path)
      }
    }
  }
  for (const path of active) if (excluded.has(path)) throw new Error(`Go source is both active and excluded: ${path}`)
  const paths = (set: Iterable<string>) => [...set].map(path => `backend/${relative(root, path).split(sep).join('/')}`).sort()
  return {
    active: paths(active), excluded: paths(excluded),
    unknown: paths(sources.filter(source => !formatted.has(resolve(source)))),
    // Unrecognized source names also get formatting checks, but never receive
    // an exclusion or a fabricated zero-line coverage mapping.
    formatted: paths(sources),
  }
}

export function goSourceSnapshot(rootDir: string, sources: string[]) {
  const backend = resolve(rootDir, 'backend')
  const files: string[] = []
  const visit = (path: string) => {
    const entry = lstatSync(path)
    if (entry.isSymbolicLink()) throw new Error(`Go module input cannot follow a symlink: ${path}`)
    if (entry.isDirectory()) {
      for (const name of readdirSync(path).sort()) visit(join(path, name))
    } else if (entry.isFile()) files.push(path)
    else throw new Error(`Go module input must be a regular file or directory: ${path}`)
  }
  // Build outputs belong in root artifacts/ or disposable directories. Include
  // every module resource, including embed assets, testdata and vendor bytes;
  // source formatting/coverage still uses the separate Go source inventory.
  visit(backend)
  for (const source of sources) if (!files.includes(resolve(source))) throw new Error(`Missing Go source in module input inventory: ${source}`)
  const threshold = join(rootDir, 'scripts/quality-gate/coverage-thresholds.json')
  if (!lstatSync(threshold).isFile() || lstatSync(threshold).isSymbolicLink()) throw new Error('Go coverage threshold must be a regular file')
  files.push(threshold)
  return files.map(path => ({
    file: relative(rootDir, path).split(sep).join('/'),
    // Embed data and test fixtures observe bytes, so even CRLF is significant.
    // Native CI checkouts explicitly disable Git's automatic EOL conversion.
    sha256: createHash('sha256').update(readFileSync(path)).digest('hex'),
  })).sort((a, b) => a.file.localeCompare(b.file))
}
