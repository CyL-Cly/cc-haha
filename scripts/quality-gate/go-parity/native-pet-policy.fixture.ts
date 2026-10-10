import { assertDisposableFixtureHome } from './fixture-guard'
const input = await Bun.stdin.json()
assertDisposableFixtureHome(input.homeDir, 'Native Pet Policy')
if (process.env.CC_HAHA_NATIVE_PET_POLICY_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Unowned pet policy fixture')
const policy = await import('../../../src/server/petAccessPolicy.js')
process.stdout.write(JSON.stringify({
  http: input.http.map((r: any) => {
    const url = new URL(r.url)
    const request = new Request(url, { method: r.method, headers: r.headers })
    return { allowed: policy.isPetHttpRequestAllowed(request, url), sessionId: policy.getPetScopedSessionId(url.pathname) }
  }),
  client: input.client.map(policy.isPetClientMessageAllowed),
  server: input.server.map(policy.toPetServerMessage),
}) + '\n')
