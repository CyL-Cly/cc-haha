import { expect, test } from 'bun:test'
import { createHash } from 'node:crypto'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, realpathSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { createHermeticGoEnvironment } from './go-dependencies'
import { mergeGoChildCoverage, writeGoChildCoverageManifest, type GoChildCoverageManifest } from './go-child-coverage'
import { parseGoCoverageProfile, parseGoCoverageProfileFile } from './run-go-tests'
import { createGoCoverageStorage } from './go-coverage-storage'

const hash = (bytes: string | Buffer) => createHash('sha256').update(bytes).digest('hex')
async function execute(command: string[], options: { cwd: string; env: Record<string, string> }) {
  const process = Bun.spawn(command, { ...options, stdout: 'pipe', stderr: 'pipe' })
  const [exitCode, stdout, stderr] = await Promise.all([process.exited, new Response(process.stdout).text(), new Response(process.stderr).text()])
  return { exitCode, stdout, stderr }
}

// These fixtures prove collection semantics using the actual installed compiler
// and runtime exit hooks. They do not stand in for any product consumer test.
test('collects actual fresh CLI and reused test-helper counters without changing the parent denominator', async () => {
  const root = realpathSync(mkdtempSync(join(tmpdir(), 'go-child-counter-regression-')))
  const home = join(root, 'home'), backend = join(root, 'backend'), directory = join(root, 'evidence')
  mkdirSync(home); mkdirSync(join(backend, 'cmd/claude-haha'), { recursive: true })
  const modulePath = 'fixture.example/childcoverage'
  const resources: Record<string, string> = {
    'go.mod': `module ${modulePath}\n\ngo 1.25.7\n`,
    'leaf.go': 'package fixture\nfunc Parent() int { return 1 }\nfunc Child() int { return 2 }\nfunc Helper() int { return 3 }\n',
    'cmd/claude-haha/main.go': `package main\nimport ("fmt"; fixture "${modulePath}")\nfunc main() { fmt.Println(fixture.Child()) }\n`,
    'public_go_coverage_test.go': `package fixture\nimport (\"os/exec\"; \"testing\"; \"${modulePath}/testdata/gochildcoverage\")\nfunc TestMain(m *testing.M) { gochildcoverage.Main(m) }\nfunc capturePublicGoCoverage(t *testing.T, c *exec.Cmd) { gochildcoverage.Capture(t,c) }\n`,
    'testdata/gochildcoverage/coverage.go': readFileSync(join(import.meta.dir, '../../backend/testdata/gochildcoverage/coverage.go'), 'utf8'),
    'leaf_test.go': `package fixture
import ("os"; "os/exec"; "path/filepath"; "testing"; "${modulePath}/testdata/gochildcoverage")
func TestParentAndActualChild(t *testing.T) {
 if Parent()!=1 {t.Fatal("parent")}
 binary:=filepath.Join(t.TempDir(),"cli")
 build:=exec.Command("go","build","-o",binary,"./cmd/claude-haha")
 capturePublicGoCoverage(t,build)
 if os.Getenv("OWNED_INCOMPLETE_BUILD")=="1" {gochildcoverage.RegisterBuilt(t,build);t.Fatal("unstarted build was registered")}
 if out,e:=build.CombinedOutput();e!=nil {t.Fatal(e,string(out))}
 if os.Getenv("OWNED_UNREGISTERED_COPY")!="1" {gochildcoverage.RegisterBuilt(t,build)}
 copied:=filepath.Join(t.TempDir(),"byte-copied-relocated-cli");data,e:=os.ReadFile(binary);if e!=nil {t.Fatal(e)};if e=os.WriteFile(copied,data,0700);e!=nil {t.Fatal(e)};if e=os.Remove(binary);e!=nil {t.Fatal(e)};binary=copied
 if os.Getenv("OWNED_MODIFIED_COPY")=="1" {if e=os.WriteFile(binary,append(data,0),0700);e!=nil {t.Fatal(e)}}
 child:=exec.Command(binary);child.Env=[]string{"PATH="+os.Getenv("PATH")}
 if os.Getenv("OWNED_INDIRECT")=="1" {child.Env=append(child.Env,"GOCOVERDIR="+gochildcoverage.IndirectEnvironment(t,binary))} else {capturePublicGoCoverage(t,child)}
 if out,e:=child.CombinedOutput();e!=nil || string(out)!="2\\n" {t.Fatal(e,string(out))}
 self,e:=os.Executable();if e!=nil {t.Fatal(e)}
 helper:=exec.Command(self,"-test.run=^TestRealHelper$","-test.count=1");helper.Env=[]string{"PATH="+os.Getenv("PATH"),"OWNED_HELPER=1","CC_HAHA_TEAM_FOOTER_CLI="+binary}
 capturePublicGoCoverage(t,helper)
 if out,e:=helper.CombinedOutput();e!=nil {t.Fatal(e,string(out))}
}
func TestRealHelper(t *testing.T) { if os.Getenv("OWNED_HELPER")!="1" {t.Skip("owned helper only")};if Helper()!=3 {t.Fatal("helper")}; nested:=exec.Command(os.Getenv("CC_HAHA_TEAM_FOOTER_CLI"));if out,e:=nested.CombinedOutput();e!=nil||string(out)!="2\\n" {t.Fatal(e,string(out))} }
`,
  }
  for (const [file, bytes] of Object.entries(resources)) { mkdirSync(join(backend, file, '..'), { recursive: true }); writeFileSync(join(backend, file), bytes) }
  const env = createHermeticGoEnvironment(home, { PATH: process.env.PATH }, false, root)
  try {
    const platform = await execute(['go', 'env', '-json', 'GOOS', 'GOARCH', 'GOVERSION'], { cwd: backend, env })
    expect(platform.exitCode).toBe(0)
    const actual = JSON.parse(platform.stdout)
    const sourceSnapshot = Object.keys(resources).sort().map(file => ({ file: `backend/${file}`, sha256: hash(readFileSync(join(backend, file))) }))
    const active = ['backend/leaf.go', 'backend/cmd/claude-haha/main.go']
    const manifest: GoChildCoverageManifest = {
      schemaVersion: 1, rootDir: root, modulePath, target: `${actual.GOOS}/${actual.GOARCH}`, toolchain: actual.GOVERSION,
      coveragePackages: ['./...'], sourceSnapshot, controlledModules: [], sources: { active, excluded: [], unknown: [], formatted: sourceSnapshot.map(row => row.file) },
    }
    writeGoChildCoverageManifest(directory, manifest)
    const parentProfile = join(root, 'parent.cover'), outputProfile = join(root, 'merged.cover')
    const run = await execute(['go', 'test', '-count=1', '-covermode=atomic', '-coverpkg=./...', `-coverprofile=${parentProfile}`, `-outputdir=${directory}`, './...'], { cwd: backend, env })
    expect(run.exitCode, run.stdout + run.stderr).toBe(0)
    const original = readFileSync(parentProfile)
    const parent = await parseGoCoverageProfileFile(parentProfile, modulePath)
    const result = await mergeGoChildCoverage({ directory, parentProfile, outputProfile, manifest, execute, env })
    const merged = await parseGoCoverageProfileFile(outputProfile, modulePath)
    expect(result.binaries).toBe(2)
    expect(result.counterFiles).toBe(3)
    expect(result.newlyCoveredBlocks).toBeGreaterThanOrEqual(2)
    expect(parent.get('backend/leaf.go')!.covered.has(3)).toBe(false)
    expect(parent.get('backend/leaf.go')!.covered.has(4)).toBe(false)
    expect(merged.get('backend/leaf.go')!.covered.has(3)).toBe(true)
    expect(merged.get('backend/leaf.go')!.covered.has(4)).toBe(true)
    expect([...merged].map(([file, value]) => [file, [...value.executable]])).toEqual([...parent].map(([file, value]) => [file, [...value.executable]]))
    expect(readFileSync(parentProfile)).toEqual(original)
    // The real subprocess is launched through an environment-only producer;
    // attestation alone never contributes counters without actual execution.
    const indirectDirectory = join(root, 'indirect-evidence')
    writeGoChildCoverageManifest(indirectDirectory, manifest)
    const indirectParent = join(root, 'indirect-parent.cover'), indirectMerged = join(root, 'indirect-merged.cover')
    const indirect = await execute(['go', 'test', '-count=1', '-covermode=atomic', '-coverpkg=./...', `-coverprofile=${indirectParent}`, `-outputdir=${indirectDirectory}`, './...'], { cwd: backend, env: { ...env, OWNED_INDIRECT: '1' } })
    expect(indirect.exitCode, indirect.stdout + indirect.stderr).toBe(0)
    const indirectResult = await mergeGoChildCoverage({ directory: indirectDirectory, parentProfile: indirectParent, outputProfile: indirectMerged, manifest, execute, env })
    expect(indirectResult.binaries).toBe(2)
    expect(indirectResult.counterFiles).toBe(3)
    expect((await parseGoCoverageProfileFile(indirectMerged, modulePath)).get('backend/leaf.go')!.covered.has(3)).toBe(true)
    const unregisteredIndirect = await execute(['go', 'test', '-count=1', '-run=^TestParentAndActualChild$', '-covermode=atomic', '-coverpkg=./...', `-outputdir=${indirectDirectory}`, './...'], { cwd: backend, env: { ...env, OWNED_INDIRECT: '1', OWNED_UNREGISTERED_COPY: '1' } })
    expect(unregisteredIndirect.exitCode).toBe(1)
    expect(unregisteredIndirect.stdout + unregisteredIndirect.stderr).toContain('indirect public Go child was not built against this frozen coverage manifest')
    // The completed build's original bytes are the only relocation authority.
    // A merely compatible Go binary, or a command that never ran, is rejected.
    for (const [key, reason] of [
      ['OWNED_INCOMPLETE_BUILD', 'actually completed successful build'],
      ['OWNED_UNREGISTERED_COPY', 'not built against this frozen coverage manifest'],
      ['OWNED_MODIFIED_COPY', 'not built against this frozen coverage manifest'],
    ]) {
      const guarded = await execute(['go', 'test', '-count=1', '-run=^TestParentAndActualChild$', '-covermode=atomic', '-coverpkg=./...', `-outputdir=${directory}`, './...'], { cwd: backend, env: { ...env, [key]: '1' } })
      expect(guarded.exitCode).toBe(1)
      expect(guarded.stdout + guarded.stderr).toContain(reason)
    }
    const child = join(directory, 'raw', readdirSync(join(directory, 'raw'))[0])
    const receiptPath = join(child, 'receipt.json')
    const receiptBytes = readFileSync(receiptPath), receipt = JSON.parse(receiptBytes.toString())
    writeFileSync(receiptPath, JSON.stringify({ ...receipt, manifestSha256: '0'.repeat(64) }))
    await expect(mergeGoChildCoverage({ directory, parentProfile, outputProfile, manifest, execute, env })).rejects.toThrow('receipt')
    writeFileSync(receiptPath, receiptBytes)
    const freshReceiptPath = readdirSync(join(directory, 'raw')).map(name => join(directory, 'raw', name, 'receipt.json')).find(path => JSON.parse(readFileSync(path, 'utf8')).kind === 'fresh-cli')!
    const freshReceiptBytes = readFileSync(freshReceiptPath)
    const freshReceipt = JSON.parse(freshReceiptBytes.toString())
    writeFileSync(freshReceiptPath, JSON.stringify({ ...freshReceipt, buildArguments: freshReceipt.buildArguments.map((arg: string) => arg.startsWith('-coverpkg=') ? '-coverpkg=some.other/module/...' : arg) }))
    await expect(mergeGoChildCoverage({ directory, parentProfile, outputProfile, manifest, execute, env })).rejects.toThrow('original instrumented build arguments')
    writeFileSync(freshReceiptPath, freshReceiptBytes)
    const binary = join(directory, receipt.binary), binaryBytes = readFileSync(binary)
    writeFileSync(binary, 'not the executed binary')
    await expect(mergeGoChildCoverage({ directory, parentProfile, outputProfile, manifest, execute, env })).rejects.toThrow('binary SHA')
    writeFileSync(binary, binaryBytes)
    writeFileSync(join(backend, 'leaf.go'), resources['leaf.go'] + '// changed\n')
    await expect(mergeGoChildCoverage({ directory, parentProfile, outputProfile, manifest, execute, env })).rejects.toThrow('source SHA')
    writeFileSync(join(backend, 'leaf.go'), resources['leaf.go'])
    const rejectMetadata = async (changed: GoChildCoverageManifest, reason: string) => {
      writeGoChildCoverageManifest(directory, changed)
      const manifestSha256 = hash(readFileSync(join(directory, 'manifest.json')))
      for (const name of readdirSync(join(directory, 'raw'))) {
        const path = join(directory, 'raw', name, 'receipt.json')
        const value = JSON.parse(readFileSync(path, 'utf8'))
        writeFileSync(path, JSON.stringify({ ...value, manifestSha256 }))
      }
      await expect(mergeGoChildCoverage({ directory, parentProfile, outputProfile, manifest: changed, execute, env })).rejects.toThrow(reason)
    }
    await rejectMetadata({ ...manifest, target: 'wrong/target' }, 'target/toolchain')
    await rejectMetadata({ ...manifest, toolchain: 'go0.0.0' }, 'target/toolchain')
    writeGoChildCoverageManifest(directory, manifest)
    const restoredHash = hash(readFileSync(join(directory, 'manifest.json')))
    for (const name of readdirSync(join(directory, 'raw'))) {
      const path = join(directory, 'raw', name, 'receipt.json')
      const value = JSON.parse(readFileSync(path, 'utf8'))
      writeFileSync(path, JSON.stringify({ ...value, manifestSha256: restoredHash }))
    }
    const collect = { directory, parentProfile, outputProfile, manifest, env }
    await expect(mergeGoChildCoverage({ ...collect, execute: async (command, options) => {
      const result = await execute(command, options)
      if (command.includes('textfmt')) {
        const path = command.find(arg => arg.startsWith('-o='))!.slice(3)
        const actual = readFileSync(path, 'utf8')
        writeFileSync(path, actual.replace(/ (\d+) (\d+)\n/, ' 999999 $2\n'))
      }
      return result
    } })).rejects.toThrow('compiler block mismatch')
    await expect(mergeGoChildCoverage({ ...collect, execute: async (command, options) => {
      const result = await execute(command, options)
      if (command.includes('textfmt')) {
        const path = command.find(arg => arg.startsWith('-o='))!.slice(3)
        writeFileSync(path, readFileSync(path, 'utf8').replace('mode: atomic', 'mode: set'))
      }
      return result
    } })).rejects.toThrow('atomic')
    const failedSource = { ...manifest, sourceSnapshot: manifest.sourceSnapshot.map(row => row.file === 'backend/leaf.go' ? { ...row, sha256: '0'.repeat(64) } : row) }
    writeGoChildCoverageManifest(directory, failedSource)
    const guarded = await execute(['go', 'test', '-count=1', '-run=^TestParentAndActualChild$', '-covermode=atomic', '-coverpkg=./...', `-outputdir=${directory}`, './...'], { cwd: backend, env })
    expect(guarded.exitCode).toBe(1)
    expect(guarded.stdout + guarded.stderr).toContain('coverage source SHA changed: backend/leaf.go')
    expect(existsSync(join(directory, 'raw'))).toBe(true)
  } finally { rmSync(root, { recursive: true, force: true }) }
}, 120_000)


