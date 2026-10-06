/**
 * Typed wrapper over docs/API.md.
 *
 * One `request` helper owns the session cookie (credentials: same-origin),
 * the CSRF header for every non-GET, and the `{error:{code,message,details}}`
 * envelope. Nothing else in the app builds a URL by hand.
 *
 * The session itself is an HttpOnly cookie the browser sends automatically.
 * Only the CSRF token is held client-side: in memory, mirrored to
 * sessionStorage so a reload keeps mutating without a re-login.
 */

import type {
  AuditResponse,
  CompleteUploadResponse,
  CreateUploadRequest,
  CreateUploadResponse,
  Dashboard,
  DependencyStatus,
  LoginRequest,
  LogsResponse,
  Media,
  PasswordRequest,
  Playlist,
  PlaylistListResponse,
  PlaylistRequest,
  PlaylistUpdateRequest,
  PreflightResult,
  RestartStreamRequest,
  RestartStreamResponse,
  SaveSettingsRequest,
  SaveSettingsResponse,
  Schedule,
  ScheduleDetailResponse,
  ScheduleListResponse,
  ScheduleRequest,
  SessionResponse,
  SettingsResponse,
  SetupRequest,
  StartStreamRequest,
  StartStreamResponse,
  StopStreamRequest,
  StopStreamResponse,
  StreamStatusResponse,
  SystemResponse,
  UploadPartResponse,
  UploadStatusResponse,
  AuthStateResponse,
  PlaybackMode,
} from '../types';

const API_BASE = '/api';

/* ------------------------------------------------------------------ */
/* Errors                                                              */
/* ------------------------------------------------------------------ */

export class ApiError extends Error {
  /** Stable API code; '' when the response carried none. */
  readonly code: string;
  /** Structured context: `{usage:[]}`, `{conflicting:[]}`, `{schedules:[]}`. */
  readonly details: Record<string, unknown>;
  /** HTTP status, or 0 when the request never reached the server. */
  readonly status: number;

  constructor(code: string, message: string, details: Record<string, unknown> = {}, status = 0) {
    super(message);
    this.name = 'ApiError';
    this.code = code;
    this.details = details;
    this.status = status;
  }
}

export function isApiError(e: unknown): e is ApiError {
  return e instanceof ApiError;
}

/** A message safe to show an operator, whatever was thrown. */
export function errorMessage(e: unknown, fallback = 'Terjadi kesalahan.'): string {
  if (isApiError(e) && e.message) return e.message;
  if (e instanceof Error && e.message) return e.message;
  return fallback;
}

/** One UUID per user intent, reused across retries of the same action. */
export function newIdempotencyKey(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID();
  }
  // RFC 4122 v4 shape; only reached on a context without randomUUID.
  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, (c) => {
    const r = (Math.random() * 16) | 0;
    const v = c === 'x' ? r : (r & 0x3) | 0x8;
    return v.toString(16);
  });
}

/* ------------------------------------------------------------------ */
/* CSRF token                                                          */
/* ------------------------------------------------------------------ */

const CSRF_STORAGE_KEY = 'ytls.csrf';

function readStoredToken(): string | null {
  try {
    return sessionStorage.getItem(CSRF_STORAGE_KEY);
  } catch {
    // Blocked storage partition: fall back to memory for this tab only.
    return null;
  }
}

let csrfToken: string | null = readStoredToken();

export function getCsrfToken(): string | null {
  if (csrfToken === null) csrfToken = readStoredToken();
  return csrfToken;
}

export function setCsrfToken(token: string): void {
  csrfToken = token;
  try {
    sessionStorage.setItem(CSRF_STORAGE_KEY, token);
  } catch {
    // Memory-only still covers the live session.
  }
}

export function clearCsrfToken(): void {
  csrfToken = null;
  try {
    sessionStorage.removeItem(CSRF_STORAGE_KEY);
  } catch {
    // Nothing to clean up.
  }
}

/* ------------------------------------------------------------------ */
/* Request helper                                                      */
/* ------------------------------------------------------------------ */

interface RequestOptions extends Omit<RequestInit, 'body'> {
  body?: unknown;
}

