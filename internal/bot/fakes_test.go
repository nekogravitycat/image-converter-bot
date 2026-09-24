package bot

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/nekogravitycat/image-converter-bot/internal/config"
	"github.com/nekogravitycat/image-converter-bot/internal/database"
	"github.com/nekogravitycat/image-converter-bot/internal/imageproc"
	"github.com/nekogravitycat/image-converter-bot/internal/worker"
)

// fakeAPI records every Discord REST call. Unset funcs return a message echoing the request.
type fakeAPI struct {
	mu    sync.Mutex
	calls []string

	creates            []discord.MessageCreate
	updates            []discord.MessageUpdate
	interactionUpdates []discord.MessageUpdate
	followups          []discord.MessageCreate

	createFn            func(discord.MessageCreate) (*discord.Message, error)
	getInteractionFn    func() (*discord.Message, error)
	getMessageFn        func() (*discord.Message, error)
	interactionUpdateFn func(discord.MessageUpdate) (*discord.Message, error)

	notify chan string // optional: receives the method name of each call
}

func (f *fakeAPI) record(name string) {
	f.calls = append(f.calls, name)
	if f.notify != nil {
		f.notify <- name
	}
}

func (f *fakeAPI) CreateMessage(_ snowflake.ID, m discord.MessageCreate, _ ...rest.RequestOpt) (*discord.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creates = append(f.creates, m)
	f.record("CreateMessage")
	if f.createFn != nil {
		return f.createFn(m)
	}
	return &discord.Message{ID: 900, Content: m.Content}, nil
}

func (f *fakeAPI) UpdateMessage(_, _ snowflake.ID, m discord.MessageUpdate, _ ...rest.RequestOpt) (*discord.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, m)
	f.record("UpdateMessage")
	return &discord.Message{ID: 900}, nil
}

func (f *fakeAPI) GetMessage(_, _ snowflake.ID, _ ...rest.RequestOpt) (*discord.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("GetMessage")
	return f.getMessageFn()
}

func (f *fakeAPI) GetInteractionResponse(_ snowflake.ID, _ string, _ ...rest.RequestOpt) (*discord.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("GetInteractionResponse")
	return f.getInteractionFn()
}

func (f *fakeAPI) UpdateInteractionResponse(_ snowflake.ID, _ string, m discord.MessageUpdate, _ ...rest.RequestOpt) (*discord.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.interactionUpdates = append(f.interactionUpdates, m)
	f.record("UpdateInteractionResponse")
	if f.interactionUpdateFn != nil {
		return f.interactionUpdateFn(m)
	}
	return &discord.Message{ID: 901}, nil
}

func (f *fakeAPI) CreateFollowupMessage(_ snowflake.ID, _ string, m discord.MessageCreate, _ ...rest.RequestOpt) (*discord.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.followups = append(f.followups, m)
	f.record("CreateFollowupMessage")
	return &discord.Message{}, nil
}

func (f *fakeAPI) callNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// fakeQueue collects jobs instead of running them.
type fakeQueue struct {
	jobs []worker.Job
	cap  int
}

func (q *fakeQueue) TrySubmit(job worker.Job) bool {
	if q.cap > 0 && len(q.jobs) >= q.cap {
		return false
	}
	q.jobs = append(q.jobs, job)
	return true
}

type fakeProcessor struct {
	result *imageproc.Result
	err    error
	got    []byte
	opts   imageproc.Options
}

func (p *fakeProcessor) Process(_ context.Context, data []byte, opts imageproc.Options) (*imageproc.Result, error) {
	p.got, p.opts = data, opts
	return p.result, p.err
}

type testEnv struct {
	bot     *Bot
	api     *fakeAPI
	queue   *fakeQueue
	proc    *fakeProcessor
	configs *config.Service
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	env := &testEnv{
		api:     &fakeAPI{},
		queue:   &fakeQueue{},
		proc:    &fakeProcessor{},
		configs: config.NewService(db),
	}
	env.bot = New(env.api, env.configs, env.queue, env.proc, Settings{
		MaxInputFileSize: 1 << 20,
		ProcessTimeout:   5 * time.Second,
		JobTimeout:       10 * time.Second,
		APITimeout:       5 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return env
}
