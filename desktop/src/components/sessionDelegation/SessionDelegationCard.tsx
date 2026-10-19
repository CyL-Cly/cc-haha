import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'
import { sessionCollaborationApi, type SessionDelegationSnapshot } from '@/api/sessionCollaboration'
import { ModelSelector, type ModelProviderChoice } from '@/components/controls/ModelSelector'
import { Badge, type Tone } from '@/components/ui/Badge'
import { Button } from '@/components/ui/Button'
import { Checkbox } from '@/components/ui/Checkbox'
import { SelectField } from '@/components/ui/SelectField'
import { useTranslation } from '@/i18n'
import { openSessionSource } from '@/lib/sessionNavigation'
import type { RuntimeSelection } from '@/types/runtime'
import type { SessionDelegationBatch, SessionRuntimeSelection } from '../../../../src/shared/sessionDelegation'

function selection(runtime: SessionRuntimeSelection): RuntimeSelection {
  return { ...runtime, effortLevel: ['low', 'medium', 'high', 'xhigh', 'max'].includes(runtime.effortLevel ?? '') ? runtime.effortLevel as RuntimeSelection['effortLevel'] : undefined }
}

/** Server state survives navigation and reload; local edits never alter a running session. */
export function SessionDelegationCard({ sessionId }: { sessionId: string }) {
  const t = useTranslation()
  const selectionLimitHintId = useId()
  const [snapshot, setSnapshot] = useState<SessionDelegationSnapshot | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [selected, setSelected] = useState<string[]>([])
  const [drafts, setDrafts] = useState<Record<string, SessionRuntimeSelection>>({})
  const [mode, setMode] = useState<'after_turn' | 'immediate'>('after_turn')
  const [resume, setResume] = useState(true)
  const draftRevisions = useRef(new Map<string, number>())
  const alive = useRef(true)
  const reading = useRef(false)
  const request = useRef<{ signature: string; id: string } | null>(null)
  const refresh = useCallback(async () => {
    if (reading.current) return
    reading.current = true
    try {
      const next = await sessionCollaborationApi.get(sessionId)
      if (alive.current) setSnapshot(next)
    } catch (reason) {
      if (alive.current) setError(reason instanceof Error ? reason.message : String(reason))
    } finally { reading.current = false }
  }, [sessionId])
  useEffect(() => {
    alive.current = true
    void refresh()
    const timer = window.setInterval(() => void refresh(), 2000)
    return () => { alive.current = false; window.clearInterval(timer) }
  }, [refresh])
  const choices = useMemo(() => {
    const groups = new Map<string | null, ModelProviderChoice>()
    for (const model of snapshot?.models ?? []) {
      const group = groups.get(model.providerId) ?? { providerId: model.providerId, providerName: model.providerName, isDefault: false, models: [] }
      group.isDefault ||= model.isDefault
      group.models.push({ id: model.modelId, name: model.modelName, description: '', context: '', supportedReasoningEfforts: model.supportedEfforts.filter((effort): effort is NonNullable<RuntimeSelection['effortLevel']> => ['low', 'medium', 'high', 'xhigh', 'max'].includes(effort)) })
      groups.set(model.providerId, group)
    }
    return [...groups.values()]
  }, [snapshot?.models])
  const batches = snapshot?.batches.map(batch => batch.state === 'cancelled' ? { ...batch, sessions: batch.sessions.filter(entry => entry.launched) } : batch).filter(batch => batch.sessions.length > 0) ?? []
  const entries = batches.flatMap(batch => batch.sessions.map(original => {
    const actual = snapshot?.sessions?.find(item => item.sessionId === original.sessionId)
    const entry = actual ? { ...original, runtime: actual.runtime, error: actual.error ?? original.error } : original
    return { entry, batch, key: `${batch.id}:${entry.id}` }
  }))
  const started = (row: typeof entries[number]) => !!row.entry.launched || row.batch.state === 'launched'
  const chosen = entries.filter(row => selected.includes(row.key))
  const selectionLimitExceeded = chosen.filter(started).length > 8
  const effective = (key: string, runtime?: SessionRuntimeSelection) => drafts[key] ?? runtime
  const ready = (runtime?: SessionRuntimeSelection) => !!runtime && (snapshot?.models ?? []).some(model => model.providerId === runtime.providerId && model.modelId === runtime.modelId)
  const run = async (action: () => Promise<unknown>) => {
    if (busy) return
    setBusy(true)
    setError(null)
    try { await action(); await refresh() } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason))
      await refresh()
    } finally { if (alive.current) setBusy(false) }
  }
  const edit = (rows: typeof entries, value: RuntimeSelection) => {
    setSelected(previous => [...new Set([...previous, ...rows.filter(started).map(row => row.key)])])
    for (const row of rows) if (!draftRevisions.current.has(row.batch.id)) draftRevisions.current.set(row.batch.id, row.batch.revision)
    setDrafts(previous => ({ ...previous, ...Object.fromEntries(rows.map(row => [row.key, value])) }))
  }
  const conflicted = (batch: SessionDelegationBatch) => draftRevisions.current.has(batch.id) && draftRevisions.current.get(batch.id) !== batch.revision
  const discard = (batch: SessionDelegationBatch) => {
    draftRevisions.current.delete(batch.id)
    setDrafts(previous => Object.fromEntries(Object.entries(previous).filter(([key]) => !key.startsWith(`${batch.id}:`))))
  }
  const confirm = (batch: SessionDelegationBatch) => run(async () => {
    const updates = batch.sessions.flatMap(entry => !entry.launched && drafts[`${batch.id}:${entry.id}`] ? [{ id: entry.id, runtime: drafts[`${batch.id}:${entry.id}`]! }] : [])
    const current = updates.length ? await sessionCollaborationApi.update(sessionId, batch, updates) : batch
    if (updates.length) {
      discard(batch)
      setSnapshot(previous => previous ? { ...previous, batches: previous.batches.map(item => item.id === current.id ? current : item) } : previous)
    }
    await sessionCollaborationApi.act(sessionId, current, 'confirm')
  })
  const apply = () => run(async () => {
    if (selectionLimitExceeded) return
    const selections = chosen.filter(started).flatMap(({ entry, key }) => entry.sessionId && effective(key, entry.runtime) ? [{ sessionId: entry.sessionId, runtime: effective(key, entry.runtime)! }] : [])
    const signature = JSON.stringify({ mode, resume, selections })
    if (request.current?.signature !== signature) request.current = { signature, id: crypto.randomUUID() }
    await sessionCollaborationApi.change(sessionId, { requestId: request.current.id, mode, resume, selections })
    request.current = null
    setDrafts(previous => Object.fromEntries(Object.entries(previous).filter(([key]) => !selected.includes(key))))
    setSelected([])
  })
  if (!snapshot || (!batches.length && !snapshot.changes.length)) return null
  const activeChanges = snapshot.changes.filter(change => change.state !== 'applied' && change.state !== 'cancelled')
  return <section aria-label={t('delegation.title')} className="@container my-2 min-w-0 rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-surface-container-lowest)] text-[var(--color-text-primary)]">
    <div className="flex min-w-0 flex-wrap items-center justify-between gap-3 border-b border-[var(--color-border)] px-4 py-3">
      <h3 className="text-sm font-semibold">{t('delegation.title')}</h3>
      <Checkbox size="sm" label={t('delegation.selectAll')} indeterminate={selected.length > 0 && !entries.every(row => selected.includes(row.key))} checked={entries.length > 0 && entries.every(row => selected.includes(row.key))} disabled={busy} onChange={event => setSelected(event.target.checked ? entries.map(row => row.key) : [])} />
    </div>
    {error && <p role="alert" className="mx-4 mt-3 break-words text-xs text-[var(--color-error)]">{error}</p>}
    {batches.map(batch => <div key={batch.id} className="min-w-0 px-4 py-3">
      <div className="mb-1"><Badge tone={batch.state === 'review' ? 'warning' : batch.state === 'failed' ? 'danger' : 'neutral'}>{t(`delegation.state.${batch.state}`)}</Badge></div>
      {conflicted(batch) && <div role="alert" className="text-xs text-[var(--color-error)]">{t('delegation.conflict')} <Button size="sm" variant="ghost" onClick={() => discard(batch)}>{t('delegation.reload')}</Button></div>}
      {batch.error && <p role="alert" className="text-xs text-[var(--color-error)]">{batch.error}</p>}
      {batch.sessions.map(entry => {
        const key = `${batch.id}:${entry.id}`
        const actual = snapshot.sessions?.find(item => item.sessionId === entry.sessionId)
        const runtime = effective(key, actual ? actual.runtime : entry.runtime)
        const title = entry.title ?? entry.prompt.slice(0, 80)
        const stateTone: Tone = actual?.paused || actual?.state === 'blocked' ? 'warning' : actual?.state === 'completed' ? 'success' : actual?.state === 'failed' ? 'danger' : actual?.state === 'running' ? 'info' : 'neutral'
        return <div key={entry.id} data-testid="delegation-task-row" className="grid min-w-0 gap-3 border-b border-[var(--color-border)] py-3 last:border-b-0 @min-[560px]:grid-cols-[minmax(0,1fr)_240px] @min-[560px]:items-start">
          <div className="min-w-0">
            <Checkbox size="sm" label={<span className="block break-words font-medium">{title}</span>} checked={selected.includes(key)} disabled={busy || batch.state === 'launching'} onChange={event => setSelected(previous => event.target.checked ? [...previous, key] : previous.filter(id => id !== key))} />
            <div className="ml-[22px] mt-1 space-y-2"><p className="line-clamp-2 break-words text-xs leading-relaxed text-[var(--color-text-secondary)]" title={entry.prompt}>{entry.prompt}</p>
            <div className="flex flex-wrap items-center gap-2">{actual && <Badge tone={stateTone}>{actual.paused ? t('delegation.paused') : t(actual.state === 'completed' ? 'delegation.completed' : actual.state === 'failed' ? 'delegation.state.failed' : actual.state === 'queued' ? 'delegation.queued' : actual.state === 'blocked' ? 'delegation.blocked' : actual.state === 'idle' ? 'delegation.idle' : 'delegation.active')}</Badge>}
            {entry.sessionId && <Button size="sm" variant="ghost" onClick={() => openSessionSource(entry.sessionId!, title)}>{t('delegation.open')}</Button>}</div>
            {actual?.error && <p role="alert" className="break-words text-xs text-[var(--color-error)]">{actual.error}</p>}
            {!ready(runtime) && <p className="text-xs text-[var(--color-error)]">{t('delegation.unavailable')}</p>}
            {entry.error && entry.error !== actual?.error && <p role="alert" className="break-words text-xs text-[var(--color-error)]">{entry.error}</p>}
          </div></div>
          <div className="min-w-0 space-y-1.5 pl-[22px] @min-[560px]:pl-0"><p className="text-xs text-[var(--color-text-tertiary)]">{t('delegation.model')}</p><ModelSelector appearance="field" fluid ariaLabel={`${title} · ${t('delegation.model')}`} runtimeChoices={choices} runtimeSelection={runtime ? selection(runtime) : { providerId: null, modelId: '' }} onRuntimeSelectionChange={value => edit(entries.filter(row => row.key === key), value)} disabled={busy || !choices.length || batch.state === 'launching'} /></div>
        </div>
      })}
      {(batch.state === 'review' || batch.state === 'failed') && <div className="flex flex-wrap justify-end gap-2 pt-3">
        <Button size="sm" disabled={busy || conflicted(batch) || batch.sessions.some(entry => !entry.launched && !ready(effective(`${batch.id}:${entry.id}`, entry.runtime)))} onClick={() => void confirm(batch)}>{t('delegation.start')}</Button>
        <Button size="sm" variant="ghost" disabled={busy} onClick={() => void run(() => sessionCollaborationApi.act(sessionId, batch, 'cancel'))}>{t('delegation.cancel')}</Button>
      </div>}
    </div>)}
    {chosen.length > 0 && <div className="space-y-3 border-t border-[var(--color-border)] bg-[var(--color-surface-container-low)] px-4 py-3">
      <div className="min-w-0 space-y-1.5"><p className="text-xs font-medium text-[var(--color-text-secondary)]">{t('delegation.batchModel')}</p><ModelSelector appearance="field" fluid ariaLabel={t('delegation.batchModel')} runtimeChoices={choices} runtimeSelection={selection(effective(chosen[0]!.key, chosen[0]!.entry.runtime) ?? { providerId: null, modelId: '' })} onRuntimeSelectionChange={value => edit(chosen, value)} disabled={busy || !choices.length} /></div>
      {chosen.some(started) && <>
        <SelectField label={t('delegation.when')} value={mode} onChange={setMode} disabled={busy} options={[{ value: 'after_turn', label: t('delegation.afterTurn') }, { value: 'immediate', label: t('delegation.immediate') }]} />
        <Checkbox label={t('delegation.resume')} checked={resume} disabled={busy} onChange={event => setResume(event.target.checked)} />
        {selectionLimitExceeded && <p id={selectionLimitHintId} role="alert" className="text-xs text-[var(--color-error)]">{t('delegation.selectionLimit')}</p>}
        <Button size="sm" aria-describedby={selectionLimitExceeded ? selectionLimitHintId : undefined} disabled={busy || selectionLimitExceeded || chosen.some(row => !started(row) || !ready(effective(row.key, row.entry.runtime)))} onClick={() => void apply()}>{t('delegation.apply')}</Button>
      </>}
    </div>}
    {activeChanges.map(change => <div key={change.id} className="min-w-0 space-y-2 border-t border-[var(--color-border)] px-4 py-3 text-xs" role="status">
      <span>{t(`delegation.change.${change.state}`)}</span>
      <ul className="space-y-1 break-words text-[var(--color-text-secondary)]">{change.selections.map(item => <li key={item.sessionId}>{entries.find(row => row.entry.sessionId === item.sessionId)?.entry.title ?? item.sessionId} · {snapshot.models.find(model => model.providerId === item.runtime.providerId)?.providerName ?? item.runtime.providerId} · {item.runtime.modelId}</li>)}</ul>
      {change.error && <p role="alert" className="text-[var(--color-error)]">{change.error}</p>}
      {change.state === 'pending' && <Button size="sm" variant="ghost" disabled={busy} onClick={() => void run(() => sessionCollaborationApi.cancelChange(sessionId, change.id, change.revision))}>{t('delegation.cancelChange')}</Button>}
    </div>)}
  </section>
}
