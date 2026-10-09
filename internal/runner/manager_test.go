package runner

import (
	"context"
	"errors"
	"io"
	"testing"

	"perigee/internal/config"
	"perigee/internal/container"
)

type fakeRuntime struct {
	items                []container.Snapshot
	listErr              error
	runs, stops, removes []string
	runErr               map[int]error
	stopErr              map[string]error
}

func (f *fakeRuntime) List(context.Context) ([]container.Snapshot, error) {
	return append([]container.Snapshot{}, f.items...), f.listErr
}
func (f *fakeRuntime) Run(_ context.Context, c config.Config, slot int, name, token string) error {
	f.runs = append(f.runs, name)
	if err := f.runErr[slot]; err != nil {
		return err
	}
	f.items = append(f.items, snapshot(c.ContainerName(slot), c.Owner(), name, "running"))
	return nil
}
func (f *fakeRuntime) Stop(_ context.Context, id string) error {
	f.stops = append(f.stops, id)
	return f.stopErr[id]
}
func (f *fakeRuntime) Remove(_ context.Context, id string) error {
	f.removes = append(f.removes, id)
	for i, s := range f.items {
		if s.ID == id {
			f.items = append(f.items[:i], f.items[i+1:]...)
			break
		}
	}
	return nil
}

type deletion struct{ selected, keep map[string]bool }
type fakeGitHub struct {
	tokens        int
	tokenFailures int
	deletions     []deletion
}

func (f *fakeGitHub) RegistrationToken(context.Context) (string, error) {
	f.tokens++
	if f.tokens <= f.tokenFailures {
		return "", errors.New("token unavailable")
	}
	return "secret", nil
}
func (f *fakeGitHub) DeleteOwned(_ context.Context, _ string, selection, keep map[string]bool) error {
	f.deletions = append(f.deletions, deletion{selection, keep})
	return nil
}
func snapshot(id, owner, name, state string) container.Snapshot {
	var s container.Snapshot
	s.ID = id
	s.Configuration.ID = id
	s.Configuration.Labels = map[string]string{container.OwnerKey: owner, container.RunnerKey: name}
	s.Status.State = state
	return s
}
func manager() (*Manager, *fakeRuntime, *fakeGitHub) {
	r := &fakeRuntime{}
	g := &fakeGitHub{}
	c := config.Config{Container: config.Container{RunnerImageName: "runner:latest"}, GitHub: config.GitHub{Org: "test"}, Runner: config.Runner{NamePrefix: "test", Count: 2}, Runtime: config.Runtime{PollIntervalSeconds: 1}}
	return &Manager{Config: c, Runtime: r, GitHub: g, Log: io.Discard}, r, g
}
func TestMaintainAndReplaceAfterJob(t *testing.T) {
	m, r, g := manager()
	ctx := context.Background()
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.runs) != 2 || g.tokens != 2 {
		t.Fatal("did not fill pool")
	}
	first := r.runs[0]
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.runs) != 2 {
		t.Fatal("restarted live runner")
	}
	r.items[0].Status.State = "stopped"
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.runs) != 3 || len(r.removes) != 1 || len(r.stops) != 0 || r.runs[2] == first || g.tokens != 3 {
		t.Fatal("did not replace with new runner and token")
	}
}
func TestListFailureDoesNotMutate(t *testing.T) {
	m, r, g := manager()
	r.listErr = errors.New("network unavailable")
	if m.Reconcile(context.Background()) == nil {
		t.Fatal("missing error")
	}
	if len(r.runs)+len(r.removes)+len(r.stops)+g.tokens+len(g.deletions) != 0 {
		t.Fatal("mutated after list failure")
	}
}
func TestOwnershipCollisionStopsBeforeMutation(t *testing.T) {
	m, r, g := manager()
	r.items = []container.Snapshot{snapshot(m.Config.ContainerName(1), "other", "foreign", "stopped")}
	if m.Reconcile(context.Background()) == nil {
		t.Fatal("missing collision error")
	}
	if len(r.runs)+len(r.removes)+len(g.deletions) != 0 {
		t.Fatal("mutated before checking all slots")
	}
}
func TestPartialFailuresContinue(t *testing.T) {
	m, r, g := manager()
	g.tokenFailures = 1
	if m.Reconcile(context.Background()) == nil {
		t.Fatal("missing failure")
	}
	if len(r.runs) != 1 || g.tokens != 2 {
		t.Fatal("failed slot blocked remaining slots")
	}
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(r.runs) != 2 {
		t.Fatal("did not retry failed slot")
	}
}
func TestCleanupOwnershipAndFailures(t *testing.T) {
	m, r, g := manager()
	owner := m.Config.Owner()
	r.items = []container.Snapshot{snapshot("one", owner, "runner-one", "running"), snapshot("two", owner, "runner-two", "running"), snapshot("foreign", "other", "runner-foreign", "running")}
	r.stopErr = map[string]error{"one": errors.New("stop failed")}
	if m.Cleanup(context.Background()) == nil {
		t.Fatal("missing error")
	}
	if len(r.stops) != 2 || len(r.removes) != 1 || r.removes[0] != "two" {
		t.Fatal("did not attempt all owned containers")
	}
	last := g.deletions[len(g.deletions)-1]
	if !last.keep["runner-one"] || last.keep["runner-foreign"] {
		t.Fatal("unsafe registration cleanup")
	}
}
func TestRecoverCrashKeepsRunningRegistrations(t *testing.T) {
	m, r, g := manager()
	r.items = []container.Snapshot{snapshot(m.Config.ContainerName(0), m.Config.Owner(), "live", "running"), snapshot(m.Config.ContainerName(1), m.Config.Owner(), "dead", "stopped")}
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !g.deletions[0].keep["live"] || g.deletions[0].keep["dead"] {
		t.Fatal("unsafe crash recovery")
	}
}
func TestCountReductionAndUnknownState(t *testing.T) {
	m, r, _ := manager()
	m.Config.Runner.Count = 1
	r.items = []container.Snapshot{snapshot(m.Config.ContainerName(0), m.Config.Owner(), "uncertain", "unknown"), snapshot(m.Config.ContainerName(1), m.Config.Owner(), "extra", "running")}
	if m.Reconcile(context.Background()) == nil {
		t.Fatal("missing unknown-state error")
	}
	if len(r.removes) != 1 || len(r.runs) != 0 {
		t.Fatal("unexpected state mutation")
	}
}
func TestCanceledServeCleansUp(t *testing.T) {
	m, r, _ := manager()
	r.items = []container.Snapshot{snapshot(m.Config.ContainerName(0), m.Config.Owner(), "live", "running")}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.Serve(ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.removes) != 1 {
		t.Fatal("cancellation did not clean up")
	}
}