// Match the updater PTY: an already registered tagged release is copied under
// helper HOME, then the helper launches it by a derived path. The collector
// must receive that exact file explicitly; HOME never authorizes recursion.
test('attests a registered tagged release under helper HOME only through an explicit existing Capture file identity', async () => {
  const root = realpathSync(mkdtempSync(join(tmpdir(), 'go-child-nested-release-')))
  const home = join(root, 'home'), backend = join(root, 'backend')
  mkdirSync(home); mkdirSync(join(backend, 'cmd/claude-haha'), { recursive: true })
  const modulePath = 'fixture.example/childcoverage'
  const resources: Record<string, string> = {
    'go.mod': `module ${modulePath}\n\ngo 1.25.7\n`,
    'leaf.go': 'package fixture\nfunc Parent() int { return 1 }\nfunc Child() int { return 2 }\nfunc Helper() int { return 3 }\n',
    'cmd/claude-haha/main.go': `package main\nimport ("fmt"; fixture "${modulePath}")\nfunc main() { fmt.Printf("%d:%d\\n",fixture.Child(),fixtureTag()) }\n`,
    'cmd/claude-haha/main_default.go': '//go:build !native_process_fixture\n\npackage main\nfunc fixtureTag() int { return 0 }\n',
    'cmd/claude-haha/main_auth_login_fixture.go': '//go:build native_process_fixture\n\npackage main\nfunc fixtureTag() int { return 7 }\n',
    'testdata/gochildcoverage/coverage.go': readFileSync(join(import.meta.dir, '../../backend/testdata/gochildcoverage/coverage.go'), 'utf8'),
    'release_test.go': `package fixture
import ("os"; "os/exec"; "path/filepath"; "testing"; "${modulePath}/testdata/gochildcoverage")
func TestMain(m *testing.M) { gochildcoverage.Main(m) }
func TestParentAndNestedRelease(t *testing.T) {
 if Parent()!=1 {t.Fatal("real parent")}
 binary:=filepath.Join(t.TempDir(),"built-release")
 build:=exec.Command("go","build","-tags","native_process_fixture","-o",binary,"./cmd/claude-haha")
 gochildcoverage.Capture(t,build)
 if out,e:=build.CombinedOutput();e!=nil {t.Fatal(e,string(out))}
 gochildcoverage.RegisterBuilt(t,build)
 home:=t.TempDir();release:=filepath.Join(home,"private-go-release.bin")
 data,e:=os.ReadFile(binary);if e!=nil {t.Fatal(e)}
 if e=os.WriteFile(release,data,0700);e!=nil {t.Fatal(e)}
 if e=os.Remove(binary);e!=nil {t.Fatal(e)}
 self,e:=os.Executable();if e!=nil {t.Fatal(e)}
 helper:=exec.Command(self,"-test.run=^TestNestedReleaseHelper$","-test.count=1")
 helper.Env=[]string{"PATH="+os.Getenv("PATH"),"HOME="+home,"OWNED_NESTED_RELEASE_HELPER=1"}
 if os.Getenv("OWNED_EXPLICIT_RELEASE_IDENTITY")=="1" {helper.Env=append(helper.Env,"CC_HAHA_UPDATE_PTY_RELEASE_BINARY="+release)}
 gochildcoverage.Capture(t,helper)
 if out,e:=helper.CombinedOutput();e!=nil {t.Fatal(e,string(out))}
}
func TestNestedReleaseHelper(t *testing.T) {
 if os.Getenv("OWNED_NESTED_RELEASE_HELPER")!="1" {t.Skip("owned helper only")}
 if Helper()!=3 {t.Fatal("real helper")}
 child:=exec.Command(filepath.Join(os.Getenv("HOME"),"private-go-release.bin"));child.Env=os.Environ()
 if out,e:=child.CombinedOutput();e!=nil||string(out)!="2:7\\n" {t.Fatal(e,string(out))}
}
`,
  }
  for (const [file, bytes] of Object.entries(resources)) { mkdirSync(join(backend, file, '..'), { recursive: true }); writeFileSync(join(backend, file), bytes) }
  const env = createHermeticGoEnvironment(home, { PATH: process.env.PATH }, false, root)
  try {
    const platform = await execute(['go', 'env', '-json', 'GOOS', 'GOARCH', 'GOVERSION'], { cwd: backend, env })
    expect(platform.exitCode, platform.stdout + platform.stderr).toBe(0)
    const actual = JSON.parse(platform.stdout)
    const sourceSnapshot = Object.keys(resources).sort().map(file => ({ file: `backend/${file}`, sha256: hash(readFileSync(join(backend, file))) }))
    const excluded = 'backend/cmd/claude-haha/main_auth_login_fixture.go'
    const manifest: GoChildCoverageManifest = {
      schemaVersion: 1, rootDir: root, modulePath, target: `${actual.GOOS}/${actual.GOARCH}`, toolchain: actual.GOVERSION,
      coveragePackages: ['./...'], sourceSnapshot, controlledModules: [],
      sources: { active: ['backend/leaf.go', 'backend/cmd/claude-haha/main.go', 'backend/cmd/claude-haha/main_default.go'], excluded: [excluded], unknown: [], formatted: sourceSnapshot.map(row => row.file) },
    }
    for (const explicit of [false, true]) {
      const directory = join(root, explicit ? 'explicit-file-evidence' : 'home-only-evidence')
      writeGoChildCoverageManifest(directory, manifest)
      const parentProfile = join(root, explicit ? 'explicit-parent.cover' : 'home-only-parent.cover'), outputProfile = join(root, explicit ? 'explicit-merged.cover' : 'home-only-merged.cover')
      const run = await execute(['go', 'test', '-count=1', '-run=^TestParentAndNestedRelease$', '-covermode=atomic', '-coverpkg=./...', `-coverprofile=${parentProfile}`, `-outputdir=${directory}`, './...'], { cwd: backend, env: { ...env, OWNED_EXPLICIT_RELEASE_IDENTITY: explicit ? '1' : '0' } })
      expect(run.exitCode, run.stdout + run.stderr).toBe(0)
      const originalParent = readFileSync(parentProfile)
      const pods = readdirSync(join(directory, 'raw'))
      expect(pods.length).toBe(1)
      const receiptPath = join(directory, 'raw', pods[0], 'receipt.json'), receiptBytes = readFileSync(receiptPath), receipt = JSON.parse(receiptBytes.toString())
      expect(receipt.kind).toBe('self-test')
      expect(receipt.test).toBe('TestParentAndNestedRelease')
      expect(receipt.additionalBinaries ?? []).toHaveLength(explicit ? 1 : 0)
      if (!explicit) {
        await expect(mergeGoChildCoverage({ directory, parentProfile, outputProfile, manifest, execute, env })).rejects.toThrow('Child compiler block is absent from parent denominator:')
      } else {
        const identity = receipt.additionalBinaries[0]
        expect(identity.kind).toBe('fresh-cli')
        expect(identity.buildArguments).toContain('native_process_fixture')
        expect(hash(readFileSync(join(directory, identity.binary)))).toBe(identity.binarySha256)
        const result = await mergeGoChildCoverage({ directory, parentProfile, outputProfile, manifest, execute, env })
        expect(result.binaries).toBe(2)
        expect(result.counterFiles).toBe(2)
        expect(result.ignored).toContainEqual({ file: excluded, reason: 'not compiled in the unchanged default-tag parent inventory' })
        const parent = await parseGoCoverageProfileFile(parentProfile, modulePath), merged = await parseGoCoverageProfileFile(outputProfile, modulePath)
        expect(parent.get('backend/leaf.go')!.covered.has(3)).toBe(false)
        expect(merged.get('backend/leaf.go')!.covered.has(3)).toBe(true)
        expect([...merged].map(([file, value]) => [file, [...value.executable]])).toEqual([...parent].map(([file, value]) => [file, [...value.executable]]))
      }
      expect(readFileSync(parentProfile)).toEqual(originalParent)
      expect(readFileSync(receiptPath)).toEqual(receiptBytes)
    }
  } finally { rmSync(root, { recursive: true, force: true }) }
}, 120_000)


