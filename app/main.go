package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"perigee/internal/command"
	"perigee/internal/config"
	"perigee/internal/container"
	"perigee/internal/github"
	"perigee/internal/lock"
	"perigee/internal/runner"
)

func parseArgs(args []string, out io.Writer) (string, string, error) {
	if len(args) == 0 {
		return "", "", fmt.Errorf("usage: perigee <build|run|up> -c <config.yaml>")
	}
	cmd := args[0]
	if cmd == "-h" || cmd == "--help" || cmd == "help" {
		fmt.Fprintln(out, "usage: perigee <build|run|up> -c <config.yaml>")
		return "", "", flag.ErrHelp
	}
	if cmd != "build" && cmd != "run" && cmd != "up" {
		return "", "", fmt.Errorf("unknown command %q; use build, run or up", cmd)
	}
	flags := flag.NewFlagSet(cmd, flag.ContinueOnError)
	flags.SetOutput(out)
	path := flags.String("c", "", "path to YAML configuration")
	if err := flags.Parse(args[1:]); err != nil {
		return "", "", err
	}
	if *path == "" || flags.NArg() != 0 {
		return "", "", fmt.Errorf("usage: perigee %s -c <config.yaml>", cmd)
	}
	return cmd, *path, nil
}
func execute(ctx context.Context, args []string) error {
	cmd, path, err := parseArgs(args, os.Stderr)
	if err != nil {
		return err
	}
	cfg, err := config.Load(path, cmd)
	if err != nil {
		return err
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return fmt.Errorf("Perigee requires an Apple Silicon Mac")
	}
	cli := container.Client{CLI: command.CLI{Binary: "container", Stdout: os.Stdout, Stderr: os.Stderr}}
	if err := cli.Check(ctx); err != nil {
		return err
	}
	release, err := lock.Acquire(cfg.Owner())
	if err != nil {
		return err
	}
	defer release()
	var gh *github.Client
	if cmd != "build" {
		gh, err = github.New(cfg.GitHub)
		if err != nil {
			return err
		}
		if cfg.Mount.Source != "" {
			info, err := os.Stat(cfg.Mount.Source)
			if err != nil {
				return fmt.Errorf("mount source: %w", err)
			}
			if !info.IsDir() {
				return fmt.Errorf("mount.source must be a directory")
			}
		}
	}
	if cmd != "run" {
		fmt.Fprintf(os.Stdout, "building %s (%s)\n", cfg.Container.RunnerImageName, cfg.Container.Platform)
		if err := cli.Build(ctx, cfg); err != nil {
			return err
		}
	}
	if cmd == "build" {
		return nil
	}
	m := runner.Manager{Config: cfg, Runtime: cli, GitHub: gh, Log: os.Stderr}
	return m.Serve(ctx)
}
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := execute(ctx, os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
