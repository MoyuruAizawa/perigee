package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Container Container `yaml:"container"`
	GitHub    GitHub    `yaml:"github"`
	Runner    Runner    `yaml:"runner"`
	Mount     Mount     `yaml:"mount"`
	Runtime   Runtime   `yaml:"runtime"`
}
type Container struct {
	SourceImage     string   `yaml:"sourceImage"`
	RunnerImageName string   `yaml:"runnerImageName"`
	Platform        string   `yaml:"platform"`
	RunOptions      []string `yaml:"runOptions"`
}
type GitHub struct {
	Org            string `yaml:"org"`
	AppID          int64  `yaml:"appId"`
	InstallationID int64  `yaml:"installationId"`
	PEM            string `yaml:"pem"`
}
type Runner struct {
	Group      string   `yaml:"group"`
	Labels     []string `yaml:"labels"`
	NamePrefix string   `yaml:"namePrefix"`
	Count      int      `yaml:"count"`
}
type Mount struct {
	Source string `yaml:"source"`
	Target string `yaml:"target"`
}
type Runtime struct {
	PollIntervalSeconds int `yaml:"pollIntervalSeconds"`
}

func Load(path, command string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()
	c := Config{Container: Container{Platform: "linux/amd64"}, Runtime: Runtime{PollIntervalSeconds: 10}}
	d := yaml.NewDecoder(f)
	d.KnownFields(true)
	if err := d.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return Config{}, fmt.Errorf("config must contain exactly one YAML document")
	}
	if err := c.Validate(command); err != nil {
		return Config{}, err
	}
	if c.Mount.Source != "" {
		c.Mount.Source, err = ExpandHome(c.Mount.Source)
		if err != nil {
			return Config{}, err
		}
		if !filepath.IsAbs(c.Mount.Source) {
			c.Mount.Source = filepath.Join(filepath.Dir(path), c.Mount.Source)
		}
		c.Mount.Source, err = filepath.Abs(c.Mount.Source)
		if err != nil {
			return Config{}, err
		}
	}
	return c, nil
}

var imagePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/:@-]*$`)
var prefixPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,39}$`)
var orgPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*$`)

func (c Config) Validate(command string) error {
	if command != "build" && command != "run" && command != "up" {
		return fmt.Errorf("unknown command %q", command)
	}
	if !imagePattern.MatchString(c.Container.RunnerImageName) {
		return fmt.Errorf("container.runnerImageName must be a valid image reference")
	}
	if command != "run" && !imagePattern.MatchString(c.Container.SourceImage) {
		return fmt.Errorf("container.sourceImage must be a valid image reference")
	}
	if c.Container.Platform != "linux/amd64" && c.Container.Platform != "linux/arm64" {
		return fmt.Errorf("container.platform must be linux/amd64 or linux/arm64")
	}
	if err := ValidateRunOptions(c.Container.RunOptions); err != nil {
		return err
	}
	if (c.Mount.Source == "") != (c.Mount.Target == "") {
		return fmt.Errorf("mount.source and mount.target must be specified together")
	}
	if c.Mount.Target != "" && (!strings.HasPrefix(c.Mount.Target, "/") || strings.ContainsAny(c.Mount.Source+c.Mount.Target, ":\n\r\x00")) {
		return fmt.Errorf("mount requires an absolute target and paths without colons or newlines")
	}
	if c.Runtime.PollIntervalSeconds <= 0 || c.Runtime.PollIntervalSeconds > 86400 {
		return fmt.Errorf("runtime.pollIntervalSeconds must be between 1 and 86400")
	}
	if command == "build" {
		return nil
	}
	if !orgPattern.MatchString(c.GitHub.Org) {
		return fmt.Errorf("github.org must be an organization name")
	}
	if c.GitHub.AppID <= 0 || c.GitHub.InstallationID <= 0 || strings.TrimSpace(c.GitHub.PEM) == "" {
		return fmt.Errorf("github.appId, github.installationId and github.pem are required")
	}
	if !prefixPattern.MatchString(c.Runner.NamePrefix) {
		return fmt.Errorf("runner.namePrefix must contain 1–40 letters, digits, dots, underscores or hyphens and start with a letter or digit")
	}
	if c.Runner.Count <= 0 || c.Runner.Count > 1000 {
		return fmt.Errorf("runner.count must be between 1 and 1000")
	}
	for _, l := range c.Runner.Labels {
		if strings.TrimSpace(l) == "" || strings.ContainsAny(l, ",\n\r\x00") || strings.HasPrefix(l, "perigee-") {
			return fmt.Errorf("runner.labels contains an empty, invalid or reserved label")
		}
	}
	if strings.ContainsAny(c.Runner.Group, "\n\r\x00") {
		return fmt.Errorf("runner.group must not contain newlines")
	}
	return nil
}

// Only allow additive resource/network options. Unknown options are rejected so
// aliases, combined short flags and future CLI options cannot override ownership.
func ValidateRunOptions(options []string) error {
	values := map[string]bool{"--cpus": true, "-c": true, "--memory": true, "-m": true, "--network": true, "--dns": true, "--dns-domain": true, "--dns-option": true, "--dns-search": true, "--publish": true, "-p": true, "--shm-size": true, "--ulimit": true}
	flags := map[string]bool{"--rosetta": true, "--no-dns": true}
	for i := 0; i < len(options); i++ {
		option, value, inline := strings.Cut(options[i], "=")
		if flags[option] && !inline {
			continue
		}
		if !values[option] {
			return fmt.Errorf("container.runOptions: unsupported or managed option %q", option)
		}
		if !inline {
			i++
			if i >= len(options) {
				return fmt.Errorf("container.runOptions: %s requires a value", option)
			}
			value = options[i]
		}
		if strings.TrimSpace(value) == "" || strings.HasPrefix(value, "-") || strings.ContainsAny(value, "\n\r\x00") {
			return fmt.Errorf("container.runOptions: invalid value for %s", option)
		}
	}
	return nil
}

func ExpandHome(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, path[2:]), nil
	}
	if strings.HasPrefix(path, "~") {
		return "", fmt.Errorf("only ~ and ~/ paths are supported")
	}
	return path, nil
}

// The identity is stable across config file copies and runner count changes.
func (c Config) Owner() string {
	sum := sha256.Sum256([]byte(strings.ToLower(c.GitHub.Org) + "\x00" + c.Runner.NamePrefix + "\x00" + c.Container.RunnerImageName))
	return hex.EncodeToString(sum[:8])
}
func (c Config) OwnerLabel() string { return "perigee-" + c.Owner() }
func (c Config) ContainerName(slot int) string {
	return fmt.Sprintf("perigee-%s-%d", c.Owner(), slot+1)
}
