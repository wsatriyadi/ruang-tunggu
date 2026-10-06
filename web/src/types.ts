/**
 * Wire types mirroring docs/API.md.
 *
 * Every enum member is the exact lowercase/uppercase string the Go backend
 * emits (internal/repo, internal/streaming, internal/logging). Optional
 * fields use `?` because Go's `omitempty` drops them entirely.
 */

/* ------------------------------------------------------------------ */
/* Auth                                                                */
/* ------------------------------------------------------------------ */

export type AuthState = 'setup' | 'login' | 'authenticated';

export interface AuthStateResponse {
  state: AuthState;
  version: string;
  app: string;
}

export interface SessionResponse {
  username: string;
  csrf_token: string;
}

export interface SetupRequest {
  username: string;
  password: string;
  confirm_password: string;
  instance_name?: string;
  timezone?: string;
  media_dir?: string;
  net_interface?: string;
  ingest_url?: string;
  stream_key?: string;
  preview_video_id?: string;
}

export interface LoginRequest {
  username: string;
  password: string;
}

export interface PasswordRequest {
  current_password: string;
  new_password: string;
  confirm_password: string;
}

/* ------------------------------------------------------------------ */
/* Errors                                                              */
/* ------------------------------------------------------------------ */

export type ApiErrorCode =
  | 'STREAM_BUSY'
  | 'SCHEDULE_CONFLICT'
  | 'MEDIA_INCOMPATIBLE'
  | 'MEDIA_IN_USE'
  | 'INSUFFICIENT_STORAGE'
  | 'INVALID_TARGET'
  | 'DEPENDENCY_MISSING'
  | 'PLAYLIST_EMPTY'
  | 'PUBLISHER_FAILED'
  | 'DEADLINE_REACHED'
  | 'PREFLIGHT_FAILED'
  | 'UNAUTHENTICATED'
  | 'CSRF_INVALID'
  | 'VALIDATION'
  | 'SCHEDULE_RUNNING'
  | 'MEDIA_BUSY'
  | 'NOT_FOUND'
  | 'INTERNAL'
  // Emitted by the auth and upload handlers in addition to the list above.
  | 'INVALID_REQUEST'
  | 'LOGIN_FAILED'
  | 'PASSWORD_MISMATCH'
  | 'SETUP_FAILED'
  | 'FILE_TOO_LARGE'
  | 'UPLOAD_REJECTED'
  | 'UPLOAD_INCOMPLETE'
  | 'UPLOAD_NOT_ACTIVE'
  | 'UPLOAD_FAILED'
  | 'RESTART_SUPERSEDED'
  | (string & {});

/* ------------------------------------------------------------------ */
/* Media                                                               */
/* ------------------------------------------------------------------ */

export type FileStatus =
  | 'uploading'
  | 'processing'
  | 'ready'
  | 'invalid'
  | 'missing';

export type CompatibilityStatus =
  | 'unchecked'
  | 'compatible'
  | 'incompatible'
  | 'needs_review';

export type IssueSeverity = 'blocking' | 'warning' | 'notice';

export interface MediaIssue {
  field: string;
  severity: IssueSeverity;
  message: string;
}

export interface MediaUsage {
  kind: string;
  id: string;
  name: string;
}

export interface Media {
  id: string;
  original_name: string;
  size: number;
  duration: number;
  container: string;
  video_codec?: string;
  audio_codec?: string;
  width?: number;
  height?: number;
  resolution?: string;
  fps?: number;
  bitrate?: number;
  sample_rate?: number;
  channels?: number;
  pix_fmt?: string;
  has_video: boolean;
  has_audio: boolean;
  file_status: FileStatus;
  compatibility: CompatibilityStatus;
  issues?: MediaIssue[];
  thumbnail_url?: string;
  has_thumbnail: boolean;
  created_at: string;
  updated_at: string;
  keyframe_max_gap?: number;
  usage?: MediaUsage[];
}

export interface VideoListResponse {
  items: Media[];
  total: number;
  limit: number;
  offset: number;
}

export interface VideoListParams {
  q?: string;
  limit?: number;
  offset?: number;
}

/* ------------------------------------------------------------------ */
/* Uploads                                                             */
/* ------------------------------------------------------------------ */

export interface CreateUploadRequest {
  filename: string;
  size: number;
  content_type: string;
}

export interface CreateUploadResponse {
  upload_id: string;
  part_size: number;
  total_parts: number;
  expires_at: string;
}

