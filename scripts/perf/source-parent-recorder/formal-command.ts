// Exact original parent benchmark process/pipe lifecycle; no runtime edits.
export async function command(command: string[], cwd: string, env: Record<string, string>, timeout: number, input?: string) {
  const started = performance.now()
  // The pinned Bun setsid() contract makes this PID the owned POSIX PGID.
  // Never signal the benchmark's own group or retain a PID for a later task.
  const child = Bun.spawn(command, { cwd, env, detached: true, stdin: input === undefined ? 'ignore' : new TextEncoder().encode(input), stdout: 'pipe', stderr: 'pipe' })
  const stdoutReader = child.stdout.getReader(), stderrReader = child.stderr.getReader()
  const recordingErrors: string[] = []
  let timedOut = false, stopping = false, retiredGroup = false, joined = false, closing = false
  let stdoutComplete = false, stderrComplete = false, forciblyClosed = false
  const closures: Promise<unknown>[] = []
  let force: ReturnType<typeof setTimeout> | undefined, drain: ReturnType<typeof setTimeout> | undefined
  const signalOwned = (signal: 'SIGTERM' | 'SIGKILL') => {
    if (retiredGroup) return
    try {
      if (process.platform === 'win32') child.kill(signal)
      else process.kill(-child.pid, signal)
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code === 'ESRCH') retiredGroup = true
      else recordingErrors.push('Owned process cleanup failed: ' + String(error))
    }
  }
  const cancelReaders = () => {
    if (!stdoutComplete || !stderrComplete) forciblyClosed = true
    if (!stdoutComplete) closures.push(stdoutReader.cancel().catch(error => { recordingErrors.push('stdout close failed: ' + String(error)) }))
    if (!stderrComplete) closures.push(stderrReader.cancel().catch(error => { recordingErrors.push('stderr close failed: ' + String(error)) }))
  }
  const stop = () => {
    if (stopping || closing) return
    stopping = true
    signalOwned('SIGTERM')
    force = setTimeout(() => {
      signalOwned('SIGKILL')
      // SIGKILL should close the actual owned group's inherited descriptors.
      // A pipe that still will not close is a cleanup/evidence failure, never
      // a successful task obtained merely by cancelling its reader.
      drain = setTimeout(() => {
        if (!stdoutComplete || !stderrComplete) {
          recordingErrors.push('Owned output pipes stayed open after process-group SIGKILL')
          cancelReaders()
        }
      }, 1_000)
    }, 5_000)
  }
  const read = async (reader: ReadableStreamDefaultReader<Uint8Array>, complete: () => void) => {
    const chunks: Uint8Array[] = []
    let length = 0
    try {
      while (true) {
        const next = await reader.read()
        if (next.done) { if (!forciblyClosed) complete(); break }
        const bytes = new Uint8Array(next.value)
        chunks.push(bytes); length += bytes.length
      }
    } catch (error) {
      recordingErrors.push('Command output read failed: ' + String(error))
      stop()
    } finally { reader.releaseLock() }
    const bytes = new Uint8Array(length)
    let offset = 0
    for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.length }
    return new TextDecoder().decode(bytes)
  }
  const timer = setTimeout(() => { timedOut = true; stop() }, timeout)
  const output = [read(stdoutReader, () => { stdoutComplete = true }), read(stderrReader, () => { stderrComplete = true })] as const
  try {
    const [stdout, stderr, code] = await Promise.all([...output, child.exited.then(code => { joined = true; return code })])
    await Promise.all(closures)
    const ended = performance.now()
    return { stdout, stderr, code, timedOut, startedMonotonicMs: started, endedMonotonicMs: ended, durationMs: ended - started, processGroup: process.platform === 'win32' ? null : child.pid, signal: child.signalCode, joined, stdoutComplete, stderrComplete, recordingErrors }
  } finally {
    closing = true
    // No delayed signal may outlive this command's joined/closed lifetime.
    clearTimeout(timer)
    if (force) clearTimeout(force)
    if (drain) clearTimeout(drain)
    if (!joined) {
      signalOwned('SIGKILL')
      cancelReaders()
      await Promise.allSettled([...output, ...closures, child.exited])
    }
  }
}
