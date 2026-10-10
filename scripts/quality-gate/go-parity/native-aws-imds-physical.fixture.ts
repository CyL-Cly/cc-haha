import { createRequire } from 'node:module'
import { realpath } from 'node:fs/promises'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
const home = process.env.HOME
if (!home || await realpath(process.cwd()) !== await realpath(home) || process.env.CC_HAHA_AWS_FIXTURE !== '1') throw new Error('private IMDS physical fixture root required')
const input = await Bun.stdin.json()
const endpoint = new URL(input.url)
if (endpoint.hostname !== '127.0.0.1') throw new Error('loopback only')
Object.assign(process.env, { AWS_EC2_METADATA_SERVICE_ENDPOINT: endpoint.href, AWS_CONFIG_FILE: join(home, 'absent-config'), AWS_SHARED_CREDENTIALS_FILE: join(home, 'absent-credentials') })
const require = createRequire(join(dirname(fileURLToPath(import.meta.url)), '../../../package.json'))
const { fromInstanceMetadata } = require('@smithy/credential-provider-imds')
try { console.log(JSON.stringify({ value: await fromInstanceMetadata()() })) }
catch (error) { console.log(JSON.stringify({ error: error.message })) }
