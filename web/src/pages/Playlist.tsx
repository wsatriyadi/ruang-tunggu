import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Button, EmptyState, IconButton, Modal, StatusBadge } from '../components'
import {
  createPlaylist,
  deletePlaylist,
  getVideoThumbnailUrl,
  errorMessage,
  isApiError,
  updatePlaylist,
} from '../api/client'
import { usePlaylists } from '../state/usePlaylists'
import { useVideos } from '../state/useVideos'
import type { Playlist } from '../types'
import {
  Banner,
  COMPAT_LABEL,
  IssueList,
  Loading,
  PLAYBACK_MODES,
  detailArray,
  formatClock,
  formatDuration,
  playbackModeHelp,
} from './shared/ui'

type PlaybackMode = 'sequential' | 'random' | 'random_no_repeat'

export default function Playlist() {
  const playlists = usePlaylists()
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [formError, setFormError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [creating, setCreating] = useState(false)
  const [newName, setNewName] = useState('')
  const [deleteTarget, setDeleteTarget] = useState<Playlist | null>(null)
  const [deleteBlockers, setDeleteBlockers] = useState<{ name: string; id: string }[]>([])

  const items = playlists.items
  const selected = useMemo(
    () => items.find((p) => p.id === selectedId) ?? items[0] ?? null,
    [items, selectedId],
  )

  // Editing state lives here so the working copy can differ from the server
  // until the operator saves — which creates a new revision.
  const [draft, setDraft] = useState<{ name: string; mode: PlaybackMode; loop: boolean; mediaIds: string[] } | null>(null)

  useEffect(() => {
    if (!selected) {
      setDraft(null)
      return
    }
    setDraft({
      name: selected.name,
      mode: selected.playback_mode,
      loop: selected.loop_enabled,
      mediaIds: (selected.items ?? []).map((i) => i.media_id),
    })
  }, [selected])

  const move = useCallback((index: number, delta: number) => {
    setDraft((current) => {
      if (!current) return current
      const next = current.mediaIds.slice()
      const target = index + delta
      if (target < 0 || target >= next.length) return current
      const b = next[target]
      next[target] = next[index]!
      next[index] = b!
      return { ...current, mediaIds: next }
    })
  }, [])

  // Drag-and-drop is an enhancement only; the up/down buttons above are the
  // accessible, keyboard-reachable path (AC-21).
  const dragIndex = useRef<number | null>(null)
  const dropOn = useCallback((index: number) => {
    const from = dragIndex.current
    dragIndex.current = null
    if (from === null || from === index) return
    setDraft((current) => {
      if (!current) return current
      const next = current.mediaIds.slice()
      const moved = next.splice(from, 1)[0]
      if (moved === undefined) return current
      next.splice(index, 0, moved)
      return { ...current, mediaIds: next }
    })
  }, [])

  const save = useCallback(async () => {
    if (!selected || !draft) return
    setSaving(true)
    setFormError(null)
    try {
      await updatePlaylist(selected.id, {
        name: draft.name.trim() || selected.name,
        playback_mode: draft.mode,
        loop_enabled: draft.loop,
        media_ids: draft.mediaIds,
      })
      await playlists.refresh()
    } catch (err) {
      setFormError(errorMessage(err, 'Penyimpanan playlist gagal.'))
    } finally {
      setSaving(false)
    }
  }, [draft, playlists, selected])

  const addPlaylist = useCallback(async () => {
    const name = newName.trim()
    if (!name) return
    setCreating(true)
    setFormError(null)
    try {
      const created = await createPlaylist({ name, playback_mode: 'sequential', loop_enabled: true })
      setNewName('')
      await playlists.refresh()
      setSelectedId(created.id)
    } catch (err) {
      setFormError(errorMessage(err, 'Pembuatan playlist gagal.'))
    } finally {
      setCreating(false)
    }
  }, [newName, playlists])

  const remove = useCallback(async () => {
    if (!deleteTarget) return
    setFormError(null)
    setDeleteBlockers([])
    try {
      await deletePlaylist(deleteTarget.id)
      setDeleteTarget(null)
      if (selectedId === deleteTarget.id) setSelectedId(null)
      await playlists.refresh()
    } catch (err) {
      if (isApiError(err) && err.details) {
        const blockers = detailArray(err.details, 'schedules').map((s) => ({
          name: String(s.name ?? s.schedule_name ?? s.id ?? ''),
          id: String(s.id ?? ''),
        }))
        if (blockers.length > 0) {
          setDeleteBlockers(blockers)
        } else {
          setFormError(err.message)
        }
      } else {
        setFormError(errorMessage(err, 'Penghapusan playlist gagal.'))
      }
    }
  }, [deleteTarget, playlists, selectedId])

  const itemRows = selected?.items ?? []
  const rowById = useMemo(() => {
    const map = new Map<string, (typeof itemRows)[number]>()
    for (const row of itemRows) map.set(row.media_id, row)
    return map
  }, [itemRows])

  const savedIds = new Set(itemRows.map((i) => i.media_id))
  const pendingIds = draft?.mediaIds.filter((id) => !savedIds.has(id)) ?? []

  const library = useVideos({ limit: 200 })

  return (
    <div className="y-root">
      <div className="y-page">
        <header className="y-page-head">
          <div>
            <h1>Playlist</h1>
            <p>Setiap penyimpanan membuat revisi baru. Jadwal tetap memakai revisi yang dipilihnya.</p>
          </div>
        </header>

        {formError ? (
          <Banner tone="error" title="Tindakan gagal">
            {formError}
          </Banner>
        ) : null}

        <section className="y-card" aria-label="Daftar playlist">
          <div className="y-card-head">
            <h2>Daftar Playlist</h2>
          </div>

          <div className="y-inline">
            <label className="y-sr" htmlFor="new-playlist-name">
              Nama playlist baru
            </label>
            <input
              id="new-playlist-name"
              className="y-input"
              style={{ flex: '1 1 200px' }}
              value={newName}
              placeholder="Nama playlist baru"
              onChange={(e) => setNewName(e.target.value)}
            />
            <Button busy={creating} disabled={!newName.trim()} onClick={() => void addPlaylist()}>
              Buat playlist
            </Button>
          </div>

          {playlists.loading && items.length === 0 ? <Loading label="Memuat playlist…" /> : null}
          {playlists.error ? (
            <Banner tone="error" title="Gagal memuat playlist">
              {playlists.error}
            </Banner>
          ) : null}

          {!playlists.loading && !playlists.error && items.length === 0 ? (
            <EmptyState
              title="Belum ada playlist"
              description="Buat playlist pertama, lalu tambahkan video dari pustaka."
            />
          ) : null}

          {items.length > 0 ? (
            <ul className="y-rows">
              {items.map((p) => (
                <li className="y-row" key={p.id}>
                  <button
                    type="button"
                    className="y-row-main"
                    style={{
                      background: 'none',
                      border: 'none',
                      color: 'inherit',
                      textAlign: 'left',
                      cursor: 'pointer',
                      padding: 0,
                    }}
                    aria-current={selected?.id === p.id ? 'true' : undefined}
                    onClick={() => setSelectedId(p.id)}
                  >
                    <span className="y-row-title">
                      {p.name}
                      {selected?.id === p.id ? ' (dipilih)' : ''}
                    </span>
                    <span className="y-row-meta">
                      revisi {p.revision_number} · {p.item_count} video · {formatDuration(p.cycle_duration)}{' '}
                      per putaran
                    </span>
                  </button>
                  <div className="y-row-actions">
                    <StatusBadge
                      state={p.compatibility}
                      kind="compatibility"
                      label={COMPAT_LABEL[p.compatibility] ?? p.compatibility}
                    />
                    <IconButton
                      label={`Hapus playlist ${p.name}`}
                      icon="✕"
                      variant="danger"
                      onClick={() => {
                        setFormError(null)
                        setDeleteBlockers([])
                        setDeleteTarget(p)
                      }}
                    />
                  </div>
                </li>
              ))}
            </ul>
          ) : null}
        </section>

        {selected && draft ? (
          <section className="y-card" aria-label={`Playlist ${selected.name}`}>
            <div className="y-card-head">
              <h2>
                {selected.name} · revisi {selected.revision_number}
              </h2>
              <div className="y-btn-row">
                <Button busy={saving} onClick={() => void save()}>
                  Simpan sebagai revisi baru
                </Button>
              </div>
            </div>

            <Banner tone="info" title="Penyimpanan membuat revisi baru">
              Sesi yang sedang berjalan memakai snapshot saat mulai, sehingga tidak berubah oleh
              penyimpanan ini. Jadwal yang sudah disimpan tetap mereferensikan revisi lamanya;
              pembaruan jadwal harus dilakukan eksplisit.
              {selected.latest_revision_id !== selected.revision_id ? (
                <>
                  {' '}
                  <strong>Revisi terbaru sudah berbeda</strong> dari yang sedang ditampilkan.
                </>
              ) : null}
            </Banner>

            <div className="y-form">
              <div className="y-field">
                <label htmlFor="pl-name">Nama playlist</label>
                <input
                  id="pl-name"
                  className="y-input"
                  value={draft.name}
                  onChange={(e) => setDraft({ ...draft, name: e.target.value })}
                />
              </div>

              <div className="y-field">
                <label htmlFor="pl-mode">Mode pemutaran</label>
                <select
                  id="pl-mode"
                  className="y-select"
                  value={draft.mode}
                  onChange={(e) => setDraft({ ...draft, mode: e.target.value as PlaybackMode })}
                >
                  {PLAYBACK_MODES.map((m) => (
                    <option key={m.value} value={m.value}>
                      {m.label}
                    </option>
                  ))}
                </select>
                <span className="y-hint">{playbackModeHelp(draft.mode)}</span>
              </div>

              <div className="y-check">
                <input
                  id="pl-loop"
                  type="checkbox"
                  checked={draft.loop}
                  onChange={(e) => setDraft({ ...draft, loop: e.target.checked })}
                />
                <span className="y-checklabel">
                  <label htmlFor="pl-loop">Loop</label>
                  <span className="y-hint">
                    Aktif: putaran berikutnya dibuat sampai operator menghentikan atau waktu akhir
                    tercapai. Mati: sesi selesai setelah satu putaran.
                  </span>
                </span>
              </div>
            </div>

            <dl className="y-kv">
              <dt>Jumlah video</dt>
              <dd>{draft.mediaIds.length}</dd>
              <dt>Durasi satu putaran</dt>
              <dd>{formatDuration(cycleDuration(draft.mediaIds, rowById))}</dd>
              <dt>Mode</dt>
              <dd>{PLAYBACK_MODES.find((m) => m.value === draft.mode)?.label ?? draft.mode}</dd>
              <dt>Loop</dt>
              <dd>{draft.loop ? 'Aktif' : 'Mati — sesi selesai setelah satu putaran'}</dd>
            </dl>

            <h3>Urutan video</h3>
            <p className="y-card-note">
              Gunakan tombol naik/turun untuk mengubah urutan. Seret-lepas hanya tambahan dan bukan
              satu-satunya cara.
            </p>

            {draft.mediaIds.length === 0 ? (
              <EmptyState
                title="Playlist ini belum berisi video"
                description="Tambahkan video dari pustaka di bawah."
              />
            ) : (
              <ol className="y-rows">
                {draft.mediaIds.map((id, index) => {
                  const row = rowById.get(id)
                  return (
                    <li
                      className={`y-row y-sortable${dragIndex.current === index ? ' y-drop-item-dragging' : ''}`}
                      key={id}
                      draggable
                      onDragStart={() => {
                        dragIndex.current = index
                      }}
                      onDragOver={(e) => e.preventDefault()}
                      onDrop={() => dropOn(index)}
                    >
                      <span className="y-mono" aria-hidden="true">
                        {index + 1}
                      </span>
                      {row?.has_thumbnail ? (
                        <img
                          className="y-thumb"
                          src={getVideoThumbnailUrl(id)}
                          alt={`Thumbnail ${row?.name ?? 'video'}`}
                          loading="lazy"
                        />
                      ) : (
                        <span className="y-thumb-ph">Tanpa thumbnail</span>
                      )}
                      <div className="y-row-main">
                        <div className="y-row-title">{row?.name ?? id}</div>
                        <div className="y-row-meta">
                          {row ? formatClock(row.duration) : 'Durasi belum diketahui'} ·{' '}
                          {COMPAT_LABEL[row?.compatibility ?? 'unchecked']}
                        </div>
                      </div>
                      <div className="y-row-actions">
                        <IconButton
                          label={`Naikkan ${row?.name ?? 'video'} ke posisi ${index}`}
                          icon="↑"
                          disabled={index === 0}
                          onClick={() => move(index, -1)}
                        />
                        <IconButton
                          label={`Turunkan ${row?.name ?? 'video'} ke posisi ${index + 2}`}
                          icon="↓"
                          disabled={index === draft.mediaIds.length - 1}
                          onClick={() => move(index, 1)}
                        />
                        <IconButton
                          label={`Keluarkan ${row?.name ?? 'video'} dari playlist`}
                          icon="✕"
                          variant="danger"
                          onClick={() =>
                            setDraft({
                              ...draft,
                              mediaIds: draft.mediaIds.filter((mid) => mid !== id),
                            })
                          }
                        />
                      </div>
                    </li>
                  )
                })}
              </ol>
            )}

            {selected.issues && selected.issues.length > 0 ? (
              <Banner
                tone={selected.compatibility === 'incompatible' ? 'error' : 'warn'}
                title={`Pemeriksaan kompatibilitas lintas berkas: ${COMPAT_LABEL[selected.compatibility] ?? selected.compatibility}`}
              >
                <p style={{ margin: '0 0 6px' }}>
                  Publisher memakai stream copy, sehingga codec, resolusi, time base, SPS/PPS, fps, dan
                  sample rate harus seragam. Perbedaan berikut memblokir mulai siaran sampai
                  diperbaiki.
                </p>
                <IssueList issues={selected.issues} showField />
              </Banner>
            ) : null}

            <h3>Tambah dari pustaka video</h3>
            {library.loading && library.items.length === 0 ? <Loading /> : null}
            {library.items.length > 0 ? (
              <ul className="y-rows">
                {library.items.map((m) => {
                  const already = draft.mediaIds.includes(m.id)
                  return (
                    <li className="y-row" key={m.id}>
                      <div className="y-row-main">
                        <div className="y-row-title">{m.original_name}</div>
                        <div className="y-row-meta">
                          {formatClock(m.duration)} · {COMPAT_LABEL[m.compatibility] ?? m.compatibility}
                        </div>
                      </div>
                      <div className="y-row-actions">
                        <Button
                          size="sm"
                          variant={already ? 'ghost' : 'primary'}
                          disabled={already}
                          onClick={() =>
                            setDraft({ ...draft, mediaIds: [...draft.mediaIds, m.id] })
                          }
                        >
                          {already ? 'Sudah ada' : 'Tambahkan'}
                        </Button>
                      </div>
                    </li>
                  )
                })}
              </ul>
            ) : null}
            {pendingIds.length > 0 ? (
              <p className="y-card-note">
                {pendingIds.length} item sudah dihapus dari pustaka tetapi masih ada di revisi ini.
                Simpan untuk melepaskannya.
              </p>
            ) : null}
          </section>
        ) : null}
      </div>

      <Modal
        open={!!deleteTarget}
        title="Hapus playlist"
        onClose={() => {
          setDeleteTarget(null)
          setDeleteBlockers([])
        }}
        footer={
          <>
            <Button
              variant="ghost"
              onClick={() => {
                setDeleteTarget(null)
                setDeleteBlockers([])
              }}
            >
              Batal
            </Button>
            <Button variant="danger" disabled={deleteBlockers.length > 0} onClick={() => void remove()}>
              Hapus
            </Button>
          </>
        }
      >
        {deleteBlockers.length > 0 ? (
          <Banner tone="error" title="Playlist masih dipakai oleh jadwal">
            <ul className="y-issue-list">
              {deleteBlockers.map((b, i) => (
                <li key={`${b.id}-${i}`}>{b.name || b.id}</li>
              ))}
            </ul>
            <p style={{ margin: '6px 0 0' }}>
              Hapus atau ubah jadwal tersebut lebih dahulu agar tidak mereferensikan revisi playlist
              ini.
            </p>
          </Banner>
        ) : (
          <p>
            Playlist <strong>{deleteTarget?.name}</strong> beserta seluruh revisinya akan dihapus.
            Tindakan ini tidak dapat dibatalkan.
          </p>
        )}
      </Modal>
    </div>
  )
}

/** Sums the durations of the rows a draft currently references. */
function cycleDuration(
  mediaIds: string[],
  rows: Map<string, { duration: number }>,
): number {
  let total = 0
  for (const id of mediaIds) total += rows.get(id)?.duration ?? 0
  return total
}