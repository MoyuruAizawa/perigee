package container

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"perigee/internal/command"
	"perigee/internal/config"
)

// Opt in: this downloads images and starts the Apple container builder/VMs.
// No GitHub credentials are used. Assets have unique names and are removed.
func TestAppleContainerIntegration(t *testing.T) {
	if os.Getenv("PERIGEE_CONTAINER_INTEGRATION") != "1" {
		t.Skip("set PERIGEE_CONTAINER_INTEGRATION=1 on an Apple Silicon Mac with container system started")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	exec := command.CLI{Binary: "container", Stdout: os.Stdout, Stderr: os.Stderr}
	c := Client{CLI: exec}
	if err := c.Check(ctx); err != nil {
		t.Fatal(err)
	}
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			id := fmt.Sprintf("perigee-smoke-%s-%d", arch, time.Now().UnixNano())
			source, image := id+"-source:latest", id+"-runner:latest"
			buildDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(buildDir, "Dockerfile"), []byte("FROM ubuntu:24.04\nENV PERIGEE_SOURCE_IMAGE_TEST=local\n"), 0600); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				cleanup, done := context.WithTimeout(context.Background(), time.Minute)
				defer done()
				if items, err := c.List(cleanup); err == nil {
					for _, s := range items {
						if s.ID == id {
							c.Stop(cleanup, id)
							c.Remove(cleanup, id)
						}
					}
				}
				for _, tag := range []string{image, source} {
					if _, err := exec.Execute(cleanup, command.Request{Args: []string{"image", "delete", tag}}); err != nil {
						t.Logf("image cleanup %s: %v", tag, err)
					}
				}
			})
			if _, err := exec.Execute(ctx, command.Request{Args: []string{"build", "--platform", "linux/" + arch, "--tag", source, buildDir}, Stream: true}); err != nil {
				t.Fatal(err)
			}
			cfg := config.Config{Container: config.Container{SourceImage: source, RunnerImageName: image, Platform: "linux/" + arch}}
			if err := c.Build(ctx, cfg); err != nil {
				t.Fatal(err)
			}
			version, err := exec.Execute(ctx, command.Request{Args: []string{"run", "--rm", "--platform", "linux/" + arch, "--entrypoint", "/bin/bash", image, "-lc", `test "$PERIGEE_SOURCE_IMAGE_TEST" = local && ./bin/Runner.Listener --version`}})
			if err != nil || strings.TrimSpace(string(version)) != "2.338.0" {
				t.Fatalf("runner executable: %s, %v", version, err)
			}
			cache := t.TempDir()
			// The fake listener isolates signal behavior from GitHub/network state.
			// Its process-group trap resembles the real run.sh manual signal mode.
			script := `cat > /actions-runner/config.sh <<'CONFIG'
#!/usr/bin/env bash
printf '%s\n' "$@" > /cache/config-args
CONFIG
cat > /actions-runner/run.sh <<'RUN'
#!/usr/bin/env bash
set -m
test -z "${RUNNER_TOKEN+x}" || exit 91
test "$RUNNER_MANUALLY_TRAP_SIG" = 1 || exit 92
sleep 300 &
pid=$!
trap 'kill -TERM -- "-$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null; echo stopped > /cache/stopped; exit 23' TERM INT
echo ready > /cache/ready
wait "$pid"
RUN
chmod +x /actions-runner/config.sh /actions-runner/run.sh
exec /entrypoint.sh`
			args := []string{"run", "--detach", "--name", id, "--platform", "linux/" + arch, "--init", "--label", OwnerKey + "=smoke", "--volume", cache + ":/cache", "--env", "GITHUB_URL=https://github.com/example", "--env", "RUNNER_TOKEN=dummy-smoke-token", "--env", "RUNNER_NAME=smoke", "--entrypoint", "/bin/bash", image, "-lc", script}
			if _, err := exec.Execute(ctx, command.Request{Args: args}); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(30 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(cache, "ready")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					logs, _ := exec.Execute(ctx, command.Request{Args: []string{"logs", id}})
					t.Fatalf("listener did not start: %s", logs)
				}
				time.Sleep(100 * time.Millisecond)
			}
			items, err := c.List(ctx)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, s := range items {
				if s.ID == id && s.Owned("smoke") && s.Status.State == "running" {
					found = true
				}
			}
			if !found {
				t.Fatal("real CLI list schema or ownership mismatch")
			}
			configArgs, err := os.ReadFile(filepath.Join(cache, "config-args"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(configArgs), "--ephemeral") || strings.Contains(string(configArgs), "--replace") {
				t.Fatal("incorrect registration flags")
			}
			if err := c.Stop(ctx, id); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(cache, "stopped")); err != nil {
				t.Fatal("signal was not forwarded to listener")
			}
			if err := c.Remove(ctx, id); err != nil {
				t.Fatal(err)
			}
		})
	}
}
