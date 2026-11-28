package hub

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Zouriel/zcoms/client"
	"github.com/Zouriel/zcoms/internal/comms/contacts"
	"github.com/Zouriel/zcoms/internal/comms/telegram"
)

// Telegram has one disappearing timer per chat, so a thread timer is enforced
// here: every sweepEvery the daemon deletes the thread's messages that are
// older than the timer. Pinned messages and the thread's own "created" notice
// are never deleted, and neither is anything sent before the timer was set.
const (
	sweepEvery     = time.Minute
	sweepPages     = 20 // of 100 messages, per thread per sweep
	minThreadTimer = time.Minute
)

// sweepThreadTimers runs the thread-timer sweeps until ctx is done. RunDaemon
// starts it for the life of the process.
func (d *daemon) sweepThreadTimers(ctx context.Context) {
	t := time.NewTicker(sweepEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.sweepOnce(time.Now())
		}
	}
}

func (d *daemon) sweepOnce(now time.Time) {
	timers, err := d.contacts.ThreadTimers("telegram", "")
	if err != nil {
		d.logf("thread timers: %v", err)
		return
	}
	for _, tt := range timers {
		if err := d.sweepThread(tt, now); err != nil {
			d.logf("thread timer %s/%s: %v", tt.NativeID, tt.ThreadID, err)
		}
	}
}

// sweepThread deletes one thread's expired messages and advances its mark.
func (d *daemon) sweepThread(tt contacts.ThreadTimer, now time.Time) error {
	chatID, err := groupChatID(tt.NativeID)
	if err != nil {
		return err
	}
	topic, err := strconv.ParseInt(tt.ThreadID, 10, 64)
	if err != nil {
		return fmt.Errorf("bad thread id %q", tt.ThreadID)
	}
	cutoff, err := telegram.MessageByDate(d.tdjson, d.clientID, chatID, now.Add(-time.Duration(tt.Seconds)*time.Second))
	if err != nil || cutoff <= tt.SinceID {
		return err
	}

	var expired []int64
	complete := false
	from, offset := cutoff, -1 // include the cutoff message itself
	for page := 0; page < sweepPages && !complete; page++ {
		msgs, err := telegram.TopicHistoryPage(d.tdjson, d.clientID, chatID, topic, from, offset, 100)
		if err != nil {
			return err
		}
		if len(msgs) == 0 {
			complete = true
			break
		}
		for _, m := range msgs {
			if m.ID > cutoff {
				continue
			}
			if m.ID <= tt.SinceID {
				complete = true
				break
			}
			if !keepMessage(m) {
				expired = append(expired, m.ID)
			}
		}
		if last := msgs[len(msgs)-1].ID; last == from && offset == 0 {
			complete = true // no progress: start of the thread
		} else {
			from, offset = last, 0
		}
	}
	if err := d.deleteEach(chatID, expired); err != nil {
		return err
	}
	if !complete {
		return nil // more next sweep; the mark stays so nothing is skipped
	}
	return d.contacts.AdvanceThreadTimer(tt.Transport, tt.NativeID, tt.ThreadID, cutoff)
}

// keepMessage says a thread timer must not delete m: pinned messages stay,
// and the thread's creation notice cannot go without the thread.
func keepMessage(m telegram.Message) bool {
	return m.IsPinned || m.Content.Type == "messageForumTopicCreated"
}

// deleteEach deletes ids in one batch, falling back to one at a time so a
// message this account may not delete does not keep the rest.
func (d *daemon) deleteEach(chatID int64, ids []int64) error {
	if len(ids) == 0 || telegram.DeleteMessages(d.tdjson, d.clientID, chatID, ids) == nil {
		return nil
	}
	failed := 0
	for _, id := range ids {
		if telegram.DeleteMessages(d.tdjson, d.clientID, chatID, []int64{id}) != nil {
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d expired messages could not be deleted (the account needs the delete right)", failed, len(ids))
	}
	return nil
}

// setThreadTimer sets or clears (seconds 0) a thread's timer. It applies to
// messages sent from now on.
func (d *daemon) setThreadTimer(chatID int64, thread string, seconds int32) (client.Response, error) {
	resp := client.Response{ChatID: chatID, Disappear: seconds}
	if seconds > 0 && time.Duration(seconds)*time.Second < minThreadTimer {
		return resp, fmt.Errorf("a thread timer must be at least %s", minThreadTimer)
	}
	topic, err := d.threadID(chatID, thread)
	if err != nil {
		return resp, err
	}
	if topic == 0 {
		return resp, errors.New("name the thread")
	}
	since, err := telegram.LastMessageID(d.tdjson, d.clientID, chatID)
	if err != nil {
		return resp, err
	}
	resp.Topic = &client.Topic{ID: strconv.FormatInt(topic, 10)}
	return resp, d.contacts.SetThreadTimer("telegram", strconv.FormatInt(chatID, 10), resp.Topic.ID, seconds, since)
}

// threadTimers lists a group's thread timers with their thread names.
func (d *daemon) threadTimers(chatID int64) ([]client.ThreadTimer, error) {
	ts, err := d.contacts.ThreadTimers("telegram", strconv.FormatInt(chatID, 10))
	if err != nil || len(ts) == 0 {
		return nil, err
	}
	names := map[string]string{}
	if fts, err := telegram.ForumTopics(d.tdjson, d.clientID, chatID); err == nil {
		for _, ft := range fts {
			names[strconv.FormatInt(ft.ID, 10)] = ft.Name
		}
	}
	out := make([]client.ThreadTimer, 0, len(ts))
	for _, t := range ts {
		out = append(out, client.ThreadTimer{ThreadID: t.ThreadID, Name: names[t.ThreadID], Seconds: t.Seconds})
	}
	return out, nil
}
