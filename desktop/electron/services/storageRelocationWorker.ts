import { spawn, type ChildProcessWithoutNullStreams } from 'node:child_process'
import { assertGoSidecarEnvironment } from './sidecarBackend'
import { resolveSidecarExecutable } from './sidecarManager'

export type StorageRelocationRequest = { sourceDir: string; targetDir: string; stagedDir: string }

/** One isolated Go process edits the private staging copy after quiescence. */
export function runStorageRelocationWorker(options: {
  desktopRoot: string
  request: StorageRelocationRequest
  env: NodeJS.ProcessEnv
  signal: AbortSignal
  spawnFn?: (command: string, args: string[], env: NodeJS.ProcessEnv) => ChildProcessWithoutNullStreams
}): Promise<void> {
  assertGoSidecarEnvironment(options.env)
  options.signal.throwIfAborted()
  return new Promise((resolve, reject) => {
    const spawnWorker = options.spawnFn ?? ((command, args, env) => spawn(command, args, { env, stdio: ['pipe', 'pipe', 'pipe'], windowsHide: true }))
    const child = spawnWorker(resolveSidecarExecutable(options.desktopRoot), ['--storage-relocation-worker'], options.env)
    let stdout = ''
    let stderr = ''
    let failure: Error | null = null
    let forceTimer: ReturnType<typeof setTimeout> | undefined
    const stop = () => {
      child.kill('SIGTERM')
      forceTimer ??= setTimeout(() => child.kill('SIGKILL'), 2_000)
    }
    const abort = () => {
      failure = options.signal.reason instanceof Error ? options.signal.reason : new Error('Storage relocation cancelled')
      stop()
    }
    options.signal.addEventListener('abort', abort, { once: true })
    child.on('error', error => { failure ??= error })
    child.stdin.on('error', error => { failure ??= error; stop() })
    child.stdout.on('data', chunk => {
      stdout += chunk.toString('utf8')
      if (stdout.length > 64 * 1024) { failure ??= new Error('Invalid storage relocation response'); stop() }
    })
    child.stderr.on('data', chunk => { stderr = (stderr + chunk.toString('utf8')).slice(-64 * 1024) })
    child.once('close', code => {
      options.signal.removeEventListener('abort', abort)
      if (forceTimer) clearTimeout(forceTimer)
      if (failure) { reject(failure); return }
      if (code !== 0) { reject(new Error(stderr.trim() || `Storage relocation worker exited with ${code}`)); return }
      try {
        const response: unknown = JSON.parse(stdout)
        if (!response || typeof response !== 'object' || (response as { relocated?: unknown }).relocated !== true) throw new Error('Go worker did not confirm managed storage relocation')
        resolve()
      } catch (error) { reject(error) }
    })
    child.stdin.end(`${JSON.stringify(options.request)}\n`)
  })
}
