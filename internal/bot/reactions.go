package bot

import (
	"context"
	"log/slog"
	"sync"

	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

// Reactions the bot adds to a source message while its attachments are queued
// or being converted, so the user gets feedback before the result (or an
// error reply) arrives. They are cleared once every attachment job for the
// message has finished, since the reply already carries the outcome.
const (
	emojiQueued     = "⏳"  // ⏳
	emojiProcessing = "⚙️" // ⚙️
)

// reactionTracker counts in-flight jobs per source message, so a message with
// several image attachments keeps its reaction until the last one finishes,
// and so the queued->processing swap happens only once per message.
type reactionTracker struct {
	mu    sync.Mutex
	state map[snowflake.ID]*reactionState
}

type reactionState struct {
	remaining  int
	processing bool
	failed     bool // at least one attachment for this message did not convert successfully
}

func newReactionTracker() *reactionTracker {
	return &reactionTracker{state: make(map[snowflake.ID]*reactionState)}
}

// markQueued records one more in-flight job for messageID and, the first time,
// reacts with emojiQueued. Call once per attachment right after it is
// successfully submitted to the work queue (before the job can start running).
func (b *Bot) markQueued(ctx context.Context, channelID, messageID snowflake.ID, log *slog.Logger) {
	t := b.reactions
	t.mu.Lock()
	st, ok := t.state[messageID]
	if !ok {
		st = &reactionState{}
		t.state[messageID] = st
	}
	st.remaining++
	first := st.remaining == 1
	t.mu.Unlock()

	if first {
		b.addReaction(ctx, channelID, messageID, emojiQueued, log)
	}
}

// markProcessing swaps the queued reaction for the processing one. It is a
// no-op if markQueued was never called for messageID (e.g. direct calls to
// runAutoJob in tests) or if the swap already happened for this message.
func (b *Bot) markProcessing(ctx context.Context, channelID, messageID snowflake.ID, log *slog.Logger) {
	t := b.reactions
	t.mu.Lock()
	st, ok := t.state[messageID]
	if !ok || st.processing {
		t.mu.Unlock()
		return
	}
	st.processing = true
	t.mu.Unlock()

	b.addReaction(ctx, channelID, messageID, emojiProcessing, log)
	b.removeReaction(ctx, channelID, messageID, emojiQueued, log)
}

// markDone records that one job for messageID finished, and clears the reaction once every
// job for that message has finished. ok reports whether that job's attachment converted
// successfully; last reports whether this call was the message's last outstanding job, and
// allOK (only meaningful when last is true) reports whether every attachment on the message
// converted successfully.
func (b *Bot) markDone(ctx context.Context, channelID, messageID snowflake.ID, ok bool, log *slog.Logger) (last, allOK bool) {
	t := b.reactions
	t.mu.Lock()
	st, found := t.state[messageID]
	if !found {
		t.mu.Unlock()
		return false, false
	}
	st.remaining--
	if !ok {
		st.failed = true
	}
	last = st.remaining <= 0
	processing, failed := st.processing, st.failed
	if last {
		delete(t.state, messageID)
	}
	t.mu.Unlock()

	if !last {
		return false, false
	}
	if processing {
		b.removeReaction(ctx, channelID, messageID, emojiProcessing, log)
	} else {
		b.removeReaction(ctx, channelID, messageID, emojiQueued, log)
	}
	return true, !failed
}

func (b *Bot) addReaction(ctx context.Context, channelID, messageID snowflake.ID, emoji string, log *slog.Logger) {
	actx, cancel := b.apiCtx(ctx)
	defer cancel()
	if err := b.api.AddReaction(channelID, messageID, emoji, rest.WithCtx(actx)); err != nil {
		log.Warn("add reaction", slog.String("emoji", emoji), slog.Any("err", err))
	}
}

func (b *Bot) removeReaction(ctx context.Context, channelID, messageID snowflake.ID, emoji string, log *slog.Logger) {
	actx, cancel := b.apiCtx(ctx)
	defer cancel()
	if err := b.api.RemoveOwnReaction(channelID, messageID, emoji, rest.WithCtx(actx)); err != nil {
		if rest.IsJSONErrorCode(err, rest.JSONErrorCodeUnknownMessage) {
			return // the source message was deleted while we were working on it
		}
		log.Warn("remove reaction", slog.String("emoji", emoji), slog.Any("err", err))
	}
}
