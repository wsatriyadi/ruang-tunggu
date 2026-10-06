import { useState } from 'react'
import { Button } from '../components'
import { useSession } from '../state/useSession'
import { Banner } from './shared/ui'

export default function Login() {
  const { login, loading } = useSession()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await login({ username, password })
    } catch (err) {
      // Deliberately vague: the server never reveals whether a user exists.
      setError(err instanceof Error ? err.message : 'Gagal masuk.')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="y-root">
      <div className="y-page" style={{ maxWidth: 420, margin: '0 auto' }}>
        <section className="y-card">
          <h1>Masuk</h1>
          <p className="y-card-note">
            Sesi memakai cookie HttpOnly. Menutup peramban tidak menghentikan siaran yang sedang
            berjalan.
          </p>

          {error ? (
            <Banner tone="error" title="Gagal masuk">
              {error}
            </Banner>
          ) : null}

          <form className="y-stack" onSubmit={submit}>
            <div className="y-field">
              <label htmlFor="login-user">Nama pengguna</label>
              <input
                id="login-user"
                className="y-input"
                autoComplete="username"
                autoFocus
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                required
              />
            </div>
            <div className="y-field">
              <label htmlFor="login-pass">Kata sandi</label>
              <input
                id="login-pass"
                className="y-input"
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
              />
            </div>
            <Button type="submit" busy={busy || loading} fullWidth>
              Masuk
            </Button>
          </form>
        </section>
      </div>
    </div>
  )
}