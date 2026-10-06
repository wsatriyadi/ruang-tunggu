# API Contract — YouTube Live Server

Base path `/api`. All responses are JSON unless noted. The backend is complete
and compiles; this file is the contract the frontend is written against.

## Auth

Every endpoint except the three below requires the `ytls_session` cookie
(HttpOnly, SameSite=Strict). Mutations additionally require the
`X-CSRF-Token` header, whose value is returned once at login/setup as
`csrf_token` and kept in memory by the client.

| Method | Path | Body | Notes |
|---|---|---|---|
| GET | `/auth/state` | — | `{state: "setup"\|"login"\|"authenticated", app, version}` |
| POST | `/auth/setup` | `{username, password, confirm_password, instance_name?, timezone?, media_dir?, net_interface?, ingest_url?, stream_key?, preview_video_id?}` | Only works while no admin exists. Sets cookie, returns `{username, csrf_token}` |
| POST | `/auth/login` | `{username, password}` | `{username, csrf_token}` |
| POST | `/auth/logout` | — | |
| POST | `/auth/password` | `{current_password, new_password, confirm_password}` | |
| GET | `/events` | — | **SSE.** See below |

### SSE envelope

`text/event-stream`. Each message is `data: {"kind":"...","payload":{...}}`.
Kinds: `metrics`, `stream_state`, `log`, `media`, `schedule`, `alert`.
A `: keep-alive` comment arrives every 20 s. Reconnect on drop.

`stream_state` payload is the `Progress` object below.

## Dashboard

`GET /dashboard` →
```ts
{
  instance: { name, timezone, version },
  system: {
    cpu_percent: number|null, mem_total_bytes, mem_available_bytes,
    mem_used_percent: number|null, temperature_celsius: number|null,
    uptime_seconds, process_rss_bytes, stale: boolean, stale_after_seconds,
    network: { interface, upload_bps, download_bps, upload_mbps } | null,
    history: [{ t: ms_offset, cpu?, mem?, up? }]
  },
  stream: Progress,
  youtube: { verified: false, state: "unverified", message, preview_video_id? },
  media: { total, ready, processing, invalid, missing, needs_review,
           total_bytes, total_duration_seconds },
  playlists: [{ id, name, item_count, cycle_duration_seconds, playback_mode,
                loop_enabled, compatibility, revision_number }],
  schedules: [{ id, name, start_at, end_at, timezone, status, enabled, is_running }],
  dependencies: { ffmpeg, ffprobe, ffmpeg_path, ffprobe_path,
                  ffmpeg_version, ffprobe_version, ready },
  disk_space: { path, total_bytes, free_bytes, used_bytes, used_percent } | undefined
}
```

## Progress (stream state)

```ts
{
  session_id, state, desired_state,
  cycle, item_index, item_count, item_name?, item_thumbnail?,
  position_seconds, item_duration,
  session_duration, connection_duration,   // SECONDS, distinct meanings
  attempt, next_retry_at?,                   // ISO string
  error_code?, error_message?,
  publisher_bitrate_kbps, media_time_seconds,
  started_at, updated_at
}
```

`state` ∈ `IDLE | VALIDATING | STARTING | STREAMING | RECONNECTING |
STOPPING | STOPPED | COMPLETED | FAILED`. **Local publisher state only — never
implies the broadcast is on air on YouTube.**

## Uploads

| Method | Path | Notes |
|---|---|---|
| POST | `/uploads` | `{filename, size, content_type}` → `{upload_id, part_size, total_parts, expires_at}` |
| GET | `/uploads/{id}` | `{status, expected_size, received_bytes, percent, received_parts[], media_id, error}` |
| PUT | `/uploads/{id}/parts/{index}` | Raw body. Send `X-Part-Offset` header. → `{part, bytes, received_bytes, percent}` |
| POST | `/uploads/{id}/complete` | → `{media_id, status:"processing"}` |
| DELETE | `/uploads/{id}` | |

Default `part_size` is 8 MiB. The client must send one part at a time per
upload and retry failed parts. Resume by `GET /uploads/{id}` and re-sending
only the missing indexes.

## Media

`GET /videos?q=&limit=50&offset=0` → `{items: Media[], total, limit, offset}`

```ts
Media = {
  id, original_name, size, duration, container,
  video_codec?, audio_codec?, width?, height?, resolution?, fps?,
  bitrate?, sample_rate?, channels?, pix_fmt?,
  has_video, has_audio, file_status, compatibility,
  issues?: [{field, severity, message}],   // blocking|warning|notice
  thumbnail_url?, has_thumbnail, created_at, updated_at, keyframe_max_gap?,
  usage?: [{kind, id, name}]
}
```

`file_status` ∈ `uploading|processing|ready|invalid|missing`.
`compatibility` ∈ `unchecked|compatible|incompatible|needs_review`.

| Method | Path | Notes |
|---|---|---|
| GET | `/videos/{id}` | Includes `usage` |
| GET | `/videos/{id}/content` | Binary, byte-range. Local preview |
| GET | `/videos/{id}/thumbnail` | JPEG |
| DELETE | `/videos/{id}` | 409 `MEDIA_IN_USE` with `details.usage[]` when referenced |
| POST | `/videos/{id}/recheck` | |

## Playlists

| Method | Path | Body |
|---|---|---|
| GET | `/playlists` | `{items: Playlist[]}` |
| POST | `/playlists` | `{name, playback_mode?, loop_enabled?, media_ids?}` |
| PUT | `/playlists/{id}` | `{name, playback_mode, loop_enabled, media_ids[]}` → creates a new revision |
| DELETE | `/playlists/{id}` | 409 when schedules pin a revision |