export type UploadStatus =
  | 'active'
  | 'processing'
  | 'complete'
  | 'failed'
  | 'cancelled'
  | (string & {});

export interface UploadStatusResponse {
  upload_id: string;
  filename: string;
  status: UploadStatus;
  expected_size: number;
  received_bytes: number;
  percent: number;
  received_parts: number[];
  media_id: string;
  error: string;
}

export interface UploadPartResponse {
  part: number;
  bytes: number;
  received_bytes: number;
  percent: number;
}

export interface CompleteUploadResponse {
  media_id: string;
  status: 'processing';
}

/* ------------------------------------------------------------------ */
/* Playlists                                                           */
/* ------------------------------------------------------------------ */

export type PlaybackMode = 'sequential' | 'random' | 'random_no_repeat';

export interface PlaylistIssue {
  media_id: string;
  name: string;
  field: string;
  severity: IssueSeverity;
  message: string;
  reference_media_id?: string;
}

export interface PlaylistItemRow {
  position: number;
  media_id: string;
  name: string;
  duration: number;
  file_status: FileStatus;
  compatibility: CompatibilityStatus;
  thumbnail_url?: string;
  has_thumbnail: boolean;
}

export interface Playlist {
  id: string;
  name: string;
  item_count: number;
  cycle_duration: number;
  loop_enabled: boolean;
  playback_mode: PlaybackMode;
  revision_id: string;
  latest_revision_id: string;
  revision_number: number;
  compatibility: CompatibilityStatus;
  issues?: PlaylistIssue[];
  created_at: string;
  updated_at: string;
  items?: PlaylistItemRow[];
}

export interface PlaylistListResponse {
  items: Playlist[];
}

export interface PlaylistRequest {
  name: string;
  playback_mode?: PlaybackMode;
  loop_enabled?: boolean;
  media_ids?: string[];
}

export interface PlaylistUpdateRequest {
  name: string;
  playback_mode: PlaybackMode;
  loop_enabled: boolean;
  media_ids: string[];
}

/* ------------------------------------------------------------------ */
/* Schedules                                                           */
/* ------------------------------------------------------------------ */

export type ScheduleStatus =
  | 'disabled'
  | 'scheduled'
  | 'running'
  | 'completed'
  | 'stopped'
  | 'failed'
  | 'missed'
  | 'skipped_busy';

export interface Schedule {
  id: string;
  name: string;
  revision_id: string;
  revision_number: number;
  playlist_name: string;
  target_id: string;
  /** ISO-8601, UTC on the wire. */
  start_at: string;
  /** ISO-8601, UTC on the wire. */
  end_at: string;
  /** IANA zone, e.g. "Asia/Makassar". */
  timezone: string;
  playback_mode: PlaybackMode;
  loop_enabled: boolean;
  auto_reconnect: boolean;
  enabled: boolean;
  status: ScheduleStatus;
  message: string;
  execution_key?: string;
  session_id?: string;
  is_running: boolean;
  has_newer_revision: boolean;
  created_at: string;
  updated_at: string;
}

export interface ScheduleListResponse {
  items: Schedule[];
}

export interface ScheduleDetailResponse {
  schedule: Schedule;
  playlist?: Playlist;
}

export interface ScheduleRequest {
  name: string;
  revision_id: string;
  /** Send WITH an offset (e.g. 2026-10-06T20:00:00+08:00) so the zone applies. */
  start_at: string;
  end_at: string;
  timezone: string;
  playback_mode: PlaybackMode;
  loop_enabled: boolean;
  auto_reconnect: boolean;
  enabled: boolean;
  target_id?: string;
}

/* ------------------------------------------------------------------ */
/* Stream control                                                      */
/* ------------------------------------------------------------------ */

export type StreamState =
  | 'IDLE'
  | 'VALIDATING'
  | 'STARTING'
  | 'STREAMING'
  | 'RECONNECTING'
  | 'STOPPING'
  | 'STOPPED'
  | 'COMPLETED'
  | 'FAILED';

export type DesiredState = 'running' | 'stopped' | (string & {});

/**
 * The live publisher view. Local state only: it never asserts that the
 * broadcast is on air on YouTube (PRD FR-06, AC-18).
 */
