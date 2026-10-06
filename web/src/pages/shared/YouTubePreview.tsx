import type { ReactNode } from 'react'
import { useState } from 'react'

const STUDIO_URL = 'https://studio.youtube.com/'

/**
 * YouTube preview with an explicit thumbnail fallback (PRD §14, AC-18).
 *
 * A cross-origin iframe cannot be inspected, so the iframe `load` event alone
 * cannot prove playback: it fires even when YouTube renders an error inside the
 * frame. The UI therefore keeps the fallback one click away and labels the
 * static image as a thumbnail, never as live video.
 */
export function YouTubePreview({
  previewVideoId,
  thumbnailUrl,
  thumbnailLabel = 'Thumbnail video aktif',
}: {
  previewVideoId: string | undefined
  thumbnailUrl: string | undefined
  thumbnailLabel?: string
}) {
  const [showEmbed, setShowEmbed] = useState(true)
  const embedSrc = previewVideoId
    ? `https://www.youtube-nocookie.com/embed/${encodeURIComponent(previewVideoId)}`
    : null

  if (!embedSrc) {
    return (
      <div className="y-stack">
        <span className="y-card-note">Preview YouTube</span>
        <ThumbnailBox url={thumbnailUrl} label={thumbnailLabel} />
        <p className="y-card-note">
          Belum ada video ID preview. Isi <strong>URL/ID video preview</strong> di halaman Pengaturan.
        </p>
        <StudioLink />
      </div>
    )
  }

  return (
    <div className="y-stack">
      <span className="y-card-note">Preview YouTube</span>
      {showEmbed ? (
        <>
          <div className="y-preview-frame">
            {/* No referrer policy attribute: setting one breaks the YouTube
                player with error 153. */}
            <iframe
              className="y-embed"
              src={embedSrc}
              title="Preview YouTube"
              allow="accelerometer; autoplay; clipboard-write; encrypted-media; picture-in-picture"
              allowFullScreen
            />
          </div>
          <p className="y-card-note">
            Pratinjau memakai embed youtube-nocookie. Bila video tidak tampil karena diblokir jaringan,
            tampilkan thumbnail sebagai gantinya.
          </p>
          <div className="y-btn-row">
            <button type="button" className="y-btn is-sm is-ghost" onClick={() => setShowEmbed(false)}>
              Tampilkan thumbnail
            </button>
          </div>
        </>
      ) : (
        <div className="y-stack">
          <ThumbnailBox url={thumbnailUrl} label={thumbnailLabel} />
          <p className="y-card-note">
            Embed tidak ditampilkan. Gambar di atas adalah <strong>{thumbnailLabel.toLowerCase()}</strong>,
            bukan video langsung.
          </p>
          <div className="y-btn-row">
            <button type="button" className="y-btn is-sm" onClick={() => setShowEmbed(true)}>
              Coba embed lagi
            </button>
          </div>
        </div>
      )}
      <StudioLink />
    </div>
  )
}

function StudioLink() {
  return (
    <a className="y-btn is-sm is-ghost" href={STUDIO_URL} target="_blank" rel="noreferrer noopener">
      Buka YouTube Studio
    </a>
  )
}

function ThumbnailBox({ url, label }: { url: string | undefined; label: string }) {
  const [failed, setFailed] = useState(false)
  if (!url || failed) {
    return (
      <div className="y-preview-frame" style={{ padding: 16, textAlign: 'center' }}>
        <Note>{failed ? 'Gambar tidak dapat dimuat.' : `${label}: belum ada gambar.`}</Note>
      </div>
    )
  }
  return (
    <div className="y-preview-frame">
      <img src={url} alt={label} style={{ width: '100%', display: 'block' }} onError={() => setFailed(true)} />
    </div>
  )
}

function Note({ children }: { children: ReactNode }) {
  return <span className="y-card-note">{children}</span>
}