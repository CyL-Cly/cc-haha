import '@testing-library/jest-dom/vitest'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { sessionCollaborationApi, type SessionDelegationSnapshot } from '@/api/sessionCollaboration'
import { useSettingsStore } from '@/stores/settingsStore'
import { SessionDelegationCard } from './SessionDelegationCard'
import type { RuntimeSelection } from '@/types/runtime'

vi.mock('@/api/sessionCollaboration', () => ({ sessionCollaborationApi: { get: vi.fn(), update: vi.fn(), act: vi.fn(), change: vi.fn(), cancelChange: vi.fn() } }))
vi.mock('@/lib/sessionNavigation', () => ({ openSessionSource: vi.fn() }))
vi.mock('@/components/controls/ModelSelector', () => ({ ModelSelector: (props: { ariaLabel: string; runtimeSelection: RuntimeSelection; runtimeChoices: unknown[]; onRuntimeSelectionChange: (selection: RuntimeSelection) => void; disabled?: boolean }) => <button aria-label={props.ariaLabel} disabled={props.disabled} data-catalog={JSON.stringify(props.runtimeChoices)} onClick={() => props.onRuntimeSelectionChange({ providerId: 'economy', modelId: 'same' })}>{props.runtimeSelection?.providerId} / {props.runtimeSelection?.modelId}</button> }))

function fixture(): SessionDelegationSnapshot {
  return {
    batches: [{ id: 'batch', requestId: 'request', callerSessionId: 'parent', revision: 1, state: 'review', sessions: [
      { id: 'build', title: 'Builder', prompt: 'Build the feature', runtime: { providerId: 'premium', modelId: 'same' } },
      { id: 'review', title: 'Reviewer', prompt: 'Review the change', runtime: { providerId: 'premium', modelId: 'same' } },
    ] }],
    models: ['premium', 'economy'].map(providerId => ({ providerId, providerName: providerId, modelId: 'same', modelName: 'Same', supportedEfforts: [], isDefault: providerId === 'premium' })),
    changes: [],
  }
}
beforeEach(() => {
  vi.clearAllMocks()
  useSettingsStore.setState({ locale: 'en' })
  vi.mocked(sessionCollaborationApi.get).mockResolvedValue(fixture())
})

