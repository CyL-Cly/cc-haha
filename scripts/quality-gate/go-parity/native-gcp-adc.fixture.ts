import { createRequire } from 'node:module'
import { realpath, writeFile, mkdir, readFile } from 'node:fs/promises'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const home = process.env.HOME
if (!home || await realpath(process.cwd()) !== await realpath(home) || process.env.CC_HAHA_AWS_FIXTURE !== '1') throw new Error('private ADC fixture root required')
const require = createRequire(join(dirname(fileURLToPath(import.meta.url)), '../../../package.json'))
const { GoogleAuth } = require('google-auth-library')
const { Gaxios } = require('gaxios')
const input = await Bun.stdin.json()
const rows = []
const OriginalDate = globalThis.Date
for (const row of input.rows) {
  if (row.now !== undefined) {
    globalThis.Date = class extends OriginalDate { constructor(...args) { super(...(args.length ? args : [row.now])) } static now() { return row.now } } as typeof Date
  } else globalThis.Date = OriginalDate
  const dir = join(home, `adc-${rows.length}`)
  await mkdir(dir)
  const credentialPath = join(dir, 'credential.json')
  await writeFile(credentialPath, JSON.stringify(row.credentials))
  for (const key of Object.keys(process.env)) if (key.startsWith('GOOGLE_') || key.startsWith('GCLOUD_') || key === 'google_application_credentials' || key === 'google_cloud_project') delete process.env[key]
  Object.assign(process.env, { GOOGLE_CLOUD_PROJECT: 'PRIVATE_PROJECT', GOOGLE_APPLICATION_CREDENTIALS: credentialPath }, row.env)
  const calls = []
  // The generic network boundary is replaced, while installed GoogleAuth ADC
  // file selection, concrete AuthClient, token/cache/header logic remain real.
  const transporter = new Gaxios()
  transporter.request = async (options) => {
    let data = options.data
    if (data instanceof URLSearchParams) data = data.toString()
    calls.push({ method: options.method, url: String(options.url), data })
    if (row.failure) throw new Error(row.failure)
    return { data: { access_token: 'GOOGLE_ACCESS_PRIVATE', token_type: 'Bearer', expires_in: 3600, ...row.response } }
  }
  const auth = new GoogleAuth({ scopes: ['https://www.googleapis.com/auth/cloud-platform'], clientOptions: { transporter } })
  try {
    const client = await auth.getClient()
    const observations = []
    for (let i = 0; i < (row.count ?? 2); i++) observations.push(Object.fromEntries((await client.getRequestHeaders()).entries()))
    rows.push({ value: { client: client.constructor.name, projectId: await auth.getProjectId(), clientProjectId: client.projectId ?? null, headers: observations, calls }, persisted: JSON.parse(await readFile(credentialPath, 'utf8')) })
  } catch (error) { rows.push({ error: error.message, calls }) }
}
globalThis.Date = OriginalDate
console.log(JSON.stringify({ rows }))
