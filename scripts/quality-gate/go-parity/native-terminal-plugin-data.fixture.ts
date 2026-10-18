import fs from 'node:fs/promises'
import path from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'

const input = await Bun.stdin.json()
assertDisposableFixtureHome(input.home, 'Original on-uninstall persistent plugin data size')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_PLUGIN_DATA_SOURCE !== '1') throw new Error('Missing data Source guard')
const { getPluginDataDirSize, pluginDataDirPath } = await import('../../../src/utils/plugins/pluginDirectories')
const directory = pluginDataDirPath('sample@mkt')
if (!directory.startsWith(input.home + path.sep)) throw new Error('Source data path escaped fixture')
if (input.profile !== 'absent') await fs.mkdir(directory, { recursive: true })
if (input.profile === 'nested' || input.profile === 'symlinks') {
  await fs.mkdir(path.join(directory, 'nested'))
  await fs.writeFile(path.join(directory, 'nested', 'data'), 'x'.repeat(2049))
  await fs.writeFile(path.join(directory, 'small'), 'abc')
}
if (input.profile === 'symlinks') {
  await fs.symlink(path.join(directory, 'nested', 'data'), path.join(directory, 'file-link'))
  await fs.symlink(path.join(directory, 'nested'), path.join(directory, 'directory-link'))
  await fs.symlink(path.join(directory, 'missing'), path.join(directory, 'broken'))
}
if (input.profile === 'not-directory') { await fs.rm(directory, { recursive: true }); await fs.writeFile(directory, 'not a directory') }
if (input.profile === 'cycle') { await fs.symlink('self', path.join(directory, 'self')); await fs.writeFile(path.join(directory, 'keep'), 'keep despite broken entry') }
const size = await getPluginDataDirSize('sample@mkt')
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ directory, size }), error => error ? reject(error) : resolve()))