export interface Progress {
  session_id: string;
  state: StreamState;
  desired_state: DesiredState;
  /** 1-based pass number. */
  cycle: number;
  /** 0-based position within the cycle. */
  item_index: number;
  item_count: number;
  item_name?: string;
  position_seconds: number;
  item_duration: number;
  /** Wall time since the session started, in seconds. */
  session_duration: number;
  /** Wall time since the current publisher attempt began, in seconds. */
  connection_duration: number;
  attempt: number;
  next_retry_at?: string;
  error_code?: string;
  error_message?: string;
  publisher_bitrate_kbps: number;
  media_time_seconds: number;
  started_at?: string;
  updated_at: string;
}

export interface PreflightIssue {
  field: string;
  message: string;
  media_id?: string;
  name?: string;
}

export interface PreflightResult {
  ok: boolean;
  code?: string;
  message?: string;
  details?: Record<string, unknown>;
  issues?: PreflightIssue[];
  warnings?: string[];
}

export interface StartStreamRequest {
  revision_id?: string;
  mode?: PlaybackMode;
  loop?: boolean;
  end_at?: string;
  /** Required for start: one UUID per user intent, reused across retries. */
  idempotency_key: string;
}

export interface StartStreamResponse {
  session_id: string;
  state: StreamState;
  accepted: boolean;
}

export interface StopStreamRequest {
  idempotency_key?: string;
}

export interface StopStreamResponse {
  ok: boolean;
  was_active: boolean;
  state: StreamState;
}

export interface RestartStreamRequest {
  idempotency_key?: string;
}

export interface RestartStreamResponse {
  ok: boolean;
  state: StreamState;
}

export interface StreamStatusResponse {
  stream: Progress;
  busy: boolean;
  dependencies: DependencyStatus;
  server_time: string;
  youtube: YouTubeStatus;
  session?: SessionRecord;
  running_schedule?: Schedule;
}

export interface SessionRecord {
  id: string;
  schedule_id?: string;
  name: string;
  desired_state: string;
  observed_state: string;
  start_at?: string;
  end_at?: string;
  stop_reason?: string;
  owner_epoch?: string;
  current_item: number;
  current_cycle: number;
  position_seconds: number;
  error_code?: string;
  error_message?: string;
  created_at: string;
  updated_at: string;
}

/* ------------------------------------------------------------------ */
/* Dashboard                                                           */
/* ------------------------------------------------------------------ */

export interface YouTubeStatus {
  /** Always false in this release: no YouTube API is connected. */
  verified: boolean;
  state: string;
  message: string;
  preview_video_id?: string;
}

export interface InstanceInfo {
  name: string;
  timezone: string;
  version: string;
}

export interface NetworkSample {
  interface: string;
  upload_bps: number;
  download_bps: number;
  upload_mbps: number;
}

export interface DiskSample {
  path: string;
  total_bytes: number;
  free_bytes: number;
  used_bytes: number;
  used_percent: number;
}

export interface MonitoringSample {
  /** Offset in milliseconds from the first retained sample. */
  t: number;
  cpu?: number;
  mem?: number;
  up?: number;
}

export interface SystemMetrics {
  cpu_percent: number | null;
  mem_total_bytes: number;
  mem_available_bytes: number;
  mem_used_percent: number | null;
  /** null when the device has no readable sensor (PRD §4). */
  temperature_celsius: number | null;
  uptime_seconds: number;
  process_rss_bytes: number;
  stale: boolean;
  stale_after_seconds: number;
  network?: NetworkSample;
  history?: MonitoringSample[];
}

export interface MediaSummary {
  total: number;
  ready: number;
  processing: number;
  invalid: number;
  missing: number;
  needs_review: number;
  total_bytes: number;
  total_duration_seconds: number;
}

export interface PlaylistBrief {
  id: string;
  name: string;
  item_count: number;
  cycle_duration_seconds: number;
  playback_mode: PlaybackMode;
  loop_enabled: boolean;
  compatibility: CompatibilityStatus;
  revision_number: number;
}

export interface ScheduleBrief {
  id: string;
  name: string;
  start_at: string;
  end_at: string;
  timezone: string;
  status: ScheduleStatus;
  enabled: boolean;
  is_running: boolean;
}

export interface DependencyStatus {
  ffmpeg: boolean;
  ffprobe: boolean;
  ffmpeg_path: string;
  ffprobe_path: string;
  ffmpeg_version: string;
  ffprobe_version: string;
  ready: boolean;
}

