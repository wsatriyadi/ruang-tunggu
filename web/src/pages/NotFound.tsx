import { Link } from 'react-router-dom'
import { EmptyState } from '../components'

export default function NotFound() {
  return (
    <div className="y-root">
      <div className="y-page">
        <section className="y-card">
          <h1>Halaman tidak ditemukan</h1>
          <EmptyState
            title="404"
            description="Alamat yang diminta tidak tersedia di aplikasi ini."
            action={
              <Link className="y-btn is-primary" to="/">
                Kembali ke Dashboard
              </Link>
            }
          />
        </section>
      </div>
    </div>
  )
}