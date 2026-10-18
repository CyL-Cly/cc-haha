import fs from 'node:fs'
import * as fsp from 'node:fs/promises'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual native filesystem guard return graph')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TRANSCRIPT_FS_GUARD !== '1')
  throw new Error('Pinned owned filesystem guard fixture required')
const file = join(home, 'original.txt')
fs.writeFileSync(file, 'original private bytes')
const closeSync = fs.closeSync
type State = {
  method: string
  native?: Promise<any>
  nativeValue?: unknown
  nativeError?: unknown
  consumed: boolean
  events: Array<{ phase: string, consumed: boolean }>
}
let active: State | undefined
function observe(state: State, promise: Promise<any>) {
  if (state.native) throw new Error('More than one original primitive in a case')
  state.native = promise
  const ready = () => {
    state.events.push({ phase: 'native-ready', consumed: state.consumed })
    queueMicrotask(() => {
      state.events.push({ phase: 'after-native-reaction', consumed: state.consumed })
      queueMicrotask(() => state.events.push({ phase: 'after-guard-reaction', consumed: state.consumed }))
    })
  }
  void promise.then(value => { state.nativeValue = value; ready() }, error => { state.nativeError = error; ready() })
  return promise
}
// Capture genuine native functions before the real capability guard installs
// its spies. These synchronous observers return the SAME original Promise.
for (const name of ['mkdir', 'open', 'stat', 'readFile'] as const) {
  const original = (fs.promises as any)[name]
  ;(fs.promises as any)[name] = (...args: any[]) => {
    const promise = original(...args)
    if (name === 'open') {
      void promise.then((handle: any) => {
        for (const method of ['close', 'stat', 'appendFile', 'read']) {
          const native = handle[method].bind(handle)
          handle[method] = (...values: any[]) => {
            const returned = native(...values)
            if (active?.method === 'handle.' + method) observe(active, returned)
            return returned
          }
        }
      }, () => {})
    }
    if (active?.method === name) observe(active, promise)
    return promise
  }
}
const capabilities = await installClientFixtureCapabilities(home)
const results: unknown[] = []
for (const method of ['mkdir', 'open', 'stat', 'readFile', 'handle.close', 'handle.stat', 'handle.appendFile', 'handle.read']) {
  for (const failure of [false, true]) {
    let handle: Awaited<ReturnType<typeof fsp.open>> | undefined
    if (method.startsWith('handle.')) {
      handle = await fsp.open(file, 'r+')
      // A real rejection from an exclusively owned native descriptor. No
      // synthetic rejected Promise replaces the guard or underlying primitive.
      if (failure) closeSync(handle.fd)
    }
    const state: State = { method, consumed: false, events: [] }
    active = state
    let returned: Promise<any>
    switch (method) {
      case 'mkdir': returned = fsp.mkdir(failure ? file : join(home, 'new-directory')); break
      case 'open': returned = fsp.open(failure ? join(home, 'absent-parent', 'absent') : file, 'r'); break
      case 'stat': returned = fsp.stat(failure ? join(home, 'absent') : file); break
      case 'readFile': returned = fsp.readFile(failure ? join(home, 'absent') : file, 'utf8'); break
      case 'handle.close': returned = handle!.close(); break
      case 'handle.stat': returned = handle!.stat(); break
      case 'handle.appendFile': returned = handle!.appendFile('x'); break
      default: returned = handle!.read(Buffer.alloc(1), 0, 1, 0)
    }
    const distinctReturn = returned !== state.native
    let errorSame = false, valueSame = false, code: string | undefined
    try {
      const value = await returned
      valueSame = value === state.nativeValue
      if (method === 'open') handle = value
    } catch (error) {
      errorSame = error === state.nativeError
      code = (error as NodeJS.ErrnoException).code
    }
    state.consumed = true
    state.events.push({ phase: 'consumer', consumed: true })
    await Promise.resolve()
    active = undefined
    if (handle && method !== 'handle.close') {
      try { await handle.close() } catch {}
    }
    if (!state.native || (failure ? !errorSame : !valueSame))
      throw new Error('Actual native value/error was replaced: ' + method)
    results.push({ method, failure, distinctReturn, errorSame, valueSame, code, events: state.events })
  }
}
if (capabilities.blocked.length) throw new Error('Filesystem guard escaped its private fixture')
process.stdout.write(JSON.stringify({ results, blocked: capabilities.blocked, physical: true }))