/** The one failure shape docs/API.md promises. */
interface ErrorEnvelope {
  error: {
    code?: unknown;
    message?: unknown;
    details?: unknown;
  };
}

function hasErrorEnvelope(body: unknown): body is ErrorEnvelope {
  return typeof body === 'object' && body !== null && 'error' in body;
}

/** Every field is `typeof`-checked, so no unchecked cast is involved. */
async function parseError(res: Response): Promise<ApiError> {
  let code = '';
  let message = '';
  let details: Record<string, unknown> = {};

  try {
    const body: unknown = await res.json();
    if (hasErrorEnvelope(body)) {
      const env = body.error;
      if (typeof env?.code === 'string') code = env.code;
      if (typeof env?.message === 'string') message = env.message;
      const { details: rawDetails } = env;
      if (rawDetails !== null && typeof rawDetails === 'object' && !Array.isArray(rawDetails)) {
        details = { ...rawDetails };
      }
    }
  } catch {
    // A non-JSON body (proxy error page, dropped connection).
  }

  if (!message) {
    message =
      res.status === 401
        ? 'Sesi berakhir. Silakan masuk kembali.'
        : res.status === 403
          ? 'Permintaan ditolak. Muat ulang halaman lalu coba lagi.'
          : `Permintaan gagal (HTTP ${res.status}).`;
  }
  return new ApiError(code, message, details, res.status);
}

/**
 * Performs one API call. Sends the session cookie always, and the CSRF header
 * on every method except GET/HEAD, which the backend does not check.
 */
export async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const { body, headers, signal, ...rest } = options;
  const method = (rest.method ?? 'GET').toUpperCase();
  const isRawBody = body instanceof Blob || body instanceof ArrayBuffer;

  const finalHeaders = new Headers(headers);
  finalHeaders.set('Accept', 'application/json');
  if (body !== undefined && !isRawBody) finalHeaders.set('Content-Type', 'application/json');
  if (method !== 'GET' && method !== 'HEAD') {
    const token = getCsrfToken();
    if (token) finalHeaders.set('X-CSRF-Token', token);
  }

  let res: Response;
  try {
    res = await fetch(path.startsWith('/') ? `${API_BASE}${path}` : path, {
      ...rest,
      method,
      credentials: 'same-origin',
      headers: finalHeaders,
      signal,
      body:
        body === undefined
          ? undefined
          : isRawBody || typeof body === 'string'
            ? (body as BodyInit)
            : JSON.stringify(body),
    });
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') throw e;
    throw new ApiError('NETWORK', 'Tidak dapat terhubung ke server.', {}, 0);
  }

  if (!res.ok) throw await parseError(res);
  if (res.status === 204) return undefined as T;

  const text = await res.text();
  if (!text) return undefined as T;
  try {
    return JSON.parse(text) as T;
  } catch {
    throw new ApiError('INTERNAL', 'Respons server tidak dapat dibaca.', {}, res.status);
  }
}

function query(params: Record<string, string | number | undefined | null>): string {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null || value === '') continue;
    search.set(key, String(value));
  }
  const s = search.toString();
  return s ? `?${s}` : '';
}

/* ------------------------------------------------------------------ */
/* Auth                                                                */
/* ------------------------------------------------------------------ */

export function getAuthState(): Promise<AuthStateResponse> {
  return request<AuthStateResponse>('/auth/state');
}

/** First-run setup. Only succeeds while no administrator exists. */
export function setup(body: SetupRequest): Promise<SessionResponse> {
  return request<SessionResponse>('/auth/setup', { method: 'POST', body });
}

export function login(body: LoginRequest): Promise<SessionResponse> {
  return request<SessionResponse>('/auth/login', { method: 'POST', body });
}

/** Ends the browser session. An active broadcast keeps running. */
export async function logout(): Promise<{ ok: true }> {
  const res = await request<{ ok: true }>('/auth/logout', { method: 'POST' });
  clearCsrfToken();
  return res;
}

export function changePassword(body: PasswordRequest): Promise<{ ok: boolean }> {
  return request<{ ok: boolean }>('/auth/password', { method: 'POST', body });
}

