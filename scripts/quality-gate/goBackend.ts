import { mkdirSync, mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { createGoBuildEnvironment } from '../pr/go-dependencies'
import { BACKEND_TARGET_ENV, resolveBackendLaunchTarget, type BackendLaunchTarget } from './backendLaunch'

/** The Agent lane exercises the native production runtime, including its real tools. */
export async function prepareGoBackend(rootDir: string, artifactDir: string, source: NodeJS.ProcessEnv = process.env) {
  if (source[BACKEND_TARGET_ENV] !== undefined) {
    const target = resolveBackendLaunchTarget(rootDir, undefined, source)
    if (target.kind !== 'go') throw new Error('The Go Agent E2E lane requires a Go backend target')
    return { target, cleanup() {} }
  }
  const home = mkdtempSync(join(tmpdir(), 'go-agent-lane-build-'))
  const outputDir = resolve(artifactDir, 'native')
  mkdirSync(outputDir, { recursive: true })
  const command = join(outputDir, process.platform === 'win32' ? 'claude-haha.exe' : 'claude-haha')
  try {
    const env = await createGoBuildEnvironment(rootDir, home, source)
    const child = Bun.spawn(['go', 'build', '-trimpath', '-o', command, './cmd/claude-haha'], {
      cwd: join(rootDir, 'backend'), env, stdin: 'ignore', stdout: 'pipe', stderr: 'pipe',
    })
    const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()])
    if (code !== 0) throw new Error(`Go Agent E2E build failed (${code}): ${stderr}`)
    const target: BackendLaunchTarget = { kind: 'go', command, args: ['server'], cwd: rootDir }
    return { target, cleanup() { rmSync(home, { recursive: true, force: true }) } }
  } catch (error) {
    rmSync(home, { recursive: true, force: true })
    throw error
  }
}
