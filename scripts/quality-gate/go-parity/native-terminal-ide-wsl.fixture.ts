import { assertDisposableFixtureHome } from './fixture-guard'
import { WindowsToWSLConverter } from '../../../src/utils/idePathConversion'

const input = await Bun.stdin.json()
assertDisposableFixtureHome(input.home, 'Original IDE Windows/WSL physical path conversion')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_TERMINAL_IDE_WSL_SOURCE !== '1') {
  throw new Error('Missing IDE WSL Source guard')
}
const converter = new WindowsToWSLConverter('owned-private-distro')
const values = input.paths.map((value: string) => converter.toIDEPath(value))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify(values), error => error ? reject(error) : resolve()))
