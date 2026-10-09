package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const valid = `container:
  sourceImage: ubuntu:24.04
  runnerImageName: test:latest
github:
  org: ExampleOrg
  appId: 1
  installationId: 2
  pem: ~/.ssh/app.pem
runner:
  namePrefix: test
  count: 2
`

func loadYAML(t *testing.T, s, cmd string) (Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(s), 0600); err != nil {
		t.Fatal(err)
	}
	return Load(path, cmd)
}
func TestLoadDefaultsAndValidation(t *testing.T) {
	c, err := loadYAML(t, valid, "up")
	if err != nil {
		t.Fatal(err)
	}
	if c.Container.Platform != "linux/amd64" || c.Runtime.PollIntervalSeconds != 10 {
		t.Fatalf("defaults: %+v", c)
	}
	for name, s := range map[string]string{
		"unknown":            valid + "unknown: true\n",
		"old docker":         strings.Replace(valid, "container:", "docker:", 1),
		"platform":           strings.Replace(valid, "sourceImage:", "platform: linux/riscv64\n  sourceImage:", 1),
		"mount":              valid + "mount:\n  source: /tmp\n",
		"count":              strings.Replace(valid, "count: 2", "count: 0", 1),
		"zero poll":          valid + "runtime:\n  pollIntervalSeconds: 0\n",
		"multiple documents": valid + "---\n{}\n",
		"group newline":      valid + "runner:\n  group: bad\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadYAML(t, s, "up"); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
func TestCommandSpecificRequirements(t *testing.T) {
	if _, err := loadYAML(t, "container:\n  sourceImage: ubuntu:24.04\n  runnerImageName: runner:latest\n", "build"); err != nil {
		t.Fatal(err)
	}
	if _, err := loadYAML(t, strings.Replace(valid, "  sourceImage: ubuntu:24.04\n", "", 1), "run"); err != nil {
		t.Fatal(err)
	}
}
func TestRunOptions(t *testing.T) {
	for _, v := range [][]string{{"--cpus", "2", "--memory=4G"}, {"--network", "default", "--rosetta"}} {
		if err := ValidateRunOptions(v); err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range [][]string{{"--name=other"}, {"--platform", "linux/arm64"}, {"-e", "RUNNER_TOKEN=oops"}, {"-dit"}, {"--env-file", "secret"}, {"--label", "io.perigee.owner=x"}, {"--entrypoint", "sh"}, {"--user", "nobody"}, {"--rm"}, {"--cpus"}, {"--cpus", "--name"}, {"ubuntu:latest"}, {"--"}, {"--cpus="}, {"--rosetta=false"}} {
		if err := ValidateRunOptions(v); err == nil {
			t.Fatalf("accepted %v", v)
		}
	}
}
func TestIdentityStableAcrossCount(t *testing.T) {
	c, err := loadYAML(t, valid, "up")
	if err != nil {
		t.Fatal(err)
	}
	a := c.Owner()
	c.Runner.Count = 8
	if c.Owner() != a {
		t.Fatal("count changed pool identity")
	}
	c.Runner.NamePrefix = "other"
	if c.Owner() == a {
		t.Fatal("prefix did not change identity")
	}
}

func TestSourceImageInjection(t *testing.T) {
	c := Config{Container: Container{SourceImage: "ubuntu:24.04\nRUN bad", RunnerImageName: "test:latest", Platform: "linux/amd64"}, Runtime: Runtime{PollIntervalSeconds: 10}}
	if c.Validate("build") == nil {
		t.Fatal("accepted Dockerfile injection")
	}
}