```ts
Playlist = {
  id, name, item_count, cycle_duration, loop_enabled, playback_mode,
  revision_id, latest_revision_id, revision_number,
  compatibility, issues?: [{media_id, name, field, severity, message, reference_media_id?}],
  created_at, updated_at,
  items: [{ position, media_id, name, duration, file_status, compatibility,
            thumbnail_url?, has_thumbnail }]
}
```

`playback_mode` ∈ `sequential | random | random_no_repeat`. Each save creates a
new revision; `media_ids` must not contain duplicates.

## Schedules

| Method | Path | Body |
|---|---|---|
| GET | `/schedules` | `{items: Schedule[]}` |
| POST | `/schedules` | see below |
| GET | `/schedules/{id}` | `{schedule, playlist}` |
| PATCH | `/schedules/{id}` | same body; 409 `SCHEDULE_RUNNING` while running |
| DELETE | `/schedules/{id}` | 409 while running |

```ts
Schedule = {
  id, name, revision_id, revision_number, playlist_name, target_id,
  start_at, end_at,            // ISO, UTC
  timezone,                    // IANA, e.g. "Asia/Makassar"
  playback_mode, loop_enabled, auto_reconnect, enabled,
  status, message, is_running, has_newer_revision, created_at, updated_at
}
```

Create body: `{name, revision_id, start_at, end_at, timezone, playback_mode,
loop_enabled, auto_reconnect, enabled}`.

`status` ∈ `disabled|scheduled|running|completed|stopped|failed|missed|skipped_busy`.
Overlapping windows → 409 `SCHEDULE_CONFLICT` with `details.conflicting[]`.
Send `start_at`/`end_at` as ISO strings **with offset** (e.g.
`2026-10-06T20:00:00+08:00`) so the chosen timezone is honoured.

## Stream control

| Method | Path | Body | Notes |
|---|---|---|---|
| POST | `/stream/preflight` | `{revision_id?, mode?, loop?, end_at?}` | Returns `{ok, code?, message?, issues[], warnings[], details}`. `issues[]` = `{field, message, media_id?, name?}` |
| POST | `/stream/start` | `{revision_id?, mode?, loop?, end_at?, idempotency_key}` | 202 `{session_id, state, accepted}`; 409 with `error.details` on preflight failure |
| POST | `/stream/stop` | `{idempotency_key?}` | `{ok, was_active, state}` |
| POST | `/stream/restart` | `{idempotency_key?}` | 202 |
| GET | `/stream/status` | | `{stream, busy, dependencies, server_time, youtube, session?, running_schedule?}` |

**`idempotency_key` is required for start** and should be a UUID generated once
per user intent, reused across retries — a double-click or a second tab then
produces one session. Stop and restart also accept it.

## Logs

| Method | Path | Notes |
|---|---|---|
| GET | `/logs?levels=&sources=&session_id=&q=&limit=200` | `{entries, count, sources, levels}` |
| GET | `/logs/export?levels=&sources=&q=&limit=2000` | `text/plain` attachment, already redacted |
| POST | `/logs/clear` | Clears the **browser view only**; server history is kept |
| GET | `/audit?limit=100` | `{items: AuditEvent[]}` |

Entry: `{ts, level, source, session_id?, message}`.
`source` ∈ `app|scheduler|publisher|audit|upload`; `level` ∈ `debug|info|warn|error`.
Multi-value filters are comma-separated.

## Settings

`GET /settings` → `{settings: Settings, target, stream_key_set, defaults:{ingest_url, profile_id}, profiles: Profile}`

`Settings` keys: `instance_name, language, timezone, ingest_url, stream_key_set,
preview_video_id, profile_id, media_dir, quota_bytes, max_file_size,
disk_reserve_bytes, ffmpeg_path, ffprobe_path, resume_after_restart,
auto_reconnect, stall_timeout_seconds, max_attempts, net_interface,
sample_interval_seconds, warn_cpu_percent, warn_disk_percent, log_max_bytes,
log_max_days`.

`PUT /settings` body `{settings: Settings, stream_key?: string}`.
`stream_key` is **write-only**; the API never returns it. Blank means keep.
200 → `{ok, stream_key_set}`.

`GET /settings/deps` → dependency object as above.

`GET /system` → `{info:{hostname, os, arch, kernel, soc, cpu_model, cpu_cores,
total_ram_bytes, boot_time}, interfaces:[], selected}`

## Errors

Every failure returns:
```json
{"error": {"code": "STREAM_BUSY", "message": "…human readable, Indonesian…",
           "details": {}}}
```

Codes: `STREAM_BUSY, SCHEDULE_CONFLICT, MEDIA_INCOMPATIBLE, MEDIA_IN_USE,
INSUFFICIENT_STORAGE, INVALID_TARGET, DEPENDENCY_MISSING, PLAYLIST_EMPTY,
PUBLISHER_FAILED, DEADLINE_REACHED, PREFLIGHT_FAILED, UNAUTHENTICATED,
CSRF_INVALID, VALIDATION, SCHEDULE_RUNNING, MEDIA_BUSY, NOT_FOUND, INTERNAL`.

Status: 400 validation, 401 no session, 403 CSRF, 404 missing, 409 conflict,
413 too large, 422 unusable value, 507 storage full.

## Media profile facts the UI may surface

Accepted: H.264 video + AAC audio, yuv420p, ≤1920px, ≤60 fps,
44.1/48 kHz stereo. Keyframe target 2 s, hard ceiling 4 s.
YouTube's recommended H.264 bitrate: 1080p60 = 17 Mbps, 1080p30 = 14 Mbps,
720p = 8 Mbps, 480p/360p = 4 Mbps.
Both a video AND an audio track are mandatory — audio-only files are rejected.