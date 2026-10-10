// Run only as a child with the harness's disposable HOME and config paths.
import { readFileSync } from 'node:fs'
import { join } from 'node:path'

if (!process.env.CC_HAHA_ACCESS_FIXTURE_HOME || process.env.HOME !== process.env.CC_HAHA_ACCESS_FIXTURE_HOME
  || process.env.CLAUDE_CONFIG_DIR !== join(process.env.HOME, '.claude')) {
  throw new Error('Access fixture requires an explicitly isolated HOME and config directory')
}
const [corsModule, authModule, h5Module, localModule] = await Promise.all([
  import('../../../src/server/middleware/cors'),
  import('../../../src/server/middleware/auth'),
  import('../../../src/server/services/h5AccessService'),
  import('../../../src/server/localAccessAuth'),
])
const { resolveCors } = corsModule
const { requireAuth, requireH5Token, validateAuth, validateRequestAuth } = authModule
const { H5AccessService } = h5Module
const { hasConfiguredLocalAccessToken, hasConfiguredPetAccessToken, isLocalAccessAuthorized, isPetAccessAuthorized } = localModule

const fixtures = JSON.parse(readFileSync(0, 'utf8'))
const results = []
for (const fixture of fixtures) {
  process.env.ANTHROPIC_API_KEY = fixture.apiKey
  process.env.CC_HAHA_LOCAL_ACCESS_TOKEN = fixture.localToken
  process.env.CC_HAHA_PET_ACCESS_TOKEN = fixture.petToken
  const h5Calls: string[] = []
  H5AccessService.prototype.validateToken = async token => {
    h5Calls.push(token)
    return fixture.validH5Tokens.includes(token)
  }
  const req = new Request('http://127.0.0.1:3456/api/status', { headers: fixture.headers })
  const request = await validateRequestAuth(req, fixture.override)
  const h5Response = await requireH5Token(req, fixture.override)
  const h5 = h5Response ? { valid: false, error: (await h5Response.clone().json()).message } : { valid: true }
  const h5Rejection = h5Response ? { status: h5Response.status, body: await h5Response.json() } : null
  const validationCallCount = h5Calls.length
  const requestResponse = await requireAuth(req, fixture.override)
  // requireAuth delegates to validateRequestAuth; count that call separately
  // from the two validations whose order the Go fixture records.
  h5Calls.length = validationCallCount
  const originCalls: string[] = []
  const cors = await resolveCors(fixture.origin, null, {
    h5Enabled: fixture.h5Enabled,
    isOriginAllowed: async origin => { originCalls.push(origin); return fixture.allowOrigin },
  })
  results.push({
    hasLocal: hasConfiguredLocalAccessToken(), hasPet: hasConfiguredPetAccessToken(),
    local: isLocalAccessAuthorized(req, fixture.override), pet: isPetAccessAuthorized(req, fixture.override),
    apiKey: validateAuth(req), request,
    requestRejection: requestResponse ? { status: requestResponse.status, body: await requestResponse.json() } : null,
    h5, h5Rejection, h5Calls, cors, originCalls,
  })
}
process.stdout.write(JSON.stringify(results))
