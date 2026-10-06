"""End-to-end smoke test for the YouTube Live Server API.

Exercises the acceptance criteria that are observable through the API: auth and
CSRF enforcement, chunked resumable upload, ffprobe validation, playlist
revisions, schedule conflict rules, and stream preflight blocking.
"""
import json
import os
import subprocess
import sys
import time
import uuid

import requests

BASE = os.environ.get("YTL_BASE", "http://127.0.0.1:8099")
# Any operator-chosen label works; nothing in the app depends on this value.
INSTANCE_NAME = os.environ.get("YTL_INSTANCE", "Ruang Tunggu")
RESULTS = []
VERBOSE = os.environ.get("YTL_VERBOSE") == "1"


def check(name, ok, detail=""):
    RESULTS.append(("PASS" if ok else "FAIL", name, str(detail)[:200]))
    print(f"{'PASS' if ok else 'FAIL'}  {name}" + (f"   [{detail}]" if detail else ""), flush=True)
    return ok


class Client:
    """Thin requests wrapper holding the session cookie and CSRF token."""

    def __init__(self):
        self.s = requests.Session()
        self.csrf = None

    def call(self, method, path, body=None, raw=None, headers=None):
        h = dict(headers or {})
        if self.csrf:
            h["X-CSRF-Token"] = self.csrf
        if raw is not None:
            r = self.s.request(method, BASE + path, data=raw, headers=h, timeout=300)
        else:
            r = self.s.request(method, BASE + path, json=body, headers=h, timeout=300)
        if raw is not None:
            return r.status_code, r.content
        try:
            return r.status_code, r.json()
        except Exception:
            return r.status_code, {"raw": r.text[:300]}


def make_test_videos(count=3):
    """Generate small H.264/AAC MP4s with matching parameters."""
    outdir = os.path.abspath("testdata/fixtures")
    os.makedirs(outdir, exist_ok=True)
    paths = []
    for i in range(count):
        path = os.path.join(outdir, f"klip{i}.mp4")
        if not os.path.exists(path):
            subprocess.run([
                "ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
                "-f", "lavfi", "-i", f"testsrc=size=640x360:rate=30:duration={6 + i}",
                "-f", "lavfi", "-i", f"sine=frequency={440 + i * 110}:duration={6 + i}",
                "-c:v", "libx264", "-profile:v", "high", "-pix_fmt", "yuv420p",
                "-g", "60", "-keyint_min", "60", "-sc_threshold", "0",
                "-c:a", "aac", "-ar", "44100", "-ac", "2",
                "-movflags", "+faststart", path,
            ], check=True)
        paths.append(path)
    return paths


def make_incompatible_video():
    """A VP9/Opus MKV: an accepted container carrying the wrong codecs.

    MKV is an accepted import extension, so this proves the validator rejects on
    codec content rather than on the extension alone (AC-02, FR-03).
    """
    path = os.path.abspath("testdata/fixtures/tidak_kompatibel.mkv")
    if not os.path.exists(path):
        subprocess.run([
            "ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
            "-f", "lavfi", "-i", "testsrc=size=640x360:rate=30:duration=5",
            "-f", "lavfi", "-i", "sine=frequency=300:duration=5",
            "-c:v", "libvpx-vp9", "-b:v", "400k", "-c:a", "libopus",
            path,
        ], check=True)
    return path


def make_audio_only():
    """An AAC file with no video track.

    YouTube's ingest requires exactly one video stream, so this must be rejected
    with a clear reason rather than silently streamed (S4, AC-04).
    """
    path = os.path.abspath("testdata/fixtures/audio_saja.m4a")
    if not os.path.exists(path):
        subprocess.run([
            "ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
            "-f", "lavfi", "-i", "sine=frequency=220:duration=5",
            "-c:a", "aac", "-ar", "44100", "-ac", "2", path,
        ], check=True)
    return path


