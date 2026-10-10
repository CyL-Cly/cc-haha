import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'TUI tool presentation')
if (process.env.CC_HAHA_TUI_PRESENTATION_FIXTURE !== '1') throw new Error('Unowned TUI presentation fixture')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../../preload')
const { renderToolResultMessage } = await import('../../../../src/tools/FileReadTool/UI')
const { FileReadTool } = await import('../../../../src/tools/FileReadTool/FileReadTool')
const { stripSystemReminders } = await import('../../../../src/components/messageActions')
const { FallbackToolUseErrorMessage } = await import('../../../../src/components/FallbackToolUseErrorMessage')
const { PromptInputFooterSuggestions } = await import('../../../../src/components/PromptInput/PromptInputFooterSuggestions')
const { TerminalSizeContext } = await import('../../../../src/ink/components/TerminalSizeContext')
const React = await import('react')
// Supply only the framework hook boundary; original component code computes
// every error, truncation and suggestion window. No TS production fallback.
const internals = (React as any).__CLIENT_INTERNALS_DO_NOT_USE_OR_WARN_USERS_THEY_CANNOT_UPGRADE
const priorDispatcher = internals.H
internals.H = {
 useMemoCache: (size: number) => Array(size).fill(Symbol.for('react.memo_cache_sentinel')),
 useContext: (context: any) => context === TerminalSizeContext ? { rows: input.height ?? 30, columns: 120 } : null,
 useRef: (value: any) => ({ current: value }),
 useEffect: () => {}
}
const textOf = (value: any): string => {
 if (value === null || value === undefined || typeof value === 'boolean') return ''
 if (typeof value === 'string' || typeof value === 'number') return String(value)
 if (Array.isArray(value)) return value.map(textOf).join('')
 return textOf(value.props?.children)
}
const rows = input.reads.map((value: any) => {
 const parsed = FileReadTool.outputSchema.safeParse(value)
 return parsed.success ? textOf(renderToolResultMessage(parsed.data)) : null
})
const reminders = input.reminders.map((value: string) => stripSystemReminders(value))
const errors = (input.errors ?? []).map((value: any) => {
 const element = FallbackToolUseErrorMessage({ result: value.result, verbose: value.verbose }) as any
 return element.props.children.props.children.map(textOf).filter(Boolean).join('\n')
})
const windows = (input.windows ?? []).map((value: any) => {
 const element = PromptInputFooterSuggestions({ suggestions: value.suggestions, selectedSuggestion: value.selected, overlay: value.overlay }) as any
 return element ? element.props.children.map((row: any) => ({ name: row.props.item.id, selected: row.props.isSelected })) : []
})
internals.H = priorDispatcher
if (capabilities.blocked.length) throw new Error('Blocked TUI presentation capability: ' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({ rows, reminders, errors, windows, capabilities: { blocked: capabilities.blocked } }) + '\n')
