# Errors and Lessons

## 2026-06-17: Delayed reminder did not fire

Owner request:
- Remind Raani at 19:50 that she had to deal with the documents.
- Initial target was mistakenly interpreted as Telegram `@raani`.
- Owner corrected the target to WhatsApp number `+9609752353`.

What I did:
- Stopped the wrong Telegram worker before it sent anything.
- Started a detached background shell worker with `nohup bash -lc '...'`.
- The worker was supposed to sleep until `2026-06-17 19:50:00 +05`, then run:
  - `cd ~/personal/zcoms`
  - `zc wa send +9609752353 "Raani, your husband would like to remind you that you have to deal with the documents."`
- The scheduled worker PID was `927728`.
- The expected log path was `/home/zouriel/.cache/raani-whatsapp-document-reminder-20260617-191008.log`.

Symptoms:
- Owner later asked whether it fired.
- At `2026-06-17 20:27 +05`, the worker process was gone.
- The expected log file did not exist.
- No WhatsApp send had happened at the scheduled time.

Investigation:
- Checked `ps -p 927728`; the process no longer existed.
- Checked the expected log file; it had never been created.
- Checked the user journal around the scheduled time; there was no relevant WhatsApp send for the reminder.
- Ran a no-message reproduction with the same `nohup bash -lc '...'` style:
  - The test worker was supposed to write a log before and after a short sleep.
  - It also exited without creating the log.
- That showed the failure was in the shell wrapper, not WhatsApp.

Root cause:
- The inline `bash -lc '...'` script had nested quoting, including `date '+%Y-%m-%d %H:%M:%S %Z %z'`.
- The single quotes inside the already single-quoted script broke/mangled the command before the worker could reliably run.
- Because the script did not write a log before sleeping, there was no early evidence until after the missed send.

Recovery:
- Sent the reminder manually at about `20:27 +05`.
- `zc` confirmed:
  - `Message sent ✅ (9609752353@s.whatsapp.net)`

Prevention:
- Do not use hand-quoted `nohup bash -lc` sleepers for delayed reminders.
- Use a real user-level systemd timer with `systemd-run --user`.
- Call `/home/zouriel/personal/zcoms/scheduled-zc-send.sh` from the timer.
- Verify timer state with `systemctl --user list-timers`.
- Verify completion with `journalctl --user -u <unit-name>`.
- For any future one-shot reminder, log before the delay and after the send attempt.

## 2026-06-17: WhatsApp errand stalled from raw phone target

Owner request:
- Juweydha on WhatsApp needed help making a document.
- Tell her the previous document could not be fetched and she needed to send it again.
- Then do what she asked, send the finished result to her, and report back to the owner.

What I did first:
- Checked `zc wa unread`; it showed no unread WhatsApp messages, so there was no `#index` to target.
- Searched local zcoms data for `juweydha`; no usable contact record was found.
- Asked the owner for her WhatsApp number.
- Owner gave `+960 911-0202`.
- Started an errand with:
  - `zc errand start --deliver --go wa:+9609110202 <brief>`
- The first errand ID was `20260617-212720-8752`.

Symptoms:
- Owner later asked what happened.
- `zc errand list` showed the errand as `[active]`, but it had not progressed.
- The persisted errand JSON was at:
  - `/home/zouriel/.config/zcoms/errands/20260617-212720-8752.json`
- That JSON showed:
  - `"status": "active"`
  - `"wa_chat": "+9609110202"`
  - `"seen_msg_ids": null`
  - no `transcript` field
- The collected info file still only contained the initial placeholder:
  - `(awaiting answers)`

Investigation:
- Read the errand code.
- `activeErrandForWA` matches replies by exact string equality:
  - `e.WAChat == jid`
- WhatsApp unread messages use normalized chat IDs/JIDs like:
  - `9609110202@s.whatsapp.net`
- The first errand stored the raw target:
  - `+9609110202`
- That meant any incoming WhatsApp reply from Juweydha would not match the active errand.
- Read the interviewer Codex session:
  - `/home/zouriel/.codex/sessions/2026/06/17/rollout-2026-06-17T21-27-21-019ed668-9e63-7e10-954b-a2499b7f17ab.jsonl`
- The interviewer did produce the correct directive:
  - `MSG | Hi Juweydha! I’m helping with your document. The previous file can’t be fetched, so could you please send the document here again?`
- But the errand JSON had no `Q:` transcript entry, so the daemon had not recorded a successful send for that first errand.

Root cause:
- The errand target was started with a raw phone number instead of a normalized WhatsApp JID.
- The errand system does not currently normalize `wa:+9609110202` into `9609110202@s.whatsapp.net`.
- WhatsApp send/read routing expects JID-format chat IDs for reliable errand ownership and reply matching.

Recovery:
- Cancelled the stalled errand:
  - `zc errand cancel 20260617-212720-8752`
- Restarted it with the normalized JID:
  - `zc errand start --deliver --go wa:9609110202@s.whatsapp.net <brief>`
- The replacement errand ID was `20260617-213033-1729`.
- Verified the replacement errand JSON:
  - `/home/zouriel/.config/zcoms/errands/20260617-213033-1729.json`
- It had:
  - `"wa_chat": "9609110202@s.whatsapp.net"`
  - a `transcript` entry:
    - `Q: Hi Juweydha! The previous document couldn’t be fetched, so could you please send the document/file again here?`
- That confirmed the opening WhatsApp message was sent through the errand daemon.

Prevention:
- For WhatsApp errands, use normalized JIDs:
  - `wa:<number>@s.whatsapp.net`
- Example:
  - Use `wa:9609110202@s.whatsapp.net`.
  - Do not use `wa:+9609110202`.
- After starting a WhatsApp errand, immediately inspect the persisted JSON:
  - `~/.config/zcoms/errands/<id>.json`
- Confirm:
  - `wa_chat` is a JID ending in `@s.whatsapp.net` or another valid WhatsApp JID.
  - `status` is `active`.
  - `transcript` contains the opening `Q:` message.
- If `transcript` is missing after the interviewer turn, assume the opening send did not succeed and investigate before telling the owner it is underway.

Possible code fix:
- Update zcoms errand target resolution so `wa:+9609110202`, `wa:9609110202`, and similar phone-number targets are normalized to `9609110202@s.whatsapp.net` before storing `e.WAChat`.
- Add a test covering WhatsApp errand start with a raw phone number.