/* ------------------------------------------------------------------ */
/* Dashboard & system                                                  */
/* ------------------------------------------------------------------ */

export function getDashboard(): Promise<Dashboard> {
  return request<Dashboard>('/dashboard');
}

export function getSystem(): Promise<SystemResponse> {
  return request<SystemResponse>('/system');
}

/* ------------------------------------------------------------------ */
/* Media library                                                       */
/* ------------------------------------------------------------------ */

export interface VideoListParams {
  q?: string;
  limit?: number;
  offset?: number;
}

export interface VideoListResponse {
  items: Media[];
  total: number;
  limit: number;
  offset: number;
}

export function listVideos(params: VideoListParams = {}): Promise<VideoListResponse> {
  const { q, limit, offset } = params;
  return request<VideoListResponse>(`/videos${query({ q, limit, offset })}`);
}

export function getVideo(id: string): Promise<Media> {
  return request<Media>(`/videos/${encodeURIComponent(id)}`);
}

/** Byte-range capable; use directly as an <video src>. */
export function getVideoContentUrl(id: string): string {
  return `${API_BASE}/videos/${encodeURIComponent(id)}/content`;
}

export function getVideoThumbnailUrl(id: string): string {
  return `${API_BASE}/videos/${encodeURIComponent(id)}/thumbnail`;
}

/** 409 MEDIA_IN_USE when referenced; `details.usage` names the blockers. */
export function deleteVideo(id: string): Promise<{ ok: true }> {
  return request<{ ok: true }>(`/videos/${encodeURIComponent(id)}`, { method: 'DELETE' });
}

export function recheckVideo(id: string): Promise<unknown> {
  return request<unknown>(`/videos/${encodeURIComponent(id)}/recheck`, { method: 'POST' });
}

/* ------------------------------------------------------------------ */
/* Playlists                                                           */
/* ------------------------------------------------------------------ */

export function listPlaylists(): Promise<PlaylistListResponse> {
  return request<PlaylistListResponse>('/playlists');
}

export function createPlaylist(body: PlaylistRequest): Promise<Playlist> {
  return request<Playlist>('/playlists', { method: 'POST', body });
}

/** Saves a NEW revision; old ones stay intact for pinned schedules. */
export function updatePlaylist(id: string, body: PlaylistUpdateRequest): Promise<Playlist> {
  return request<Playlist>(`/playlists/${encodeURIComponent(id)}`, { method: 'PUT', body });
}

/** 409 MEDIA_IN_USE; `details.schedules` names the blocking schedules. */
export function deletePlaylist(id: string): Promise<{ ok: true }> {
  return request<{ ok: true }>(`/playlists/${encodeURIComponent(id)}`, { method: 'DELETE' });
}

/* ------------------------------------------------------------------ */
/* Schedules                                                           */
/* ------------------------------------------------------------------ */

export function listSchedules(): Promise<ScheduleListResponse> {
  return request<ScheduleListResponse>('/schedules');
}

export function getSchedule(id: string): Promise<ScheduleDetailResponse> {
  return request<ScheduleDetailResponse>(`/schedules/${encodeURIComponent(id)}`);
}

export function createSchedule(body: ScheduleRequest): Promise<Schedule> {
  return request<Schedule>('/schedules', { method: 'POST', body });
}

/** 409 SCHEDULE_RUNNING while the window owns the publisher slot. */
export function updateSchedule(id: string, body: ScheduleRequest): Promise<Schedule> {
  return request<Schedule>(`/schedules/${encodeURIComponent(id)}`, { method: 'PATCH', body });
}

export function deleteSchedule(id: string): Promise<{ ok: true }> {
  return request<{ ok: true }>(`/schedules/${encodeURIComponent(id)}`, { method: 'DELETE' });
}

/* ------------------------------------------------------------------ */
/* Stream control                                                      */
/* ------------------------------------------------------------------ */

export interface PreflightRequest {
  revision_id?: string;
  mode?: PlaybackMode;
  loop?: boolean;
  end_at?: string;
}

