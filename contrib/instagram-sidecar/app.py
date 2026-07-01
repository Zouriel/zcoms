"""
zcoms Instagram sidecar — a thin REST wrapper around instagrapi (the private
Instagram API library). The Go transport in internal/comms/instagram/ talks to
this over localhost HTTP; it owns login state, session persistence (encrypted on
the Go side), thread polling, and sending.

Instagram has no official API. This uses the private mobile API via instagrapi,
which violates Instagram's ToS and carries a real ban risk — run it against a
secondary account, ideally behind a residential proxy, and keep polling gentle.

Endpoints (all JSON):
  GET  /health                      -> {ok, logged_in}
  POST /login   {username,password,proxy?}
                                    -> {status: ok|needs_2fa|needs_challenge|error, message?}
  POST /login/verify {code}         -> {status: ok|needs_challenge|error, message?}
  GET  /settings                    -> {settings: {...}}          (dump for persistence)
  POST /settings {settings:{...}}   -> {status: ok|error}          (restore + verify)
  POST /logout                      -> {ok}
  GET  /threads?amount=N            -> {threads:[{thread_id,title,username,is_group,messages:[...]}]}
  GET  /user_id?username=handle     -> {user_id}
  POST /send      {thread_id?,user_id?,text}          -> {message_id, thread_id}
  POST /send_file {thread_id?,user_id?,path,caption?} -> {message_id, thread_id}

This is intentionally single-account and single-process: one daemon, one IG login.
"""

from __future__ import annotations

import os
from typing import Any

from fastapi import FastAPI
from pydantic import BaseModel
from instagrapi import Client
from instagrapi.exceptions import (
    TwoFactorRequired,
    ChallengeRequired,
    ClientError,
    LoginRequired,
)

app = FastAPI(title="zcoms-instagram-sidecar")

cl = Client()
cl.delay_range = [1, 3]  # be gentle: randomised pause between requests

# Login continuation state. Instagram's 2FA/challenge is a two-step exchange, so
# we stash the credentials from /login and finish on /login/verify.
_pending: dict[str, str] = {}


def _logged_in() -> bool:
    try:
        return bool(cl.user_id)
    except Exception:
        return False


class LoginBody(BaseModel):
    username: str
    password: str
    proxy: str | None = None


class VerifyBody(BaseModel):
    code: str


class SettingsBody(BaseModel):
    settings: dict[str, Any]


class SendBody(BaseModel):
    thread_id: str | None = None
    user_id: str | None = None
    text: str = ""


class SendFileBody(BaseModel):
    thread_id: str | None = None
    user_id: str | None = None
    path: str
    caption: str = ""


@app.get("/health")
def health() -> dict:
    return {"ok": True, "logged_in": _logged_in()}


@app.post("/login")
def login(body: LoginBody) -> dict:
    _pending.clear()
    if body.proxy:
        try:
            cl.set_proxy(body.proxy)
        except Exception as e:  # noqa: BLE001
            return {"status": "error", "message": f"bad proxy: {e}"}
    try:
        cl.login(body.username, body.password)
        return {"status": "ok"}
    except TwoFactorRequired:
        _pending.update(username=body.username, password=body.password, kind="2fa")
        return {"status": "needs_2fa"}
    except ChallengeRequired:
        _pending.update(username=body.username, password=body.password, kind="challenge")
        return {"status": "needs_challenge"}
    except ClientError as e:
        return {"status": "error", "message": str(e)}


@app.post("/login/verify")
def login_verify(body: VerifyBody) -> dict:
    if not _pending:
        return {"status": "error", "message": "no login in progress"}
    username = _pending.get("username", "")
    password = _pending.get("password", "")
    kind = _pending.get("kind", "2fa")
    try:
        if kind == "2fa":
            cl.login(username, password, verification_code=body.code.strip())
        else:
            # Challenge: feed the emailed/SMS code back into the resolver.
            cl.challenge_code = body.code.strip()
            cl.challenge_resolve(cl.last_json)
        _pending.clear()
        return {"status": "ok"}
    except ChallengeRequired:
        _pending.update(kind="challenge")
        return {"status": "needs_challenge"}
    except ClientError as e:
        return {"status": "error", "message": str(e)}


@app.get("/settings")
def get_settings() -> dict:
    return {"settings": cl.get_settings()}


@app.post("/settings")
def set_settings(body: SettingsBody) -> dict:
    try:
        cl.set_settings(body.settings)
        # A cheap authenticated call proves the restored session is still valid.
        cl.get_timeline_feed()
        return {"status": "ok"}
    except (LoginRequired, ClientError) as e:
        return {"status": "error", "message": str(e)}


@app.post("/logout")
def logout() -> dict:
    try:
        cl.logout()
    except Exception:  # noqa: BLE001
        pass
    return {"ok": True}


def _thread_json(t: Any) -> dict:
    users = getattr(t, "users", []) or []
    other = users[0].username if users else ""
    msgs = []
    for m in getattr(t, "messages", []) or []:
        ts = getattr(m, "timestamp", None)
        msgs.append(
            {
                "id": str(getattr(m, "id", "")),
                "user_id": str(getattr(m, "user_id", "")),
                "text": getattr(m, "text", None) or "",
                "item_type": getattr(m, "item_type", "") or "text",
                "timestamp": ts.timestamp() if ts else 0,
                "is_from_me": str(getattr(m, "user_id", "")) == str(cl.user_id),
            }
        )
    return {
        "thread_id": str(getattr(t, "id", "")),
        "title": getattr(t, "thread_title", "") or "",
        "username": other,
        "is_group": bool(getattr(t, "is_group", False)) or len(users) > 1,
        "messages": msgs,
    }


@app.get("/threads")
def threads(amount: int = 20) -> dict:
    ts = cl.direct_threads(amount=amount, thread_message_limit=10)
    return {"threads": [_thread_json(t) for t in ts]}


@app.get("/user_id")
def user_id(username: str) -> dict:
    return {"user_id": str(cl.user_id_from_username(username))}


@app.post("/send")
def send(body: SendBody) -> dict:
    if body.thread_id:
        dm = cl.direct_send(body.text, thread_ids=[int(body.thread_id)])
    elif body.user_id:
        dm = cl.direct_send(body.text, user_ids=[int(body.user_id)])
    else:
        return {"message_id": "", "thread_id": ""}
    return {"message_id": str(dm.id), "thread_id": str(getattr(dm, "thread_id", "") or body.thread_id or "")}


@app.post("/send_file")
def send_file(body: SendFileBody) -> dict:
    thread_ids = [int(body.thread_id)] if body.thread_id else None
    user_ids = [int(body.user_id)] if body.user_id else None
    ext = os.path.splitext(body.path)[1].lower()
    if ext in (".mp4", ".mov"):
        dm = cl.direct_send_video(body.path, thread_ids=thread_ids, user_ids=user_ids)
    else:
        dm = cl.direct_send_photo(body.path, thread_ids=thread_ids, user_ids=user_ids)
    return {"message_id": str(dm.id), "thread_id": str(getattr(dm, "thread_id", "") or body.thread_id or "")}
