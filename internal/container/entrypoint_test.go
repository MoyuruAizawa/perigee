package container

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestEntrypointProcessLifecycle(t *testing.T) {
	for _, scenario := range []string{"complete", "registration failure", "stop registration", "stop listener"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			configScript := `#!/usr/bin/env bash
printf '%s\n' "$@" > config-args
`
			runScript := `#!/usr/bin/env bash
test -z "${RUNNER_TOKEN+x}" || exit 91
test "$RUNNER_MANUALLY_TRAP_SIG" = 1 || exit 92
echo ran > ran
exit 23
`
			blocking := `sleep 30 &
pid=$!
trap 'kill -TERM "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null; echo stopped > stopped; exit 23' TERM
echo ready > ready
wait "$pid"
`
			if scenario == "registration failure" {
				configScript += "exit 7\n"
			}
			if scenario == "stop registration" {
				configScript += blocking
			}
			if scenario == "stop listener" {
				runScript = strings.Replace(runScript, "exit 23\n", blocking, 1)
			}
			entrypoint := strings.Replace(RunnerEntrypoint, "cd /actions-runner", "cd \""+dir+"\"", 1)
			for file, contents := range map[string]string{"config.sh": configScript, "run.sh": runScript, "entrypoint.sh": entrypoint} {
				if err := os.WriteFile(filepath.Join(dir, file), []byte(contents), 0755); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/bin/bash", filepath.Join(dir, "entrypoint.sh"))
			cmd.Env = append(os.Environ(), "GITHUB_URL=https://github.com/example", "RUNNER_NAME=unique", "RUNNER_TOKEN=dummy", "RUNNER_WORKDIR=_work", "RUNNER_GROUP=group", "RUNNER_LABELS=label")
			var out bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &out
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(scenario, "stop") {
				for {
					if _, err := os.Stat(filepath.Join(dir, "ready")); err == nil {
						break
					}
					if ctx.Err() != nil {
						cmd.Wait()
						t.Fatalf("not ready: %s", out.String())
					}
					time.Sleep(10 * time.Millisecond)
				}
				if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
					t.Fatal(err)
				}
			}
			err := cmd.Wait()
			if ctx.Err() != nil {
				t.Fatalf("lifecycle timed out: %s", out.String())
			}
			want := 23
			if scenario == "registration failure" {
				want = 7
			}
			if scenario == "stop registration" {
				want = 130
			}
			if e, ok := err.(*exec.ExitError); !ok || e.ExitCode() != want {
				t.Fatalf("exit %v, want %d; output: %s", err, want, out.String())
			}
			if strings.HasPrefix(scenario, "stop") {
				if _, err := os.Stat(filepath.Join(dir, "stopped")); err != nil {
					t.Fatal("child did not handle stop signal")
				}
			}
			if scenario == "registration failure" || scenario == "stop registration" {
				if _, err := os.Stat(filepath.Join(dir, "ran")); !os.IsNotExist(err) {
					t.Fatal("listener started after registration failed or stopped")
				}
			}
			args, err := os.ReadFile(filepath.Join(dir, "config-args"))
			if err != nil {
				t.Fatal(err)
			}
			for _, flag := range []string{"--ephemeral", "--runnergroup", "--labels"} {
				if !strings.Contains(string(args), flag) {
					t.Fatalf("missing %s", flag)
				}
			}
		})
	}
}