/** Runs every start check without starting anything. */
export function preflight(body: PreflightRequest = {}): Promise<PreflightResult> {
  return request<PreflightResult>('/stream/preflight', { method: 'POST', body });
}

/** `idempotency_key` is required: a double click must produce one session. */
export function startStream(body: StartStreamRequest): Promise<StartStreamResponse> {
  return request<StartStreamResponse>('/stream/start', { method: 'POST', body });
}

export function stopStream(body: StopStreamRequest = {}): Promise<StopStreamResponse> {
  return request<StopStreamResponse>('/stream/stop', { method: 'POST', body });
}

export function restartStream(body: RestartStreamRequest = {}): Promise<RestartStreamResponse> {
  return request<RestartStreamResponse>('/stream/restart', { method: 'POST', body });
}

export function getStreamStatus(): Promise<StreamStatusResponse> {
  return request<StreamStatusResponse>('/stream/status');
}

/* ------------------------------------------------------------------ */
/* Logs & audit                                                        */
/* ------------------------------------------------------------------ */

export interface LogListParams {
  /** Comma-separated: 'warn,error'. */
  levels?: string;
  /** Comma-separated: 'publisher,upload'. */
  sources?: string;
  session_id?: string;
  q?: string;
  limit?: number;
  /** RFC3339. */
  since?: string;
}

export function getLogs(params: LogListParams = {}): Promise<LogsResponse> {
  const { levels, sources, session_id, q, limit, since } = params;
  return request<LogsResponse>(`/logs${query({ levels, sources, session_id, q, limit, since })}`);
}

/** Download link for the sanitized text/plain export. */
export function exportLogsUrl(params: LogListParams = {}): string {
  const { levels, sources, session_id, q, limit } = params;
  return `${API_BASE}/logs/export${query({ levels, sources, session_id, q, limit })}`;
}

/** Empties the browser view only; server history is kept. */
export function clearLogs(): Promise<unknown> {
  return request<unknown>('/logs/clear', { method: 'POST' });
}

export function getAudit(limit = 100): Promise<AuditResponse> {
  return request<AuditResponse>(`/audit${query({ limit })}`);
}

/* ------------------------------------------------------------------ */
/* Settings                                                            */
/* ------------------------------------------------------------------ */

export function getSettings(): Promise<SettingsResponse> {
  return request<SettingsResponse>('/settings');
}

/** `stream_key` is write-only; a blank value keeps the stored key. */
export function saveSettings(body: SaveSettingsRequest): Promise<SaveSettingsResponse> {
  return request<SaveSettingsResponse>('/settings', { method: 'PUT', body });
}

export function getDependencies(): Promise<DependencyStatus> {
  return request<DependencyStatus>('/settings/deps');
}

/* ------------------------------------------------------------------ */
/* Uploads — resumable, one part at a time                             */
/* ------------------------------------------------------------------ */

export function createUpload(body: CreateUploadRequest): Promise<CreateUploadResponse> {
  return request<CreateUploadResponse>('/uploads', { method: 'POST', body });
}

export function getUploadStatus(uploadId: string): Promise<UploadStatusResponse> {
  return request<UploadStatusResponse>(`/uploads/${encodeURIComponent(uploadId)}`);
}

export function completeUpload(uploadId: string): Promise<CompleteUploadResponse> {
  return request<CompleteUploadResponse>(`/uploads/${encodeURIComponent(uploadId)}/complete`, {
    method: 'POST',
  });
}

export function cancelUpload(uploadId: string): Promise<{ ok: true }> {
  return request<{ ok: true }>(`/uploads/${encodeURIComponent(uploadId)}`, { method: 'DELETE' });
}

/** Part indexes the server already holds, for resuming an interrupted upload. */
export async function listReceivedParts(uploadId: string): Promise<number[]> {
  const status = await getUploadStatus(uploadId);
  return status.received_parts ?? [];
}

/**
 * Everything needed to resume: the id plus the server's own part geometry.
 * The status endpoint reports neither part size nor total parts, so this is
 * captured at creation and handed back to `uploadFile` to resume.
 */
