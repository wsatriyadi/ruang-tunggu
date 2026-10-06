import { useCallback, useEffect, useMemo, useState } from 'react'
import { Button, StatusBadge } from '../components'
import {
  changePassword,
  errorMessage,
  getDependencies,
  getSettings,
  getSystem,
  isApiError,
  saveSettings,
} from '../api/client'
import type { Profile, SaveSettingsRequest, Settings } from '../types'
import { useSession } from '../state/useSession'
import { Banner, Loading, formatBytes, timeZoneOptions } from './shared/ui'

type Draft = Omit<Settings, 'stream_key_set'> & { stream_key: string }

export default function Settings() {
  const { logout } = useSession()
  const [draft, setDraft] = useState<Draft | null>(null)
  const [profile, setProfile] = useState<Profile | null>(null)
  const [defaults, setDefaults] = useState<{ ingest_url: string; profile_id: string } | null>(null)
  const [streamKeySet, setStreamKeySet] = useState(false)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [interfaces, setInterfaces] = useState<string[]>([])
  const [systemInfo, setSystemInfo] = useState<Record<string, unknown> | null>(null)
  const [deps, setDeps] = useState<{ ffmpeg: boolean; ffprobe: boolean; ffmpeg_path: string; ffprobe_path: string; ffmpeg_version: string; ffprobe_version: string; ready: boolean } | null>(null)

  const [pw, setPw] = useState({ current_password: '', new_password: '', confirm_password: '' })
  const [pwBusy, setPwBusy] = useState(false)
  const [pwError, setPwError] = useState<string | null>(null)
  const [pwDone, setPwDone] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const r = await getSettings()
      const { stream_key_set: _ignored, ...rest } = r.settings
      setDraft({ ...rest, stream_key: '' })
      setProfile(r.profiles)
      setDefaults(r.defaults)
      setStreamKeySet(r.stream_key_set)
    } catch (err) {
      setError(errorMessage(err, 'Pengaturan gagal dimuat.'))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
    void getSystem()
      .then((r) => {
        // `interfaces` may be absent when the host exposes none; the select
        // must still render, so it falls back to the auto-detect option.
        setInterfaces(Array.isArray(r.interfaces) ? r.interfaces : [])
        setSystemInfo((r.info ?? null) as unknown as Record<string, unknown> | null)
      })
      .catch(() => {
        /* System facts are optional context; settings still work without them. */
      })
    void getDependencies()
      .then(setDeps)
      .catch(() => setDeps(null))
  }, [load])

  const set = useCallback(<K extends keyof Draft>(key: K, value: Draft[K]) => {
    setDraft((d) => (d ? { ...d, [key]: value } : d))
    setSaved(false)
  }, [])

  const save = useCallback(async () => {
    if (!draft) return
    setSaving(true)
    setError(null)
    setSaved(false)
    try {
      const { stream_key, ...rest } = draft
      // A blank stream_key means "keep the stored one" (write-only field), and
      // stream_key_set is server-owned, so it is echoed back unchanged.
      const settings: Settings = { ...rest, stream_key_set: streamKeySet }
      const body: SaveSettingsRequest = stream_key.trim()
        ? { settings, stream_key: stream_key.trim() }
        : { settings }
      const r = await saveSettings(body)
      setStreamKeySet(r.stream_key_set)
      set('stream_key', '')
      setSaved(true)
    } catch (err) {
      setError(
        isApiError(err) ? `${err.message}${err.code ? ` (${err.code})` : ''}` : errorMessage(err),
      )
    } finally {
      setSaving(false)
    }
  }, [draft, set])

  const submitPassword = useCallback(async () => {
    setPwBusy(true)
    setPwError(null)
    setPwDone(false)
    try {
      if (pw.new_password !== pw.confirm_password) {
        setPwError('Konfirmasi kata sandi tidak cocok.')
        return
      }
      await changePassword(pw)
      setPw({ current_password: '', new_password: '', confirm_password: '' })
      setPwDone(true)
    } catch (err) {
      setPwError(errorMessage(err, 'Perubahan kata sandi gagal.'))
    } finally {
      setPwBusy(false)
    }
  }, [pw])

  const zones = useMemo(() => timeZoneOptions(draft?.timezone), [draft?.timezone])

  if (loading && !draft) {
    return (
      <div className="y-root">
        <div className="y-page">
          <Loading label="Memuat pengaturan…" />
        </div>
      </div>
    )
  }

  if (!draft) {
    return (
      <div className="y-root">
        <div className="y-page">
          <Banner tone="error" title="Gagal memuat pengaturan">
            {error ?? 'Pengaturan tidak dapat dimuat.'}
          </Banner>
          <Button onClick={() => void load()}>Coba lagi</Button>
        </div>
      </div>
    )
  }

  const rules = profile?.rules

  return (
    <div className="y-root">
      <div className="y-page">
        <header className="y-page-head">
          <div>
            <h1>Pengaturan</h1>
            <p>Perubahan berlaku untuk sesi berikutnya; sesi berjalan memakai snapshot saat mulai.</p>
          </div>
          <div className="y-btn-row">
            <Button busy={saving} onClick={() => void save()}>
              Simpan pengaturan
            </Button>
          </div>
        </header>

        {error ? (
          <Banner tone="error" title="Penyimpanan gagal">
            {error}
          </Banner>
        ) : null}
        {saved ? <Banner tone="info">Pengaturan tersimpan.</Banner> : null}

        <section className="y-card" aria-label="Umum">
          <h2>Umum</h2>
          <div className="y-form">
            <div className="y-field">
              <label htmlFor="set-instance">Nama instance</label>
              <input
                id="set-instance"
                className="y-input"
                value={draft.instance_name}
                onChange={(e) => set('instance_name', e.target.value)}
              />
            </div>
            <div className="y-field">
              <label htmlFor="set-lang">Bahasa</label>
              <input
                id="set-lang"
                className="y-input"
                value={draft.language}
                onChange={(e) => set('language', e.target.value)}
                disabled
              />
              <span className="y-hint">Bahasa awal aplikasi adalah Indonesia.</span>
            </div>
            <div className="y-field">
              <label htmlFor="set-tz">Zona waktu</label>
              <select
                id="set-tz"
                className="y-select"
                value={draft.timezone}
                onChange={(e) => set('timezone', e.target.value)}
              >
                {zones.map((z) => (
                  <option key={z} value={z}>
                    {z}
                  </option>
                ))}
              </select>
            </div>
          </div>
        </section>

        <section className="y-card" aria-label="Tujuan siaran">
          <h2>Tujuan siaran</h2>
          <div className="y-form">
            <div className="y-field">
              <label htmlFor="set-ingest">URL ingest RTMPS</label>
              <input
                id="set-ingest"
                className="y-input"
                value={draft.ingest_url}
                placeholder={defaults?.ingest_url ?? 'rtmps://a.rtmps.youtube.com/live2'}
                onChange={(e) => set('ingest_url', e.target.value)}
              />
              <span className="y-hint">
                Nilai bawaan: <span className="y-mono">{defaults?.ingest_url ?? 'rtmps://a.rtmps.youtube.com/live2'}</span>.
                Broadcast harus disiapkan terpisah di YouTube Studio; aplikasi hanya mengirim media ke
                ingest URL tersebut.
              </span>
            </div>

            <div className="y-field">
              <label htmlFor="set-key">Stream key</label>
              <input
                id="set-key"
                className="y-input"
                type="password"
                autoComplete="new-password"
                value={draft.stream_key}
                placeholder="Isi untuk mengganti stream key"
                onChange={(e) => set('stream_key', e.target.value)}
              />
              <span className="y-inline">
                <StatusBadge
                  state={streamKeySet ? 'compatible' : 'unchecked'}
                  kind="compatibility"
                  label={streamKeySet ? 'sudah diatur' : 'belum diatur'}
                />
                <span className="y-hint">
                  Stream key bersifat tulis-saja. Nilainya tidak pernah dibaca kembali oleh API;
                  kolom kosong berarti stream key yang ada dipertahankan.
                </span>
              </span>
            </div>

            <div className="y-field">
              <label htmlFor="set-preview">URL atau ID video preview</label>
              <input
                id="set-preview"
                className="y-input"
                value={draft.preview_video_id}
                placeholder="https://www.youtube.com/watch?v=… atau ID video"
                onChange={(e) => set('preview_video_id', e.target.value)}
              />
              <span className="y-hint">
                Dipakai untuk menyematkan pratinjau. Bila embed diblokir, panel menampilkan thumbnail.
              </span>
            </div>
          </div>
        </section>

        <section className="y-card" aria-label="Media">
          <h2>Media</h2>
          <div className="y-form">
            <div className="y-field is-wide">
              <label htmlFor="set-media-dir">Direktori penyimpanan</label>
              <input
                id="set-media-dir"
                className="y-input"
                value={draft.media_dir}
                onChange={(e) => set('media_dir', e.target.value)}
              />
              <span className="y-hint">
                Perubahan lokasi media ditolak ketika ada unggahan atau siaran aktif.
              </span>
            </div>
            <div className="y-field">
              <label htmlFor="set-quota">Kuota media (byte)</label>
              <input
                id="set-quota"
                className="y-input"
                type="number"
                min={0}
                value={draft.quota_bytes}
                onChange={(e) => set('quota_bytes', Number(e.target.value))}
              />
              <span className="y-hint">{formatBytes(draft.quota_bytes)}</span>
            </div>
            <div className="y-field">
              <label htmlFor="set-maxsize">Batas ukuran unggah (byte)</label>
              <input
                id="set-maxsize"
                className="y-input"
                type="number"
                min={0}
                value={draft.max_file_size}
                onChange={(e) => set('max_file_size', Number(e.target.value))}
              />
              <span className="y-hint">{formatBytes(draft.max_file_size)}</span>
            </div>
            <div className="y-field">
              <label htmlFor="set-reserve">Cadangan disk (byte)</label>
              <input
                id="set-reserve"
                className="y-input"
                type="number"
                min={0}
                value={draft.disk_reserve_bytes}
                onChange={(e) => set('disk_reserve_bytes', Number(e.target.value))}
              />
              <span className="y-hint">{formatBytes(draft.disk_reserve_bytes)}</span>
            </div>
          </div>
        </section>

        <section className="y-card" aria-label="Engine">
          <h2>Engine</h2>
          <div className="y-form">
            <div className="y-field">
              <label htmlFor="set-profile">Profil media</label>
              <input
                id="set-profile"
                className="y-input"
                value={draft.profile_id}
                onChange={(e) => set('profile_id', e.target.value)}
              />
              <span className="y-hint">
                {profile ? `${profile.name} · ${profile.transport}` : 'Profil bawaan'}
              </span>
            </div>
            <div className="y-field">
              <label htmlFor="set-ffmpeg">Lokasi FFmpeg</label>
              <input
                id="set-ffmpeg"
                className="y-input"
                value={draft.ffmpeg_path}
                placeholder="/usr/bin/ffmpeg"
                onChange={(e) => set('ffmpeg_path', e.target.value)}
              />
            </div>
            <div className="y-field">
              <label htmlFor="set-ffprobe">Lokasi FFprobe</label>
              <input
                id="set-ffprobe"
                className="y-input"
                value={draft.ffprobe_path}
                placeholder="/usr/bin/ffprobe"
                onChange={(e) => set('ffprobe_path', e.target.value)}
              />
            </div>
            <div className="y-field">
              <label htmlFor="set-stall">Batas kemacetan (detik)</label>
              <input
                id="set-stall"
                className="y-input"
                type="number"
                min={5}
                value={draft.stall_timeout_seconds}
                onChange={(e) => set('stall_timeout_seconds', Number(e.target.value))}
              />
            </div>
            <div className="y-field">
              <label htmlFor="set-attempts">Maksimum percobaan</label>
              <input
                id="set-attempts"
                className="y-input"
                type="number"
                min={1}
                value={draft.max_attempts}
                onChange={(e) => set('max_attempts', Number(e.target.value))}
              />
            </div>
          </div>

          <div className="y-inline">
            <label className="y-check">
              <input
                id="set-autorecon"
                type="checkbox"
                checked={draft.auto_reconnect}
                onChange={(e) => set('auto_reconnect', e.target.checked)}
              />
              <span>Auto reconnect</span>
            </label>
            <label className="y-check">
              <input
                id="set-resume"
                type="checkbox"
                checked={draft.resume_after_restart}
                onChange={(e) => set('resume_after_restart', e.target.checked)}
              />
              <span>Lanjutkan setelah restart server</span>
            </label>
          </div>
        </section>

        <section className="y-card" aria-label="Monitoring">
          <h2>Monitoring</h2>
          <div className="y-form">
            <div className="y-field">
              <label htmlFor="set-iface">Interface jaringan</label>
              <select
                id="set-iface"
                className="y-select"
                value={draft.net_interface}
                onChange={(e) => set('net_interface', e.target.value)}
              >
                <option value="">Otomatis</option>
                {interfaces.map((i) => (
                  <option key={i} value={i}>
                    {i}
                  </option>
                ))}
              </select>
              <span className="y-hint">
                Angka upload pada dashboard berasal dari interface ini dan berbeda dari bitrate
                publisher.
              </span>
            </div>
            <div className="y-field">
              <label htmlFor="set-interval">Interval sampel (detik)</label>
              <input
                id="set-interval"
                className="y-input"
                type="number"
                min={1}
                value={draft.sample_interval_seconds}
                onChange={(e) => set('sample_interval_seconds', Number(e.target.value))}
              />
            </div>
            <div className="y-field">
              <label htmlFor="set-warncpu">Ambang peringatan CPU (%)</label>
              <input
                id="set-warncpu"
                className="y-input"
                type="number"
                min={1}
                max={100}
                value={draft.warn_cpu_percent}
                onChange={(e) => set('warn_cpu_percent', Number(e.target.value))}
              />
            </div>
            <div className="y-field">
              <label htmlFor="set-warndisk">Ambang peringatan disk (%)</label>
              <input
                id="set-warndisk"
                className="y-input"
                type="number"
                min={1}
                max={100}
                value={draft.warn_disk_percent}
                onChange={(e) => set('warn_disk_percent', Number(e.target.value))}
              />
            </div>
          </div>
        </section>

        <section className="y-card" aria-label="Log">
          <h2>Log</h2>
          <div className="y-form">
            <div className="y-field">
              <label htmlFor="set-logbytes">Retensi ukuran (byte)</label>
              <input
                id="set-logbytes"
                className="y-input"
                type="number"
                min={0}
                value={draft.log_max_bytes}
                onChange={(e) => set('log_max_bytes', Number(e.target.value))}
              />
              <span className="y-hint">{formatBytes(draft.log_max_bytes)}</span>
            </div>
            <div className="y-field">
              <label htmlFor="set-logdays">Retensi usia (hari)</label>
              <input
                id="set-logdays"
                className="y-input"
                type="number"
                min={1}
                value={draft.log_max_days}
                onChange={(e) => set('log_max_days', Number(e.target.value))}
              />
            </div>
          </div>
          <p className="y-card-note">
            Ekspor log sudah disanitasi di server. Riwayat server diatur oleh retensi ini;
            "Bersihkan tampilan" di halaman Log tidak menghapusnya.
          </p>
        </section>

        <section className="y-card" aria-label="Ketergantungan server">
          <h2>Ketergantungan server</h2>
          <dl className="y-kv">
            <dt>ffmpeg</dt>
            <dd>
              <StatusBadge
                state={deps?.ffmpeg ? 'compatible' : 'incompatible'}
                kind="compatibility"
                label={deps?.ffmpeg ? 'ditemukan' : 'tidak ditemukan'}
              />{' '}
              <span className="y-mono">{deps?.ffmpeg_version || '—'}</span>
              <br />
              <span className="y-row-meta">{deps?.ffmpeg_path || '—'}</span>
            </dd>
            <dt>ffprobe</dt>
            <dd>
              <StatusBadge
                state={deps?.ffprobe ? 'compatible' : 'incompatible'}
                kind="compatibility"
                label={deps?.ffprobe ? 'ditemukan' : 'tidak ditemukan'}
              />{' '}
              <span className="y-mono">{deps?.ffprobe_version || '—'}</span>
              <br />
              <span className="y-row-meta">{deps?.ffprobe_path || '—'}</span>
            </dd>
          </dl>
          {deps && !deps.ready ? (
            <Banner tone="error" title="Mulai siaran dinonaktifkan">
              FFmpeg dan FFprobe harus ditemukan sebelum Start Live dapat dijalankan.
            </Banner>
          ) : null}
          {systemInfo ? (
            <dl className="y-kv">
              <dt>Host</dt>
              <dd>{String(systemInfo.hostname ?? '—')}</dd>
              <dt>Sistem</dt>
              <dd>
                {String(systemInfo.os ?? '—')} · {String(systemInfo.arch ?? '—')}
              </dd>
              <dt>CPU</dt>
              <dd>
                {String(systemInfo.cpu_model ?? '—')} ({String(systemInfo.cpu_cores ?? '—')} inti)
              </dd>
              <dt>RAM</dt>
              <dd>{formatBytes(Number(systemInfo.total_ram_bytes ?? 0))}</dd>
            </dl>
          ) : null}
        </section>

        <section className="y-card" aria-label="Aturan profil media">
          <h2>Aturan profil media</h2>
          {rules ? (
            <dl className="y-kv">
              <dt>Video</dt>
              <dd className="y-mono">{rules.video_codec}</dd>
              <dt>Audio</dt>
              <dd className="y-mono">{rules.audio_codecs.join(', ')}</dd>
              <dt>Format piksel</dt>
              <dd className="y-mono">{rules.pixel_formats.join(', ')}</dd>
              <dt>Resolusi maksimum</dt>
              <dd>{rules.max_resolution ? `≤ ${rules.max_resolution} px` : 'Tidak dibatasi'}</dd>
              <dt>Frame rate maksimum</dt>
              <dd>≤ {rules.max_fps} fps</dd>
              <dt>Sample rate</dt>
              <dd className="y-mono">
                {rules.sample_rates.join(', ')} Hz (disarankan {rules.preferred_sample_rate} Hz)
              </dd>
              <dt>Kanal</dt>
              <dd>{rules.channels === 2 ? 'Stereo' : rules.channels}</dd>
              <dt>Jarak keyframe</dt>
              <dd>
                target {rules.target_keyframe_gap} dtk · batas keras {rules.max_keyframe_gap} dtk
              </dd>
            </dl>
          ) : (
            <p className="y-card-note">Aturan profil tidak tersedia.</p>
          )}
          <Banner tone="info" title="Kedua track wajib">
            Setiap file harus punya satu track video dan satu track audio. Berkas audio-saja ditolak,
            dan publisher memakai stream copy sehingga tidak ada transcoding otomatis.
          </Banner>
        </section>

        <section className="y-card" aria-label="Akun">
          <h2>Akun</h2>
          <div className="y-form">
            <div className="y-field">
              <label htmlFor="pw-current">Kata sandi saat ini</label>
              <input
                id="pw-current"
                className="y-input"
                type="password"
                autoComplete="current-password"
                value={pw.current_password}
                onChange={(e) => setPw({ ...pw, current_password: e.target.value })}
              />
            </div>
            <div className="y-field">
              <label htmlFor="pw-new">Kata sandi baru</label>
              <input
                id="pw-new"
                className="y-input"
                type="password"
                autoComplete="new-password"
                value={pw.new_password}
                onChange={(e) => setPw({ ...pw, new_password: e.target.value })}
              />
            </div>
            <div className="y-field">
              <label htmlFor="pw-confirm">Ulangi kata sandi baru</label>
              <input
                id="pw-confirm"
                className="y-input"
                type="password"
                autoComplete="new-password"
                value={pw.confirm_password}
                onChange={(e) => setPw({ ...pw, confirm_password: e.target.value })}
              />
            </div>
          </div>
          {pwError ? (
            <Banner tone="error" title="Perubahan kata sandi gagal">
              {pwError}
            </Banner>
          ) : null}
          {pwDone ? <Banner tone="info">Kata sandi diubah.</Banner> : null}
          <div className="y-btn-row">
            <Button busy={pwBusy} onClick={() => void submitPassword()}>
              Ubah kata sandi
            </Button>
            <Button variant="danger" onClick={() => void logout()}>
              Keluar
            </Button>
          </div>
          <p className="y-card-note">
            Keluar hanya menutup sesi peramban. Siaran yang sedang berjalan tidak ikut berhenti.
          </p>
        </section>
      </div>
    </div>
  )
}