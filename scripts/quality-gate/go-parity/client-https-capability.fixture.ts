import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as { homeDir: string; origin: string; destination: string; mode: string }
assertDisposableFixtureHome(input.homeDir, 'Client HTTPS capability')
if (process.env.CC_HAHA_CLIENT_HTTPS_CAPABILITY !== '1') throw new Error('Explicit HTTPS capability fixture required')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { loopbackFetchOrigin: input.origin })
if (input.mode === 'filesystem') readFileSync('/etc/hosts')
if (input.mode === 'native') Bun.spawn(['security'])
const response = await fetch(input.destination, { tls: { ca: readFileSync(join(input.homeDir, 'certificate.pem'), 'utf8') } } as RequestInit)
process.stdout.write(JSON.stringify({ status: response.status, body: await response.text(), capabilities }))