export interface UploadSession {
  uploadId: string;
  partSize: number;
  totalParts: number;
}

export interface UploadProgress {
  uploadId: string | null;
  sentBytes: number;
  totalBytes: number;
  percent: number;
  partIndex: number;
  totalParts: number;
}

export interface UploadResult {
  uploadId: string;
  mediaId: string;
}

export interface UploadOptions {
  /** Resume this session instead of creating a new one. */
  session?: UploadSession;
  /** Retries per part. Default 2. */
  maxRetries?: number;
  signal?: AbortSignal;
  /**
   * Called after a failure with the surviving session, so the caller can
   * retry the same file later without resending what already arrived.
   */
  onError?: (error: ApiError, session: UploadSession | null) => void;
}

async function sendPart(
  session: UploadSession,
  index: number,
  body: ArrayBuffer,
  offset: number,
  maxRetries: number,
  signal?: AbortSignal,
): Promise<void> {
  let lastError: unknown;
  for (let attempt = 0; attempt <= maxRetries; attempt += 1) {
    if (signal?.aborted) throw new ApiError('UPLOAD_ABORTED', 'Upload dibatalkan.', {}, 0);
    try {
      await request<UploadPartResponse>(
        `/uploads/${encodeURIComponent(session.uploadId)}/parts/${index}`,
        { method: 'PUT', headers: { 'X-Part-Offset': String(offset) }, body },
      );
      return;
    } catch (e) {
      lastError = e;
      // A rejected part (bad offset, expired session) will not fix itself;
      // only transport faults and 5xx/429 are worth retrying.
      if (isApiError(e) && e.status >= 400 && e.status < 500 && e.status !== 429) break;
    }
  }
  if (isApiError(lastError)) throw lastError;
  throw new ApiError('UPLOAD_FAILED', errorMessage(lastError, 'Bagian upload gagal.'), {}, 0);
}

/**
 * The 3-step protocol: POST /uploads, then sequential part PUTs carrying
 * X-Part-Offset with a raw body, then POST complete.
 *
 * Resuming passes the session from a previous failure back in; only the
 * indexes the server lacks are sent.
 */
export async function uploadFile(
  file: File,
  onProgress?: (progress: UploadProgress) => void,
  options: UploadOptions = {},
): Promise<UploadResult> {
  const maxRetries = options.maxRetries ?? 2;
  let session: UploadSession;
  let received: Set<number>;

  if (options.session) {
    session = options.session;
    received = new Set(await listReceivedParts(session.uploadId));
  } else {
    const created = await createUpload({
      filename: file.name,
      size: file.size,
      content_type: file.type || 'application/octet-stream',
    });
    session = {
      uploadId: created.upload_id,
      partSize: created.part_size,
      totalParts: created.total_parts,
    };
    received = new Set();
  }

  const { partSize, totalParts } = session;

  const report = (sentBytes: number, partIndex: number): void => {
    onProgress?.({
      uploadId: session.uploadId,
      sentBytes,
      totalBytes: file.size,
      percent: file.size > 0 ? Math.min(100, (sentBytes / file.size) * 100) : 0,
      partIndex,
      totalParts,
    });
  };

  let sentBytes = 0;
  try {
    for (let index = 0; index < totalParts; index += 1) {
      const start = index * partSize;
      const end = Math.min(file.size, start + partSize);

      if (received.has(index)) {
        sentBytes = end;
        report(sentBytes, index);
        continue;
      }

      const body = await file.slice(start, end).arrayBuffer();
      await sendPart(session, index, body, start, maxRetries, options.signal);
      sentBytes = end;
      report(sentBytes, index);
    }
  } catch (e) {
    const err = isApiError(e)
      ? e
      : new ApiError('UPLOAD_FAILED', errorMessage(e, 'Upload gagal.'), {}, 0);
    // The session survives the failure, so the caller can resume rather than
    // resend the whole file.
    options.onError?.(err, session);
    throw err;
  }

  const done = await completeUpload(session.uploadId);
  report(file.size, totalParts);
  return { uploadId: session.uploadId, mediaId: done.media_id };
}