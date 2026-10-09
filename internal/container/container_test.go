package container

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"perigee/internal/command"
	"perigee/internal/config"
)

type fakeCLI struct {
	requests  []command.Request
	data      []byte
	err       error
	buildFile string
}

func (f *fakeCLI) Execute(_ context.Context, r command.Request) ([]byte, error) {
	f.requests = append(f.requests, r)
	if len(r.Args) > 0 && r.Args[0] == "build" {
		b, e := os.ReadFile(filepath.Join(r.Args[len(r.Args)-1], "Dockerfile"))
		if e != nil {
			return nil, e
		}
		f.buildFile = string(b)
	}
	return f.data, f.err
}
func TestPlatformAndSecretTransport(t *testing.T) {
	for _, platform := range []string{"linux/amd64", "linux/arm64"} {
		t.Run(platform, func(t *testing.T) {
			f := &fakeCLI{}
			c := Client{CLI: f}
			cfg := config.Config{Container: config.Container{SourceImage: "ubuntu:24.04", RunnerImageName: "runner:latest", Platform: platform}, GitHub: config.GitHub{Org: "test"}, Runner: config.Runner{NamePrefix: "test", Labels: []string{"android"}}, Mount: config.Mount{Source: "/tmp/cache", Target: "/cache"}}
			if err := c.Build(context.Background(), cfg); err != nil {
				t.Fatal(err)
			}
			build := f.requests[0]
			if !strings.Contains(strings.Join(build.Args, " "), "--platform "+platform) {
				t.Fatal(build.Args)
			}
			if !strings.Contains(f.buildFile, "FROM ubuntu:24.04") {
				t.Fatal("missing source")
			}
			if _, err := os.Stat(build.Args[len(build.Args)-1]); !os.IsNotExist(err) {
				t.Fatal("build context was not removed")
			}
			t.Setenv("RUNNER_TOKEN", "old-value")
			if err := c.Run(context.Background(), cfg, 0, "test-unique", "secret-value"); err != nil {
				t.Fatal(err)
			}
			r := f.requests[1]
			args := strings.Join(r.Args, " ")
			if strings.Contains(args, "secret-value") || !strings.Contains(args, "--platform "+platform) || !strings.Contains(args, "--env RUNNER_TOKEN") || !strings.Contains(args, "--volume /tmp/cache:/cache") {
				t.Fatal(args)
			}
			found := 0
			for _, e := range r.Env {
				if strings.HasPrefix(e, "RUNNER_TOKEN=") {
					found++
					if e != "RUNNER_TOKEN=secret-value" {
						t.Fatal(e)
					}
				}
			}
			if found != 1 {
				t.Fatal("duplicate or missing token")
			}
		})
	}
}
func TestListSchema(t *testing.T) {
	valid := `[{"id":"test","configuration":{"id":"test","labels":{"io.perigee.owner":"owner"}},"status":{"state":"running","networks":[]}}]`
	f := &fakeCLI{data: []byte(valid)}
	c := Client{CLI: f}
	items, err := c.List(context.Background())
	if err != nil || len(items) != 1 || !items[0].Owned("owner") {
		t.Fatalf("%v %v", items, err)
	}
	for _, data := range []string{"null", "{}", "not JSON", `[{"id":"test","configuration":{"id":"test"},"status":"running"}]`, `[{"id":"test","configuration":{"id":"test"},"status":{"state":"new-state"}}]`, `[{"id":"test","configuration":{"id":"other"},"status":{"state":"running"}}]`} {
		f.data = []byte(data)
		if _, err := c.List(context.Background()); err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
	f.data = []byte("[]")
	if _, err := c.List(context.Background()); err != nil {
		t.Fatal(err)
	}
}
