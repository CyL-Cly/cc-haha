import { chmod, copyFile, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import {
  getBundledRipgrepName,
  stageRipgrepLicenses,
} from './prepare-ripgrep'
import { signMacBinary } from './sign-identity'
import { readScopeExclusions, recordComputerUseExclusion } from '../../scripts/quality-gate/scope-exclusions'
import {
  createCuHelperBuildEnv,
  resolveCuHelperArch,
  removeStagedCuHelperArtifacts,
  type CuHelperArch,
} from './cu-helper-build-target'
import { assertGoSidecarEnvironment } from '../electron/services/sidecarBackend'
import { createGoSidecarBuildPlan, removeLegacyBusinessSidecars } from './go-sidecar-build'

const scopeExclusions = readScopeExclusions()
const computerUseExcluded = scopeExclusions.includes('computer-use')

const desktopRoot = path.resolve(import.meta.dir, '..')
const repoRoot = path.resolve(desktopRoot, '..')
const binariesDir = path.join(desktopRoot, 'src-tauri', 'binaries')

// The SwiftPM resource bundle the cu-helper binary loads at runtime via
// `Bundle.module`. It MUST travel next to the binary or the LensSequence
// overlay assets fail to load. Declared up here (BEFORE the top-level
// `await buildCuHelper()` below) so it is initialized when that runs — a
// `const` placed after the call site hits the temporal dead zone (ReferenceError).
// SwiftPM names the resource bundle `${PackageName}_${TargetName}.bundle`.
// Package stays `cu-helper`; the executable target is now `cc-haha-computer-use`.
const CU_HELPER_RESOURCE_BUNDLE = 'cu-helper_cc-haha-computer-use.bundle'

const targetTriple =
  process.env.SIDECAR_TARGET_TRIPLE ||
  process.env.TAURI_ENV_TARGET_TRIPLE ||
  process.env.CARGO_BUILD_TARGET ||
  (await detectHostTriple())

assertGoSidecarEnvironment(process.env)

await mkdir(binariesDir, { recursive: true })
await stageRipgrepLicenses(binariesDir)
await stageHostRipgrepForOfflineBuild()

// Server and CLI share the Go executable. No TS business launcher is packaged.
await compileGoExecutable()
await removeLegacyBusinessSidecars(binariesDir)

console.log(`[build-sidecars] Built Go desktop sidecar for ${targetTriple}`)

// macOS-only: build + bundle the native `cu-helper` Computer Use binary.
// On Windows/Linux this is skipped entirely so the Python helper path is
// preserved (helperBridge.ts routes non-darwin → python). We do NOT ad-hoc
// re-sign cu-helper here: native/cu-helper/build.sh already signs it with a
// STABLE identity + hardened runtime, and re-signing would rotate its TCC
// identity, dropping the user's Accessibility + Screen Recording grants.
const cuHelperArch = resolveCuHelperArch(targetTriple)
if (process.platform === 'darwin' && cuHelperArch) {
  if (computerUseExcluded) {
    await removeStagedCuHelperArtifacts(binariesDir)
    recordComputerUseExclusion('cu-helper-build')
  } else {
    await buildCuHelper(cuHelperArch)
  }
}

async function stageHostRipgrepForOfflineBuild() {
  const destination = path.join(binariesDir, getBundledRipgrepName(targetTriple))
  const manifestPath = path.join(binariesDir, 'ripgrep-manifest.json')
  if (
    await Bun.file(destination).exists() &&
    await hasMatchingRipgrepManifest(manifestPath, targetTriple)
  ) {
    return
  }

  const hostTriple = await detectHostTriple()
  if (hostTriple !== targetTriple) {
    throw new Error(
      `[build-sidecars] bundled ripgrep missing for cross target ${targetTriple}; run prepare:ripgrep first`,
    )
  }

  const systemRipgrep = Bun.which('rg')
  if (!systemRipgrep) {
    console.warn(
      '[build-sidecars] system ripgrep unavailable for offline build; release builds must run prepare:ripgrep',
    )
    return
  }

  const versionCheck = Bun.spawn([systemRipgrep, '--version'], {
    stdout: 'pipe',
    stderr: 'ignore',
  })
  const [versionOutput, versionExit] = await Promise.all([
    new Response(versionCheck.stdout).text(),
    versionCheck.exited,
  ])
  if (versionExit !== 0 || !versionOutput.startsWith('ripgrep ')) {
    console.warn(`[build-sidecars] refusing unusable system ripgrep at ${systemRipgrep}`)
    return
  }

  await copyFile(systemRipgrep, destination)
  await chmod(destination, 0o755)
  await writeFile(
    manifestPath,
    `${JSON.stringify({
      version: versionOutput.split(/\r?\n/, 1)[0]?.replace(/^ripgrep\s+/, ''),
      targetTriple,
      source: 'system-dev-fallback',
    }, null, 2)}\n`,
  )
  console.log(`[build-sidecars] staged host ripgrep for offline build: ${destination}`)
}

async function compileGoExecutable() {
  const telemetryDir = await mkdtemp(path.join(tmpdir(), 'cc-haha-go-build-telemetry-'))
  await chmod(telemetryDir, 0o700)
  await writeFile(path.join(telemetryDir, 'mode'), 'off\n', { mode: 0o600 })
  try {
    const packageJson = JSON.parse(await readFile(path.join(repoRoot, 'package.json'), 'utf8')) as { version: string }
    const plan = createGoSidecarBuildPlan({
      repoRoot,
      binariesDir,
      triple: targetTriple,
      version: process.env.CLAUDE_CODE_LOCAL_VERSION ?? packageJson.version,
      telemetryDir,
      env: process.env,
    })
    const probe = Bun.spawn([plan.command, 'env', '-json', 'GOTELEMETRY', 'GOTELEMETRYDIR'], {
      cwd: plan.cwd, env: plan.env, stdout: 'pipe', stderr: 'inherit',
    })
    const [raw, probeExit] = await Promise.all([new Response(probe.stdout).text(), probe.exited])
    if (probeExit !== 0) throw new Error(`[build-sidecars] Go telemetry probe failed (exit ${probeExit})`)
    const observed = JSON.parse(raw) as { GOTELEMETRY: string; GOTELEMETRYDIR: string }
    if (observed.GOTELEMETRY !== 'off' || observed.GOTELEMETRYDIR !== telemetryDir) {
      throw new Error('[build-sidecars] Go telemetry must be off in the private build directory')
    }
    const build = Bun.spawn([plan.command, ...plan.args], {
      cwd: plan.cwd, env: plan.env, stdout: 'inherit', stderr: 'inherit',
    })
    const buildExit = await build.exited
    if (buildExit !== 0) throw new Error(`[build-sidecars] Go sidecar build failed (exit ${buildExit})`)
    if (targetTriple.includes('apple-darwin') && process.platform === 'darwin') {
      await signMacBinary(plan.outputPath, desktopRoot, {
        adHocOnly: computerUseExcluded && process.env.CSC_IDENTITY_AUTO_DISCOVERY === 'false',
      })
    }
    console.log(`[build-sidecars] Go CLI / Server -> ${plan.outputPath}`)
  } finally {
    await rm(telemetryDir, { recursive: true, force: true })
  }
}

async function hasMatchingRipgrepManifest(
  manifestPath: string,
  expectedTargetTriple: string,
): Promise<boolean> {
  try {
    const manifest = JSON.parse(await readFile(manifestPath, 'utf8')) as {
      targetTriple?: string
    }
    return manifest.targetTriple === expectedTargetTriple
  } catch {
    return false
  }
}

async function detectHostTriple() {
  const platform = process.platform
  const arch = process.arch

  if (platform === 'darwin') {
    if (arch === 'arm64') return 'aarch64-apple-darwin'
    if (arch === 'x64') return 'x86_64-apple-darwin'
  }

  if (platform === 'win32') {
    if (arch === 'x64') return 'x86_64-pc-windows-msvc'
    if (arch === 'arm64') return 'aarch64-pc-windows-msvc'
  }

  if (platform === 'linux') {
    if (arch === 'x64') return 'x86_64-unknown-linux-gnu'
    if (arch === 'arm64') return 'aarch64-unknown-linux-gnu'
  }

  throw new Error(`[build-sidecars] Unsupported host platform/arch: ${platform}/${arch}`)
}

/**
 * macOS-only: run `native/cu-helper/build.sh`, then copy the produced (already
 * signed) `cu-helper` binary AND its sibling SwiftPM resource bundle into
 * `desktop/src-tauri/binaries/` so electron-builder packs them (the existing
 * `src-tauri/binaries/**` glob already covers both).
 *
 * The copy is byte-preserving (`cp -R`) so cu-helper's stable `dev.cchaha.cu-helper`
 * Mach-O signature is left intact — we never strip or re-sign it here.
 */
async function buildCuHelper(arch: CuHelperArch) {
  const buildScript = path.join(repoRoot, 'native', 'cu-helper', 'build.sh')
  console.log(`[build-sidecars] Building native cu-helper (${arch}) via ${buildScript} ...`)

  const proc = Bun.spawn(['bash', buildScript], {
    cwd: path.dirname(buildScript),
    env: createCuHelperBuildEnv(targetTriple, process.env),
    // build.sh prints all diagnostics to STDERR and the ONE machine-readable
    // `built: <abs path>` line to STDOUT, so capture stdout and inherit stderr.
    stdout: 'pipe',
    stderr: 'inherit',
  })
  const stdout = await new Response(proc.stdout).text()
  const exitCode = await proc.exited
  if (exitCode !== 0) {
    throw new Error(`[build-sidecars] cu-helper build.sh failed (exit ${exitCode})`)
  }

  // Parse the single `built: <abs path>` line (build.sh:286).
  const builtMatch = stdout.match(/^built:\s*(.+)$/m)
  const builtBinary = builtMatch?.[1]?.trim()
  if (!builtBinary) {
    throw new Error(
      `[build-sidecars] cu-helper build.sh did not print a 'built: <path>' line.\nstdout:\n${stdout}`,
    )
  }

  // build.sh now emits the .app BUNDLE path (Screen Recording only works for a
  // real .app bundle subject, not a bare Mach-O). Copy the WHOLE bundle — the
  // resource bundle lives inside it at Contents/Resources/, and the spawnable
  // executable is at Contents/MacOS/cc-haha-computer-use (see cuHelperBridge.ts).
  const destApp = path.join(binariesDir, 'cc-haha-computer-use.app')

  // Remove any stale copies first (incl. ALL legacy bare-binary / bundle / old
  // -name artifacts) so `cp -R` does not nest into an existing dir.
  await Bun.spawn(
    ['rm', '-rf',
     destApp,
     path.join(binariesDir, 'cc-haha-computer-use'),         // legacy bare binary
     path.join(binariesDir, 'cu-helper'),                    // legacy old-name binary
     path.join(binariesDir, CU_HELPER_RESOURCE_BUNDLE),      // legacy sibling bundle
     path.join(binariesDir, 'cu-helper_cu-helper.bundle')],
    { stderr: 'inherit' },
  ).exited

  // Byte-preserving copy — keeps the .app's signature; do NOT re-sign.
  await copyPreserving(builtBinary, destApp)

  console.log(`[build-sidecars] cu-helper .app bundle -> ${destApp}`)
}

/** `cp -R <src> <dest>` — preserves the Mach-O code signature byte-for-byte. */
async function copyPreserving(src: string, dest: string) {
  const proc = Bun.spawn(['cp', '-R', src, dest], { stderr: 'inherit' })
  const exitCode = await proc.exited
  if (exitCode !== 0) {
    throw new Error(`[build-sidecars] failed to copy ${src} -> ${dest} (exit ${exitCode})`)
  }
}
