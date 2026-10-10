import { assertDisposableFixtureHome } from './fixture-guard'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Native body socket')
if (process.env.CC_HAHA_NATIVE_BODY_SOCKET_FIXTURE !== '1' || !/^http:\/\/127\.0\.0\.1:[0-9]+$/.test(input.origin)) throw new Error('Native socket requires explicit private loopback')
const response = await fetch(input.origin)
const reader = response.body!.getReader()
let bytes = 0
let error: unknown
try {
  for (;;) {
    const next = await reader.read()
    if (next.done) break
    bytes += next.value.byteLength
  }
} catch (failure) { error = failure }
const fault = error as { name?: string, message?: string, code?: string } | undefined
process.stdout.write(JSON.stringify({ bytes, status: response.status, fault: fault && { name: fault.name, message: fault.message, code: fault.code } }))
