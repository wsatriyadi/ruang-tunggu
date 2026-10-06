import { useEffect, useState } from 'react'
import { Button } from '../components'
import { useSession } from '../state/useSession'
import { getSystem } from '../api/client'
import { Banner, browserTimeZone, timeZoneOptions } from './shared/ui'

const DEFAULT_INGEST = 'rtmps://a.rtmps.youtube.com/live2'

export default function Setup() {
  const { setup, loading } = useSession()
  const [form, setForm] = useState({
    username: '',
    password: '',
    confirm_password: '',
    instance_name: '',
    timezone: browserTimeZone(),
    media_dir: '',
    net_interface: '',
    ingest_url: DEFAULT_INGEST,
    stream_key: '',
    preview_video_id: '',
  })
  const [interfaces, setInterfaces] = useState<string[]>([])
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    void getSystem()
      .then((r) => setInterfaces(r.interfaces))
      .catch(() => {
        /* Interface list is optional; the default auto-detection still works. */
      })
  }, [])

  const set = <K extends keyof typeof form>(key: K, value: (typeof form)[K]) =>
    setForm((f) => ({ ...f, [key]: value }))

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError(null)
    if (form.password !== form.confirm_password) {
      setError('Konfirmasi kata sandi tidak cocok.')
      return
    }
    setBusy(true)
    try {
      await setup({
        username: form.username,
        password: form.password,
        confirm_password: form.confirm_password,
        ...(form.instance_name ? { instance_name: form.instance_name } : {}),
        ...(form.media_dir ? { media_dir: form.media_dir } : {}),
        ...(form.net_interface ? { net_interface: form.net_interface } : {}),
        ...(form.ingest_url ? { ingest_url: form.ingest_url } : {}),
        ...(form.stream_key ? { stream_key: form.stream_key } : {}),
        ...(form.preview_video_id ? { preview_video_id: form.preview_video_id } : {}),
        timezone: form.timezone,
      })
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Penyiapan awal gagal.')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="y-root">
      <div className="y-page" style={{ maxWidth: 640, margin: '0 auto' }}>
        <section className="y-card">
          <h1>Penyiapan awal</h1>
          <p className="y-card-note">
            Proses ini hanya dapat dijalankan sekali, selama belum ada akun administrator.
          </p>

          {error ? (
            <Banner tone="error" title="Penyiapan gagal">
              {error}
            </Banner>
          ) : null}

          <form className="y-stack" onSubmit={submit}>
            <div className="y-form">
              <div className="y-field">
                <label htmlFor="su-user">Nama pengguna</label>
                <input
                  id="su-user"
                  className="y-input"
                  autoComplete="username"
                  value={form.username}
                  onChange={(e) => set('username', e.target.value)}
                  required
                />
              </div>
              <div className="y-field">
                <label htmlFor="su-pass">Kata sandi</label>
                <input
                  id="su-pass"
                  className="y-input"
                  type="password"
                  autoComplete="new-password"
                  value={form.password}
                  onChange={(e) => set('password', e.target.value)}
                  required
                />
              </div>
              <div className="y-field">
                <label htmlFor="su-pass2">Ulangi kata sandi</label>
                <input
                  id="su-pass2"
                  className="y-input"
                  type="password"
                  autoComplete="new-password"
                  value={form.confirm_password}
                  onChange={(e) => set('confirm_password', e.target.value)}
                  required
                />
              </div>
              <div className="y-field">
                <label htmlFor="su-name">Nama server</label>
                <input
                  id="su-name"
                  className="y-input"
                  placeholder="YouTube Live Server"
                  value={form.instance_name}
                  onChange={(e) => set('instance_name', e.target.value)}
                />
              </div>
              <div className="y-field">
                <label htmlFor="su-tz">Zona waktu</label>
                <select
                  id="su-tz"
                  className="y-select"
                  value={form.timezone}
                  onChange={(e) => set('timezone', e.target.value)}
                >
                  {timeZoneOptions(form.timezone).map((z) => (
                    <option key={z} value={z}>
                      {z}
                    </option>
                  ))}
                </select>
              </div>
              <div className="y-field">
                <label htmlFor="su-dir">Lokasi penyimpanan media</label>
                <input
                  id="su-dir"
                  className="y-input"
                  placeholder="/var/lib/ylive/media"
                  value={form.media_dir}
                  onChange={(e) => set('media_dir', e.target.value)}
                />
              </div>
              <div className="y-field">
                <label htmlFor="su-iface">Interface jaringan</label>
                <select
                  id="su-iface"
                  className="y-select"
                  value={form.net_interface}
                  onChange={(e) => set('net_interface', e.target.value)}
                >
                  <option value="">Deteksi otomatis</option>
                  {interfaces.map((i) => (
                    <option key={i} value={i}>
                      {i}
                    </option>
                  ))}
                </select>
              </div>
              <div className="y-field is-wide">
                <label htmlFor="su-ingest">URL ingest RTMPS</label>
                <input
                  id="su-ingest"
                  className="y-input"
                  value={form.ingest_url}
                  onChange={(e) => set('ingest_url', e.target.value)}
                />
              </div>
              <div className="y-field">
                <label htmlFor="su-key">Stream key</label>
                <input
                  id="su-key"
                  className="y-input"
                  type="password"
                  autoComplete="off"
                  placeholder="Boleh diisi nanti di Pengaturan"
                  value={form.stream_key}
                  onChange={(e) => set('stream_key', e.target.value)}
                />
                <span className="y-hint">Tidak dapat dibaca kembali setelah disimpan.</span>
              </div>
              <div className="y-field">
                <label htmlFor="su-preview">URL atau ID video preview</label>
                <input
                  id="su-preview"
                  className="y-input"
                  value={form.preview_video_id}
                  onChange={(e) => set('preview_video_id', e.target.value)}
                />
              </div>
            </div>

            <Banner tone="info" title="Siapkan broadcast di YouTube Studio">
              Aplikasi ini hanya mengirim media ke ingest URL. Broadcast harus dibuat dan diperiksa
              sendiri di YouTube Studio, termasuk pengaturan mulai dan selesai otomatis.
            </Banner>

            <Button type="submit" busy={busy || loading} fullWidth>
              Selesaikan penyiapan
            </Button>
          </form>
        </section>
      </div>
    </div>
  )
}