import { APIError } from '@anthropic-ai/sdk'
import { assertDisposableFixtureHome } from './fixture-guard'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.home, 'SDK error metadata')
if (process.env.CC_HAHA_SDK_ERROR_METADATA_FIXTURE !== '1') throw new Error('Unowned SDK metadata fixture')
const version = (await import('@anthropic-ai/sdk/package.json')).default.version
if (version !== '0.128.0') throw new Error(`Wrong SDK ${version}`)
const results = input.cases.map((row: any) => {
  const headers = new Headers(row.headers)
  const body = JSON.parse(row.body)
  const error = row.http
    ? APIError.generate(row.status, body, undefined, headers)
    : new APIError(undefined, body, undefined, headers, body?.error?.type)
  // IDs are cached by the real constructor; headers remain live references.
  headers.set('anthropic-workspace-id', 'after-construction')
  return JSON.parse(JSON.stringify(error))
})
process.stdout.write(JSON.stringify({ version, results }))