export interface Dashboard {
  instance: InstanceInfo;
  system: SystemMetrics;
  stream: Progress;
  youtube: YouTubeStatus;
  media: MediaSummary;
  playlists: PlaylistBrief[];
  schedules: ScheduleBrief[];
  dependencies: DependencyStatus;
  disk_space?: DiskSample;
}

/* ------------------------------------------------------------------ */
/* System                                                              */
/* ------------------------------------------------------------------ */

export interface SystemInfo {
  hostname: string;
  os: string;
  arch: string;
  kernel: string;
  soc: string;
  cpu_model: string;
  cpu_cores: number;
  total_ram_bytes: number;
  boot_time: string;
}

export interface SystemResponse {
  info: SystemInfo;
  interfaces: string[];
  selected: string;
}

/* ------------------------------------------------------------------ */
/* Logs & audit                                                        */
/* ------------------------------------------------------------------ */

export type LogLevel = 'debug' | 'info' | 'warn' | 'error';
export type LogSource = 'app' | 'scheduler' | 'publisher' | 'audit' | 'upload';

export interface LogEntry {
  ts: string;
  level: LogLevel;
  source: LogSource;
  session_id?: string;
  message: string;
}

export interface LogsResponse {
  entries: LogEntry[];
  count: number;
  sources: LogSource[];
  levels: LogLevel[];
}

export interface LogListParams {
  /** Comma-separated multi-value filter. */
  levels?: string;
  sources?: string;
  session_id?: string;
  q?: string;
  limit?: number;
  since?: string;
}

export interface AuditEvent {
  id: number;
  actor_id: number;
  action: string;
  entity_type: string;
  entity_id: string;
  metadata?: Record<string, unknown>;
  created_at: string;
}

export interface AuditResponse {
  items: AuditEvent[];
}

/* ------------------------------------------------------------------ */
/* Settings & target                                                   */
/* ------------------------------------------------------------------ */

export interface Settings {
  instance_name: string;
  language: string;
  timezone: string;
  ingest_url: string;
  stream_key_set: boolean;
  preview_video_id: string;
  profile_id: string;
  media_dir: string;
  quota_bytes: number;
  max_file_size: number;
  disk_reserve_bytes: number;
  ffmpeg_path: string;
  ffprobe_path: string;
  resume_after_restart: boolean;
  auto_reconnect: boolean;
  stall_timeout_seconds: number;
  max_attempts: number;
  net_interface: string;
  sample_interval_seconds: number;
  warn_cpu_percent: number;
  warn_disk_percent: number;
  log_max_bytes: number;
  log_max_days: number;
}

export interface StreamTarget {
  id: string;
  name: string;
  ingest_url: string;
  preview_video_id: string;
  profile_id: string;
  version: number;
  stream_key_set: boolean;
  updated_at: string;
}

export interface ProfileRules {
  video_codec: string;
  audio_codecs: string[];
  pixel_formats: string[];
  max_resolution: number;
  max_fps: number;
  sample_rates: number[];
  preferred_sample_rate: number;
  channels: number;
  max_keyframe_gap: number;
  target_keyframe_gap: number;
}

export interface Profile {
  id: string;
  name: string;
  version: number;
  transport: string;
  rules: ProfileRules;
}

export interface SettingsResponse {
  settings: Settings;
  target?: StreamTarget;
  stream_key_set: boolean;
  defaults: { ingest_url: string; profile_id: string };
  profiles: Profile;
}

export interface SaveSettingsRequest {
  settings: Settings;
  /** Write-only. Blank keeps the stored key; the API never returns it. */
  stream_key?: string;
}

export interface SaveSettingsResponse {
  ok: boolean;
  stream_key_set: boolean;
}

/* ------------------------------------------------------------------ */
/* SSE                                                                 */
/* ------------------------------------------------------------------ */

export type EventKind =
  | 'metrics'
  | 'stream_state'
  | 'log'
  | 'media'
  | 'schedule'
  | 'alert';

export interface MetricsEventPayload extends Partial<SystemMetrics> {
  publisher_bitrate_kbps?: number;
}

export interface MediaEventPayload {
  action: string;
  id: string;
}

export interface ScheduleEventPayload {
  action: string;
  id: string;
}

export interface AlertEventPayload {
  level?: LogLevel;
  message: string;
}

export interface EventPayloadMap {
  metrics: MetricsEventPayload;
  stream_state: Progress;
  log: LogEntry;
  media: MediaEventPayload;
  schedule: ScheduleEventPayload;
  alert: AlertEventPayload;
}