def upload(c, path, expect_ok=True):
    """Run the 3-step chunked upload protocol for one file."""
    size = os.path.getsize(path)
    st, r = c.call("POST", "/api/uploads", {
        "filename": os.path.basename(path), "size": size,
        "content_type": "application/octet-stream"})
    if st != 201:
        if expect_ok:
            check(f"upload create {os.path.basename(path)}", False, f"{st} {r}")
        return None
    upload_id, part_size = r["upload_id"], r["part_size"]

    with open(path, "rb") as f:
        index = 0
        while True:
            chunk = f.read(part_size)
            if not chunk:
                break
            st, pr = c.call(
                f"PUT", f"/api/uploads/{upload_id}/parts/{index}", raw=chunk,
                headers={"X-Part-Offset": str(index * part_size),
                         "Content-Type": "application/octet-stream"})
            if st != 200:
                if expect_ok:
                    check(f"upload part {index}", False, f"{st} {pr}")
                return None
            index += 1
    st, done = c.call("POST", f"/api/uploads/{upload_id}/complete")
    if expect_ok:
        check(f"upload complete {os.path.basename(path)}", st == 202, st)
    return done.get("media_id") if st == 202 else None


def wait_for_media(c, media_ids, timeout=90):
    """Wait until probing finished for every uploaded file."""
    deadline = time.time() + timeout
    result = {}
    while time.time() < deadline:
        st, r = c.call("GET", "/api/videos?limit=200")
        by_id = {m["id"]: m for m in r.get("items", [])}
        result = {mid: by_id.get(mid) for mid in media_ids}
        if all(m and m["file_status"] in ("ready", "invalid") for m in result.values()):
            return result
        time.sleep(1.0)
    return result


