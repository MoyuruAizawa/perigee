package container

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"perigee/internal/command"
	"perigee/internal/config"
)

const OwnerKey = "io.perigee.owner"
const RunnerKey = "io.perigee.runner"

type Snapshot struct {
	ID            string `json:"id"`
	Configuration struct {
		ID     string            `json:"id"`
		Labels map[string]string `json:"labels"`
	} `json:"configuration"`
	Status struct {
		State string `json:"state"`
	} `json:"status"`
}

func (s Snapshot) Owned(owner string) bool { return s.Configuration.Labels[OwnerKey] == owner }
func (s Snapshot) RunnerName() string      { return s.Configuration.Labels[RunnerKey] }

type Client struct{ CLI command.Executor }

func (c Client) execute(ctx context.Context, r command.Request) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	return c.CLI.Execute(ctx, r)
}
func (c Client) Check(ctx context.Context) error {
	_, err := c.execute(ctx, command.Request{Args: []string{"system", "status"}})
	if err != nil {
		return fmt.Errorf("container service unavailable; run `container system start`: %w", err)
	}
	return nil
}
func (c Client) List(ctx context.Context) ([]Snapshot, error) {
	data, err := c.execute(ctx, command.Request{Args: []string{"list", "--all", "--format", "json"}})
	if err != nil {
		return nil, err
	}
	var result []Snapshot
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("invalid container list JSON: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("container list must be a JSON array")
	}
	seen := map[string]bool{}
	for _, s := range result {
		if s.ID == "" || s.ID != s.Configuration.ID || s.Status.State == "" || seen[s.ID] {
			return nil, fmt.Errorf("invalid or duplicate container snapshot")
		}
		seen[s.ID] = true
		if s.Status.State != "running" && s.Status.State != "stopped" && s.Status.State != "stopping" && s.Status.State != "unknown" {
			return nil, fmt.Errorf("unsupported container state %q", s.Status.State)
		}
	}
	return result, nil
}
func (c Client) Run(ctx context.Context, cfg config.Config, slot int, name, token string) error {
	labels := append(append([]string{}, cfg.Runner.Labels...), cfg.OwnerLabel())
	args := []string{"run", "--detach", "--name", cfg.ContainerName(slot), "--platform", cfg.Container.Platform, "--init", "--label", OwnerKey + "=" + cfg.Owner(), "--label", RunnerKey + "=" + name}
	for _, key := range []string{"GITHUB_URL", "RUNNER_NAME", "RUNNER_GROUP", "RUNNER_LABELS", "RUNNER_TOKEN"} {
		args = append(args, "--env", key)
	}
	if cfg.Mount.Source != "" {
		args = append(args, "--volume", cfg.Mount.Source+":"+cfg.Mount.Target)
	}
	args = append(args, cfg.Container.RunOptions...)
	args = append(args, cfg.Container.RunnerImageName)
	// Pass the secret through the subprocess environment, never argv.
	env := os.Environ()
	values := map[string]string{"GITHUB_URL": "https://github.com/" + cfg.GitHub.Org, "RUNNER_NAME": name, "RUNNER_GROUP": cfg.Runner.Group, "RUNNER_LABELS": strings.Join(labels, ","), "RUNNER_TOKEN": token}
	filtered := env[:0]
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if _, exists := values[key]; !exists {
			filtered = append(filtered, entry)
		}
	}
	for key, value := range values {
		filtered = append(filtered, key+"="+value)
	}
	_, err := c.execute(ctx, command.Request{Args: args, Env: filtered, Secrets: []string{token}})
	return err
}
func (c Client) Stop(ctx context.Context, id string) error {
	_, err := c.execute(ctx, command.Request{Args: []string{"stop", "--time", "10", id}})
	return err
}
func (c Client) Remove(ctx context.Context, id string) error {
	_, err := c.execute(ctx, command.Request{Args: []string{"delete", id}})
	return err
}
func (c Client) Build(ctx context.Context, cfg config.Config) error {
	dir, err := os.MkdirTemp("", "perigee-build-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	dockerfile := strings.ReplaceAll(RunnerDockerfile, "__SOURCE_IMAGE__", cfg.Container.SourceImage)
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "entrypoint.sh"), []byte(RunnerEntrypoint), 0755); err != nil {
		return err
	}
	arch := strings.TrimPrefix(cfg.Container.Platform, "linux/")
	_, err = c.CLI.Execute(ctx, command.Request{Args: []string{"build", "--platform", cfg.Container.Platform, "--build-arg", "RUNNER_TARGET_ARCH=" + arch, "--tag", cfg.Container.RunnerImageName, dir}, Stream: true})
	return err
}