// Real compiler/exit-hook/covdata coverage, with only private output roots
// relocated between the default and opt-in storage runs.
test.skipIf(process.platform === 'win32')('losslessly stores actual parent and child textfmt profiles with the unchanged denominator and counters', async () => {
  const root = realpathSync(mkdtempSync(join(tmpdir(), 'go-child-lossless-storage-')))
  const backend = join(root, 'backend'), home = join(root, 'home')
  mkdirSync(join(backend, 'cmd/claude-haha'), { recursive: true })
  mkdirSync(home)
  const modulePath = 'fixture.example/losslesscoverage'
  const resources: Record<string, string> = {
    'go.mod': `module ${modulePath}\n\ngo 1.25.7\n`,
    'leaf.go': 'package fixture\nfunc Parent() int { return 1 }\nfunc Child() int { return 2 }\n',
    'cmd/claude-haha/main.go': `package main\nimport ("fmt"; fixture "${modulePath}")\nfunc main() { fmt.Println(fixture.Child()) }\n`,
    'testdata/gochildcoverage/coverage.go': readFileSync(join(import.meta.dir, '../../backend/testdata/gochildcoverage/coverage.go'), 'utf8'),
    'leaf_test.go': `package fixture
import ("os/exec"; "path/filepath"; "testing"; "${modulePath}/testdata/gochildcoverage")
func TestMain(m *testing.M) { gochildcoverage.Main(m) }
func TestActualParentAndChild(t *testing.T) {
 if Parent()!=1 {t.Fatal("parent")}
 binary:=filepath.Join(t.TempDir(),"child")
 build:=exec.Command("go","build","-o",binary,"./cmd/claude-haha")
 gochildcoverage.Capture(t,build)
 if out,e:=build.CombinedOutput();e!=nil {t.Fatal(e,string(out))}
 gochildcoverage.RegisterBuilt(t,build)
 child:=exec.Command(binary)
 gochildcoverage.Capture(t,child)
 if out,e:=child.CombinedOutput();e!=nil||string(out)!="2\\n" {t.Fatal(e,string(out))}
}
`,
  }
  for (const [name, value] of Object.entries(resources)) {
    mkdirSync(join(backend, name, '..'), { recursive: true })
    writeFileSync(join(backend, name), value)
  }
  const env = createHermeticGoEnvironment(home, { PATH: process.env.PATH }, false, root)
  const normalize = (coverage: Awaited<ReturnType<typeof parseGoCoverageProfileFile>>) => [...coverage].map(([file, value]) => [file, [...value.executable].sort((a, b) => a - b), [...value.covered].sort((a, b) => a - b)]).sort((a, b) => String(a[0]).localeCompare(String(b[0])))
  try {
    const platform = await execute(['go', 'env', '-json', 'GOOS', 'GOARCH', 'GOVERSION'], { cwd: backend, env })
    expect(platform.exitCode).toBe(0)
    const actual = JSON.parse(platform.stdout)
    const snapshot = Object.keys(resources).sort().map(name => ({ file: `backend/${name}`, sha256: hash(readFileSync(join(backend, name))) }))
    const manifest: GoChildCoverageManifest = {
      schemaVersion: 1, rootDir: root, modulePath, target: `${actual.GOOS}/${actual.GOARCH}`, toolchain: actual.GOVERSION,
      coveragePackages: ['./...'], sourceSnapshot: snapshot, controlledModules: [],
      sources: { active: ['backend/leaf.go', 'backend/cmd/claude-haha/main.go'], excluded: [], unknown: [], formatted: snapshot.map(row => row.file) },
    }
    const observed: Array<{ parent: ReturnType<typeof normalize>; merged: ReturnType<typeof normalize>; binaries: number; counters: number; blocks: number; newlyCovered: number; commands: string[][] }> = []
    for (const mode of ['regular', 'gzip-fifo'] as const) {
      const artifact = join(root, mode)
      const directory = join(artifact, 'child-coverage'), parent = join(artifact, 'coverage.out'), merged = join(artifact, 'coverage-with-children.out')
      writeGoChildCoverageManifest(directory, manifest)
      const storage = mode === 'gzip-fifo' ? createGoCoverageStorage({ directory: artifact }) : undefined
      const commands: string[][] = []
      const run = async (command: string[], options: { cwd: string; env: Record<string, string> }) => {
        commands.push(command)
        return execute(command, options)
      }
      try {
        const command = ['go', 'test', '-count=1', '-covermode=atomic', '-coverpkg=./...', `-coverprofile=${parent}`, `-outputdir=${directory}`, './...']
        const producer = async () => {
          const result = await run(command, { cwd: backend, env })
          expect(result.exitCode, result.stdout + result.stderr).toBe(0)
          return result
        }
        if (storage) await storage.capture(parent, producer)
        else await producer()
        const original = storage ? await storage.read(parent, async input => {
          const chunks: Buffer[] = []
          for await (const chunk of input) chunks.push(Buffer.from(chunk))
          return Buffer.concat(chunks)
        }) : readFileSync(parent)
        const parentCoverage = parseGoCoverageProfile(original.toString(), modulePath)
        const result = await mergeGoChildCoverage({ directory, parentProfile: parent, outputProfile: merged, manifest, execute: run, env, profileStorage: storage })
        const actualMerged = await parseGoCoverageProfileFile(merged, modulePath)
        expect(result.counterFiles).toBeGreaterThan(0)
        expect(result.binaries).toBeGreaterThan(0)
        expect(result.newlyCoveredBlocks).toBeGreaterThan(0)
        expect(normalize(actualMerged).map(row => row.slice(0, 2))).toEqual(normalize(parentCoverage).map(row => row.slice(0, 2)))
        if (storage) {
          expect(existsSync(parent)).toBe(false)
          expect(readdirSync(directory).filter(name => name.endsWith('.cover'))).toEqual([])
          expect(storage.evidence().profiles).toHaveLength(result.profiles.length + 1)
          expect(storage.evidence().profiles.every(row => row.status === 'complete')).toBe(true)
          expect(storage.evidence().profiles[0].descriptor!.rawSHA256).toBe(hash(original))
        } else expect(readFileSync(parent)).toEqual(original)
        // Only these actual, collector-verified profiles authorize child-path
        // normalization; every covdata argv and all coverage data stay checked.
        expect(new Set(result.profiles).size).toBe(result.profiles.length)
        const verifiedChildArguments = new Map<string[], [string, string]>()
        for (const [ordinal, profile] of result.profiles.entries()) {
          expect(profile).toMatch(/^child-[0-9]+\.cover$/)
          const childName = profile.slice(0, -'.cover'.length)
          const input = `-i=${join(directory, 'raw', childName)}`
          const output = `-o=${join(directory, profile)}`
          const references = commands.filter(command => command.includes(input) || command.includes(output))
          expect(references).toHaveLength(1)
          expect(references[0]).toEqual(['go', 'tool', 'covdata', 'textfmt', input, output])
          verifiedChildArguments.set(references[0]!, [
            `-i=<artifact>/child-coverage/raw/<verified-child-${ordinal}>`,
            `-o=<artifact>/child-coverage/<verified-child-${ordinal}>.cover`,
          ])
        }
        expect(commands.filter(command => command[0] === 'go' && command[1] === 'tool' && command[2] === 'covdata' && command[3] === 'textfmt')).toHaveLength(result.profiles.length)
        observed.push({ parent: normalize(parentCoverage), merged: normalize(actualMerged), binaries: result.binaries, counters: result.counterFiles, blocks: result.compilerBlocks, newlyCovered: result.newlyCoveredBlocks,
          commands: commands.map(command => {
            const normalized = [...command]
            const childArguments = verifiedChildArguments.get(command)
            if (childArguments) [normalized[4], normalized[5]] = childArguments
            return normalized.map(value => value.replaceAll(artifact, '<artifact>').replace(/<artifact>\/child-coverage\/bin\/[a-f0-9]{64}/g, '<artifact>/child-coverage/bin/<verified-binary-sha>'))
          }) })
      } finally {
        await storage?.close()
        if (storage) expect(storage.evidence()).toMatchObject({ ownedStreams: 0, ownedAnchors: 0 })
      }
    }
    expect(observed[1]).toEqual(observed[0])
    expect(Object.keys(resources).sort().map(name => hash(readFileSync(join(backend, name))))).toEqual(snapshot.map(row => row.sha256))
  } finally { rmSync(root, { recursive: true, force: true }) }
}, 120_000)
