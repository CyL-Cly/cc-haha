import { createRequire } from 'node:module'
import { realpath, writeFile, mkdir } from 'node:fs/promises'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
const home = process.env.HOME
if (!home || await realpath(process.cwd()) !== await realpath(home) || process.env.CC_HAHA_AWS_FIXTURE !== '1') throw new Error('private INI cache fixture root required')
const require = createRequire(join(dirname(fileURLToPath(import.meta.url)), '../../../package.json'))
const { fromIni } = require('@aws-sdk/credential-provider-ini')
const rows = []
const input = await Bun.stdin.json()
for (const row of input.rows) {
  const dir = join(home, `ini-cache-${rows.length}`)
  await mkdir(dir)
  const config = join(dir, 'config')
  const filepath = join(dir, 'credentials')
  await writeFile(filepath, '')
  const observations = []
  for (const step of row.steps) {
    if (step.write !== undefined) await writeFile(config, step.write)
    else {
      try {
        const value = await fromIni({ profile: 'selected', configFilepath: config, filepath, ignoreCache: step.force })()
        delete value.$source
        observations.push({ value })
      } catch (error) { observations.push({ error: error.message }) }
    }
  }
  rows.push({ value: observations })
}
console.log(JSON.stringify({ rows }))
