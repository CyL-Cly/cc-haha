import { rm } from 'node:fs/promises'
import path from 'node:path'

export type CuHelperArch = 'arm64' | 'x86_64'

/**
 * Resolve the Swift helper architecture from the package target, never from
 * the build host. macOS x64 releases are commonly cross-built on Apple Silicon,
 * so `uname -m` is not a valid source of truth here.
 */
export function resolveCuHelperArch(targetTriple: string): CuHelperArch | null {
  if (targetTriple === 'aarch64-apple-darwin') return 'arm64'
  if (targetTriple === 'x86_64-apple-darwin') return 'x86_64'
  if (targetTriple.endsWith('-apple-darwin')) {
    throw new Error(`[build-sidecars] unsupported macOS cu-helper target: ${targetTriple}`)
  }
  return null
}

export function createCuHelperBuildEnv(
  targetTriple: string,
  inheritedEnv: NodeJS.ProcessEnv,
): NodeJS.ProcessEnv {
  const arch = resolveCuHelperArch(targetTriple)
  if (!arch) {
    throw new Error(`[build-sidecars] cannot build cu-helper for non-macOS target: ${targetTriple}`)
  }
  const env: NodeJS.ProcessEnv = {
    ...inheritedEnv,
    CU_HELPER_ARCH: arch,
  }
  // A helper-only override left in the developer's shell must never split the
  // helper from the host/sidecar certificate selected for this build.
  if (env.CC_HAHA_SIGN_IDENTITY) {
    env.CU_HELPER_IDENTITY = env.CC_HAHA_SIGN_IDENTITY
  } else {
    delete env.CU_HELPER_IDENTITY
  }
  return env
}

/** Explicit non-CU builds must not package an older generated helper by accident. */
export async function removeStagedCuHelperArtifacts(binariesDir: string) {
  for (const name of [
    'cc-haha-computer-use.app', 'cc-haha-computer-use', 'cu-helper',
    'cu-helper_cc-haha-computer-use.bundle', 'cu-helper_cu-helper.bundle',
  ]) {
    await rm(path.join(binariesDir, name), { recursive: true, force: true })
  }
}