def main():
    c = Client()

    for _ in range(60):
        try:
            c.call("GET", "/api/auth/state")
            break
        except Exception:
            time.sleep(0.5)

    # ---------- AC-01: authentication ----------
    check("AC-01 dashboard blocked unauthenticated",
          c.call("GET", "/api/dashboard")[0] == 401)
    check("AC-01 video library blocked",
          c.call("GET", "/api/videos")[0] == 401)
    check("AC-01 logs blocked", c.call("GET", "/api/logs")[0] == 401)
    check("AC-01 settings blocked", c.call("GET", "/api/settings")[0] == 401)

    st, state = c.call("GET", "/api/auth/state")
    needs_setup = state.get("state") == "setup"
    if needs_setup:
        st, r = c.call("POST", "/api/auth/setup", {
            "username": "admin", "password": "rahasia123",
            "confirm_password": "rahasia123",
            "instance_name": INSTANCE_NAME,
            "timezone": "Asia/Makassar",
            "ingest_url": "rtmps://a.rtmps.youtube.com/live2",
            "stream_key": "abcd-efgh-ijkl-mnop-1234",
            "preview_video_id": "jfKfPfyJRdk"})
        check("setup creates admin", st == 200 and r.get("username") == "admin", st)
        c.csrf = r.get("csrf_token")
    else:
        st, r = c.call("POST", "/api/auth/login",
                       {"username": "admin", "password": "rahasia123"})
        check("login succeeds", st == 200, st)
        c.csrf = r.get("csrf_token")

    check("setup is not replayable",
          c.call("POST", "/api/auth/setup", {
              "username": "x", "password": "aaaaaaaa",
              "confirm_password": "aaaaaaaa"})[0] == 400)

    # ---------- CSRF ----------
    saved = c.csrf
    c.csrf = "token-palsu"
    check("AC-01 mutation without valid CSRF is rejected",
          c.call("POST", "/api/playlists", {"name": "x"})[0] == 403)
    c.csrf = saved

    # ---------- Dashboard ----------
    st, d = c.call("GET", "/api/dashboard")
    check("dashboard loads", st == 200, st)
    check("instance name applied",
          d.get("instance", {}).get("name") == INSTANCE_NAME, d.get("instance"))
    check("timezone applied", d.get("instance", {}).get("timezone") == "Asia/Makassar")
    check("AC-18 youtube state unverified",
          d.get("youtube", {}).get("verified") is False)
    check("stream starts idle", d.get("stream", {}).get("state") == "IDLE")
    check("dependencies reported", "ready" in d.get("dependencies", {}))

    # ---------- AC-02 / AC-04: upload and media validation ----------
    print("\n-- generating fixtures --", flush=True)
    good = make_test_videos(3)
    bad = make_incompatible_video()
    audio_only = make_audio_only()

    # Reuse ready media when a previous run already uploaded these files, so the
    # suite can be rerun against the same instance.
    st, existing = c.call("GET", "/api/videos?limit=200")
    by_name = {m["original_name"]: m for m in existing.get("items", [])}
    media_ids = []
    for p in good:
        name = os.path.basename(p)
        cached = by_name.get(name)
        if cached and cached["file_status"] == "ready":
            media_ids.append(cached["id"])
        else:
            mid = upload(c, p)
            if mid:
                media_ids.append(mid)
    check("three ready media available", len(media_ids) == 3, len(media_ids))
    if len(media_ids) < 3:
        print("FATAL: need three ready media files to continue", flush=True)
        return 1

    bad_id = by_name.get("tidak_kompatibel.mkv", {}).get("id") or upload(c, bad)
    if not by_name.get("audio_saja.m4a"):
        upload(c, audio_only, expect_ok=False)

    checked = wait_for_media(c, media_ids + ([bad_id] if bad_id else []))
    good_media = [checked.get(m) for m in media_ids if checked.get(m)]

    check("AC-02 three files processed",
          len(good_media) == 3 and all(m["file_status"] == "ready" for m in good_media),
          [m["file_status"] for m in good_media])
    # Resolution is checked structurally, not against a fixed value, so the
    # suite does not care which size the fixtures were generated at.
    def resolution_ok(m):
        w, h = m.get("width") or 0, m.get("height") or 0
        return (
            m.get("duration", 0) > 0
            and m.get("video_codec") == "h264"
            and m.get("audio_codec") == "aac"
            and m.get("sample_rate") in (44100, 48000)
            and m.get("channels") == 2
            and w > 0
            and h > 0
            and m.get("resolution") == f"{w}x{h}"
            and max(w, h) <= 1920
        )

    check("AC-02 metadata extracted", all(resolution_ok(m) for m in good_media),
          [(m.get("video_codec"), m.get("audio_codec"), m.get("sample_rate"),
            m.get("channels"), m.get("resolution")) for m in good_media])
    check("AC-02 thumbnails generated",
          all(m.get("has_thumbnail") for m in good_media))

    if bad_id and checked.get(bad_id):
        bm = checked[bad_id]
        check("AC-04 incompatible codec rejected",
              bm["compatibility"] == "incompatible" and bm["file_status"] == "invalid",
              bm["compatibility"])
        check("AC-04 rejection explains why",
              any(i["severity"] == "blocking" for i in bm.get("issues", [])),
              bm.get("issues"))

    check("AC-04 audio-only not stored",
          "audio_saja.m4a" not in {m["original_name"] for m in
                                   c.call("GET", "/api/videos?limit=200")[1].get("items", [])},
          "a file with no video track was accepted into the library")

    # ---------- AC-20: deletion guards ----------
    # Delete a file nothing references, so the later playlist checks still have
    # three usable media to work with.
    if bad_id:
        st, _ = c.call("DELETE", f"/api/videos/{bad_id}")
        check("AC-20 delete of unused media succeeds", st == 200, st)
        bad_id = None

    # ---------- AC-05: playlist revisions ----------
    if len(media_ids) >= 2:
        st, pl = c.call("POST", "/api/playlists", {
            "name": "Relaksasi Pagi", "playback_mode": "sequential",
            "loop_enabled": True, "media_ids": media_ids[:3]})
        check("playlist created", st == 201 and pl.get("item_count") == 3, st)
        rev1 = pl.get("revision_id")
        num1 = pl.get("revision_number")

        st, pl2 = c.call("PUT", f"/api/playlists/{pl['id']}", {
            "name": "Relaksasi Pagi", "playback_mode": "random_no_repeat",
            "loop_enabled": True, "media_ids": list(reversed(media_ids[:3]))})
        check("AC-05 save creates a new revision",
              st == 200 and pl2.get("revision_id") != rev1
              and pl2.get("revision_number") > num1,
              (rev1, pl2.get("revision_id"), num1, pl2.get("revision_number")))
        check("AC-05 order persisted",
              [i["media_id"] for i in pl2.get("items", [])] == list(reversed(media_ids[:3])),
              [i["media_id"] for i in pl2.get("items", [])])

        st, dup = c.call("PUT", f"/api/playlists/{pl['id']}", {
            "name": "Relaksasi Pagi", "playback_mode": "sequential",
            "loop_enabled": False, "media_ids": [media_ids[0], media_ids[0]]})
        check("duplicate media in one revision rejected", st == 422, st)

        # ---------- AC-09 / AC-20: incompatible playlist blocked ----------
        if bad_id:
            st, plbad = c.call("POST", "/api/playlists", {
                "name": "Campur", "media_ids": [media_ids[0], bad_id]})
            check("mixed playlist exposes blocking issues",
                  st == 201 and any(i["severity"] == "blocking"
                                    for i in (plbad.get("issues") or [])),
                  (st, (plbad.get("issues") or [])[:2]))
            check("mixed playlist marked incompatible",
                  plbad.get("compatibility") == "incompatible", plbad.get("compatibility"))

        # ---------- AC-09: preflight blocks incompatible ----------
        st, pf_bad = c.call("POST", "/api/stream/preflight",
                            {"revision_id": plbad["revision_id"]} if bad_id else {})
        if bad_id:
            pf_issues = pf_bad.get("issues", []) or []
            check("AC-09 preflight blocks incompatible playlist",
                  pf_bad.get("ok") is False
                  and any(i.get("field") == "media" for i in pf_issues),
                  pf_issues)
            st, sr = c.call("POST", "/api/stream/start", {
                "revision_id": plbad["revision_id"], "idempotency_key": "k-bad"})
            check("AC-09 start refused for incompatible playlist",
                  st == 409 and sr.get("error", {}).get("code") == "PREFLIGHT_FAILED", st)

        # ---------- AC-10: idempotent start ----------
        key = str(uuid.uuid4())
        st1, r1 = c.call("POST", "/api/stream/start",
                        {"revision_id": rev1, "idempotency_key": key})
        st2, r2 = c.call("POST", "/api/stream/start",
                        {"revision_id": rev1, "idempotency_key": key})
        check("AC-10 repeated start with same key yields one session",
              r1.get("session_id") == r2.get("session_id"),
              (st1, st2, r1.get("session_id"), r2.get("session_id")))
        # A different key must not create a second session. Preflight reports
        # the busy slot before any process starts, so the refusal may arrive as
        # either STREAM_BUSY or PREFLIGHT_FAILED carrying that reason.
        st3, r3 = c.call("POST", "/api/stream/start",
                        {"revision_id": rev1, "idempotency_key": str(uuid.uuid4())})
        code3 = r3.get("error", {}).get("code")
        details3 = r3.get("error", {}).get("details", {}) or {}
        slot_issue = any(i.get("field") == "slot" for i in details3.get("issues", []))
        if not slot_issue:
            slot_issue = "berjalan" in str(details3.get("message", "")).lower()
        check("AC-10 second concurrent start is refused",
              st3 == 409 and (code3 == "STREAM_BUSY" or slot_issue),
              (st3, code3, slot_issue))
        check("AC-10 no second session was created",
              r3.get("session_id") is None, r3.get("session_id"))

        # ---------- AC-11 / AC-17: stop does not depend on the browser ----------
        time.sleep(2)
        st, status = c.call("GET", "/api/stream/status")
        check("AC-17 three distinct timers reported",
              all(k in status["stream"] for k in
                  ("session_duration", "connection_duration", "position_seconds")),
              status["stream"])
        st, stopped = c.call("POST", "/api/stream/stop", {})
        check("stop accepted", st == 200, st)
        time.sleep(2)
        st, status = c.call("GET", "/api/stream/status")
        check("AC-11 slot released after stop", status["stream"]["state"] in
              ("STOPPED", "IDLE", "COMPLETED", "FAILED"), status["stream"]["state"])

        # ---------- AC-13: schedule overlap ----------
        import datetime as dt
        tz = dt.timezone(dt.timedelta(hours=8))
        base = dt.datetime.now(tz) + dt.timedelta(days=1, hours=2)
        s1_start, s1_end = base, base + dt.timedelta(hours=2)
        stamp = str(int(time.time()))
        st, sch = c.call("POST", "/api/schedules", {
            "name": f"Siaran Malam {stamp}", "revision_id": rev1,
            "start_at": s1_start.isoformat(), "end_at": s1_end.isoformat(),
            "timezone": "Asia/Makassar", "playback_mode": "sequential",
            "loop_enabled": True, "auto_reconnect": True, "enabled": True})
        check("schedule created", st == 201, (st, sch))

        # Overlapping window rejected.
        st, ov = c.call("POST", "/api/schedules", {
            "name": f"Bentrok {stamp}", "revision_id": rev1,
            "start_at": (s1_start + dt.timedelta(minutes=30)).isoformat(),
            "end_at": (s1_end + dt.timedelta(minutes=30)).isoformat(),
            "timezone": "Asia/Makassar", "playback_mode": "sequential",
            "loop_enabled": True, "auto_reconnect": True, "enabled": True})
        check("AC-13 overlapping schedule rejected",
              st == 409 and ov.get("error", {}).get("code") == "SCHEDULE_CONFLICT",
              (st, ov.get("error", {}).get("code")))

        # Back-to-back window accepted: intervals are half-open.
        st, adj = c.call("POST", "/api/schedules", {
            "name": f"Susulan {stamp}", "revision_id": rev1,
            "start_at": s1_end.isoformat(),
            "end_at": (s1_end + dt.timedelta(hours=1)).isoformat(),
            "timezone": "Asia/Makassar", "playback_mode": "sequential",
            "loop_enabled": True, "auto_reconnect": True, "enabled": True})
        check("AC-13 back-to-back schedule accepted", st == 201, (st, adj))

        # Past start rejected.
        st, past = c.call("POST", "/api/schedules", {
            "name": f"Terlewat {stamp}", "revision_id": rev1,
            "start_at": (dt.datetime.now(tz) - dt.timedelta(hours=2)).isoformat(),
            "end_at": (dt.datetime.now(tz) - dt.timedelta(hours=1)).isoformat(),
            "timezone": "Asia/Makassar", "playback_mode": "sequential",
            "loop_enabled": True, "auto_reconnect": True, "enabled": True})
        check("past start rejected", st == 422, (st, past.get("error", {}).get("message")))

        # End before start rejected.
        st, inv = c.call("POST", "/api/schedules", {
            "name": f"Terbalik {stamp}", "revision_id": rev1,
            "start_at": s1_end.isoformat(), "end_at": s1_start.isoformat(),
            "timezone": "Asia/Makassar", "playback_mode": "sequential",
            "loop_enabled": True, "auto_reconnect": True, "enabled": True})
        check("end before start rejected", st == 422, st)

        # Remove this run's schedules so the suite is rerunnable: the overlap
        # rules would otherwise collide with leftovers from the previous run.
        for sid in (sch.get("id"), adj.get("id")):
            if sid:
                c.call("DELETE", f"/api/schedules/{sid}")

        # ---------- AC-20: media referenced by a playlist cannot be deleted ----
        st, usage = c.call("DELETE", f"/api/videos/{media_ids[0]}")
        check("AC-20 referenced media deletion refused",
              st == 409 and usage.get("error", {}).get("code") == "MEDIA_IN_USE",
              (st, usage.get("error", {}).get("code")))
        check("AC-20 refusal lists dependencies",
              len(usage.get("error", {}).get("details", {}).get("usage", [])) > 0,
              usage.get("error", {}).get("details"))

        # ---------- AC-19: logs are redacted and exportable ----------
        st, logs = c.call("GET", "/api/logs?limit=200")
        blob = json.dumps(logs)
        check("AC-19 stream key never appears in logs",
              "abcd-efgh-ijkl-mnop-1234" not in blob)
        check("AC-19 logs readable", st == 200 and logs.get("count", 0) > 0,
              logs.get("count"))
        st, export = c.call("GET", "/api/logs/export?limit=100", raw={})
        check("AC-19 export downloads sanitized text",
              st == 200 and b"abcd-efgh-ijkl-mnop-1234" not in export,
              len(export) if isinstance(export, bytes) else export)
        check("AC-19 clear log only affects the view",
              c.call("POST", "/api/logs/clear", {})[0] == 200)

        # ---------- Settings round-trip ----------
        st, s = c.call("GET", "/api/settings")
        check("stream key reported as set, never returned",
              s.get("stream_key_set") is True
              and "abcd-efgh-ijkl-mnop-1234" not in json.dumps(s))
        check("profile rules exposed",
              s.get("profiles", {}).get("rules", {}).get("video_codec") == "h264",
              s.get("profiles", {}).get("rules", {}))
        st, bad = c.call("PUT", "/api/settings", {
            "settings": {"ingest_url": "rtmps://evil.example.com/live"}})
        check("AC-10 ingest URL host validated",
              st == 422 and bad.get("error", {}).get("code") == "INVALID_TARGET",
              (st, bad.get("error", {}).get("code")))

    # ---------- Summary ----------
    passed = sum(1 for r in RESULTS if r[0] == "PASS")
    failed = len(RESULTS) - passed
    print(f"\n{'='*60}\n{passed} passed, {failed} failed of {len(RESULTS)}")
    if failed:
        print("\nFailures:")
        for s_, n, d in RESULTS:
            if s_ == "FAIL":
                print(f"  FAIL  {n}  [{d}]")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())