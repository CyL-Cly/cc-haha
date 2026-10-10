import path from 'node:path'
import { rm } from 'node:fs/promises'
import { sidecarBinaryName } from '../electron/services/sidecarBackend'

export type GoSidecarBuildPlan = {
  command: string
  args: string[]
  cwd: string
  env: NodeJS.ProcessEnv
  outputPath: string
}

const legacyBusinessTriples = [
  'aarch64-apple-darwin', 'x86_64-apple-darwin',
  'x86_64-pc-windows-msvc', 'aarch64-pc-windows-msvc',
  'x86_64-unknown-linux-gnu', 'aarch64-unknown-linux-gnu',
  'x86_64-unknown-linux-musl', 'aarch64-unknown-linux-musl',
]

/** Old generated targets must not be picked up by Electron's binary glob. */
export async function removeLegacyBusinessSidecars(binariesDir: string): Promise<void> {
  for (const triple of legacyBusinessTriples) {
    const extension = triple.includes('windows') ? '.exe' : ''
    await rm(path.join(binariesDir, `claude-sidecar-${triple}${extension}`), { force: true })
  }
}

export function mapTargetTripleToGo(triple: string): { goos: string; goarch: string } {
  switch (triple) {
    case 'aarch64-apple-darwin': return { goos: 'darwin', goarch: 'arm64' }
    case 'x86_64-apple-darwin': return { goos: 'darwin', goarch: 'amd64' }
    case 'x86_64-pc-windows-msvc': return { goos: 'windows', goarch: 'amd64' }
    case 'aarch64-pc-windows-msvc': return { goos: 'windows', goarch: 'arm64' }
    case 'x86_64-unknown-linux-gnu':
    case 'x86_64-unknown-linux-musl': return { goos: 'linux', goarch: 'amd64' }
    case 'aarch64-unknown-linux-gnu':
    case 'aarch64-unknown-linux-musl': return { goos: 'linux', goarch: 'arm64' }
    default: throw new Error(`[build-sidecars] Unsupported Go target triple: ${triple}`)
  }
}

/** The same main package owns desktop server, SDK children, and terminal CLI. */
export function createGoSidecarBuildPlan(options: {
  repoRoot: string
  binariesDir: string
  triple: string
  version: string
  telemetryDir: string
  env: NodeJS.ProcessEnv
}): GoSidecarBuildPlan {
  const target = mapTargetTripleToGo(options.triple)
  if (!/^[0-9A-Za-z][0-9A-Za-z.+_-]*$/.test(options.version)) {
    throw new Error('[build-sidecars] Go sidecar version must be a nonempty version identifier')
  }
  const outputPath = path.join(options.binariesDir, sidecarBinaryName(options.triple))
  return {
    command: options.env.CC_HAHA_GO_EXECUTABLE || 'go',
    args: ['build', '-trimpath', '-ldflags', `-X main.version=${options.version}`, '-o', outputPath, './cmd/claude-haha'],
    cwd: path.join(options.repoRoot, 'backend'),
    env: {
      ...options.env,
      GOOS: target.goos,
      GOARCH: target.goarch,
      GOAMD64: 'v1',
      GOARM64: 'v8.0',
      CGO_ENABLED: '0',
      GOENV: 'off',
      GOTOOLCHAIN: 'local',
      GOFLAGS: '-mod=readonly',
      GOTELEMETRY: 'off',
      TEST_TELEMETRY_DIR: options.telemetryDir,
    },
    outputPath,
  }
}
