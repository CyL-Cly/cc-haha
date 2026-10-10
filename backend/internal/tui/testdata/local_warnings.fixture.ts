import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'Local warning presentation')
if (process.env.CC_HAHA_LOCAL_WARNINGS_FIXTURE !== '1') throw new Error('Unowned local warnings fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../../preload')
const { mock } = await import('bun:test')
const React = await import('react')
mock.module('react/compiler-runtime', () => ({ c: (count: number) => Array(count).fill(Symbol.for('react.memo_cache_sentinel')) }))
mock.module('../../../../src/components/messageActions', () => ({ useSelectedMessageBg: () => undefined, InVirtualListContext: React.createContext(false), MessageActionsSelectedContext: React.createContext(false) }))
mock.module('../../../../src/components/MessageResponse', () => ({ MessageResponse: (props: unknown) => props }))
const { SystemTextMessage } = await import('../../../../src/components/messages/SystemTextMessage')
const visible = []
for (const record of input.records) {
  const rendered = SystemTextMessage({ message: record, addMargin: false, verbose: false })
  if (!rendered) continue
  const child = rendered.props.children
  if (!child || child.type.name !== 'SystemTextMessageInner') throw new Error('Unimplemented original warning child')
  visible.push({ content: child.props.content, dot: child.props.dot, color: child.props.color, dimColor: child.props.dimColor })
}
if (capabilities.blocked.length) throw new Error('Blocked original warning render: ' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({ visible }) + '\n')