describe('SessionDelegationCard', () => {
  it('does not launch before confirmation, edits all selected members and confirms the saved revision', async () => {
    vi.mocked(sessionCollaborationApi.update).mockResolvedValue({ ...fixture().batches[0]!, revision: 2 })
    render(<SessionDelegationCard sessionId="parent" />)
    await screen.findByText('Session assignments')
    expect(sessionCollaborationApi.act).not.toHaveBeenCalled()
    fireEvent.click(screen.getByLabelText('Select all'))
    fireEvent.click(screen.getByRole('button', { name: 'Model for selected sessions' }))
    fireEvent.click(screen.getByRole('button', { name: 'Start sessions' }))
    await waitFor(() => expect(sessionCollaborationApi.act).toHaveBeenCalledWith('parent', expect.objectContaining({ revision: 2 }), 'confirm'))
    expect(sessionCollaborationApi.update).toHaveBeenCalledWith('parent', expect.objectContaining({ revision: 1 }), [
      { id: 'build', runtime: { providerId: 'economy', modelId: 'same' } },
      { id: 'review', runtime: { providerId: 'economy', modelId: 'same' } },
    ])
    const catalog = JSON.parse(screen.getByRole('button', { name: 'Builder · Provider and model' }).getAttribute('data-catalog')!) as Array<{ isDefault: boolean }>
    expect(catalog).toHaveLength(2)
    // Only the globally active provider receives the default marker.
    expect(catalog.map(choice => choice.isDefault)).toEqual([true, false])
  })
  it('blocks unresolved models and can cancel without starting', async () => {
    const data = fixture()
    data.batches[0]!.sessions[0]!.runtime = { providerId: 'deleted', modelId: 'same' }
    vi.mocked(sessionCollaborationApi.get).mockResolvedValue(data)
    render(<SessionDelegationCard sessionId="parent" />)
    expect(await screen.findByRole('button', { name: 'Start sessions' })).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: 'Cancel assignment' }))
    await waitFor(() => expect(sessionCollaborationApi.act).toHaveBeenCalledWith('parent', data.batches[0], 'cancel'))
  })
  it('uses actual runtime after reload and sends immediate pause-only changes without altering main runtime', async () => {
    const data = fixture()
    data.batches[0]!.state = 'launched'
    data.batches[0]!.sessions.forEach(entry => { entry.sessionId = entry.id })
    data.sessions = [{ sessionId: 'build', runtime: { providerId: 'economy', modelId: 'same' }, state: 'running', paused: false }]
    vi.mocked(sessionCollaborationApi.get).mockResolvedValue(data)
    render(<SessionDelegationCard sessionId="parent" />)
    expect(await screen.findByRole('button', { name: 'Builder · Provider and model' })).toHaveTextContent('economy / same')
    fireEvent.click(screen.getByLabelText('Builder'))
    fireEvent.change(screen.getByLabelText('When to change'), { target: { value: 'immediate' } })
    fireEvent.click(screen.getByLabelText('Continue after changing (uncheck to keep paused)'))
    fireEvent.click(screen.getByRole('button', { name: 'Apply to selected sessions' }))
    await waitFor(() => expect(sessionCollaborationApi.change).toHaveBeenCalledWith('parent', { requestId: expect.any(String), mode: 'immediate', resume: false, selections: [{ sessionId: 'build', runtime: { providerId: 'economy', modelId: 'same' } }] }))
  })
  it('retains a stable request ID after an uncertain change response', async () => {
    const data = fixture()
    data.batches[0]!.state = 'launched'
    data.batches[0]!.sessions[0]!.sessionId = 'child'
    vi.mocked(sessionCollaborationApi.get).mockResolvedValue(data)
    vi.mocked(sessionCollaborationApi.change).mockRejectedValueOnce(new Error('Connection interrupted')).mockResolvedValueOnce({} as never)
    render(<SessionDelegationCard sessionId="parent" />)
    fireEvent.click(await screen.findByLabelText('Builder'))
    fireEvent.click(screen.getByRole('button', { name: 'Apply to selected sessions' }))
    await screen.findByText('Connection interrupted')
    await waitFor(() => expect(screen.getByRole('button', { name: 'Apply to selected sessions' })).toBeEnabled())
    fireEvent.click(screen.getByRole('button', { name: 'Apply to selected sessions' }))
    await waitFor(() => expect(sessionCollaborationApi.change).toHaveBeenCalledTimes(2))
    expect(vi.mocked(sessionCollaborationApi.change).mock.calls[0]![1].requestId).toEqual(vi.mocked(sessionCollaborationApi.change).mock.calls[1]![1].requestId)
  })
  it('restores pending switches and cancels with their revision', async () => {
    const data = fixture()
    data.changes = [{ id: 'change', revision: 5, callerSessionId: 'parent', requestId: 'r', state: 'pending', mode: 'after_turn', resume: true, selections: [] }]
    vi.mocked(sessionCollaborationApi.get).mockResolvedValue(data)
    render(<SessionDelegationCard sessionId="parent" />)
    fireEvent.click(await screen.findByRole('button', { name: 'Cancel pending change' }))
    await waitFor(() => expect(sessionCollaborationApi.cancelChange).toHaveBeenCalledWith('parent', 'change', 5))
  })
})

it('does not silently overwrite newer server choices while a draft is open', async () => {
  let poll: (() => void) | undefined
  const originalInterval = window.setInterval.bind(window)
  const timer = vi.spyOn(window, 'setInterval').mockImplementation((callback, delay) => {
    if (delay === 2000) { poll = callback as () => void; return 98765 as unknown as ReturnType<typeof window.setInterval> }
    return originalInterval(callback, delay) as unknown as ReturnType<typeof window.setInterval>
  })
  try {
    render(<SessionDelegationCard sessionId="parent" />)
    fireEvent.click(await screen.findByRole('button', { name: 'Builder · Provider and model' }))
    const changed = fixture()
    changed.batches[0]!.revision = 3
    vi.mocked(sessionCollaborationApi.get).mockResolvedValue(changed)
    await act(async () => { poll?.(); await Promise.resolve() })
    expect(await screen.findByText('This assignment changed in another window. Reload before confirming.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Start sessions' })).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: 'Reload assignment' }))
    expect(screen.getByRole('button', { name: 'Builder · Provider and model' })).toHaveTextContent('premium / same')
    expect(screen.getByRole('button', { name: 'Start sessions' })).toBeEnabled()
  } finally { timer.mockRestore() }
})

it('keeps started sessions manageable when the remaining assignment is cancelled', async () => {
  const data = fixture()
  data.batches[0]!.state = 'cancelled'
  data.batches[0]!.sessions[0]!.sessionId = 'child'
  data.batches[0]!.sessions[0]!.launched = true
  vi.mocked(sessionCollaborationApi.get).mockResolvedValue(data)
  render(<SessionDelegationCard sessionId="parent" />)
  expect(await screen.findByLabelText('Builder')).toBeInTheDocument()
  expect(screen.queryByLabelText('Reviewer')).not.toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: 'Builder · Provider and model' }))
  expect(screen.getByRole('button', { name: 'Apply to selected sessions' })).toBeEnabled()
})

 it('uses container layout and reports partially selected rows without a second scroll surface', async () => {
  render(<SessionDelegationCard sessionId="parent" />)
  const card = await screen.findByRole('region', { name: 'Session assignments' })
  expect(card).toHaveClass('@container', 'min-w-0')
  expect(card).not.toHaveClass('overflow-y-auto')
  for (const row of screen.getAllByTestId('delegation-task-row')) {
    expect(row).toHaveClass('@min-[560px]:grid-cols-[minmax(0,1fr)_240px]')
  }
  fireEvent.click(screen.getByRole('checkbox', { name: 'Builder' }))
  expect(screen.getByRole('checkbox', { name: 'Select all' })).toBePartiallyChecked()
 })


it('requires reducing a cross-batch selection to eight before applying runtime changes', async () => {
  const data = fixture()
  const template = data.batches[0]!
  data.batches = [5, 4].map((count, batchIndex) => ({
    ...template,
    id: `batch-${batchIndex}`,
    state: 'launched',
    sessions: Array.from({ length: count }, (_, index) => ({
      ...template.sessions[0]!,
      id: `${batchIndex}-${index}`,
      title: `Task ${batchIndex}-${index}`,
      sessionId: `child-${batchIndex}-${index}`,
      launched: true,
    })),
  }))
  vi.mocked(sessionCollaborationApi.get).mockResolvedValue(data)
  vi.mocked(sessionCollaborationApi.change).mockResolvedValue({} as never)
  render(<SessionDelegationCard sessionId="parent" />)
  fireEvent.click(await screen.findByLabelText('Select all'))
  const apply = screen.getByRole('button', { name: 'Apply to selected sessions' })
  expect(apply).toBeDisabled()
  expect(apply).toHaveAccessibleDescription('Select at most 8 started sessions per change. Deselect a session to continue.')
  expect(sessionCollaborationApi.change).not.toHaveBeenCalled()
  fireEvent.click(screen.getByLabelText('Task 1-3'))
  expect(apply).toBeEnabled()
  expect(screen.queryByText('Select at most 8 started sessions per change. Deselect a session to continue.')).not.toBeInTheDocument()
  fireEvent.click(apply)
  await waitFor(() => expect(sessionCollaborationApi.change).toHaveBeenCalledTimes(1))
  const submitted = vi.mocked(sessionCollaborationApi.change).mock.calls[0]![1].selections
  expect(submitted).toHaveLength(8)
  expect(submitted.some(item => item.sessionId === 'child-1-3')).toBe(false)
})
