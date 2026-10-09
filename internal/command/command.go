package command

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

type Request struct {
	Args    []string
	Env     []string
	Stream  bool
	Secrets []string
}
type Executor interface {
	Execute(context.Context, Request) ([]byte, error)
}
type CLI struct {
	Binary         string
	Stdout, Stderr io.Writer
}

func (c CLI) Execute(ctx context.Context, r Request) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.Binary, r.Args...)
	cmd.Env = r.Env
	var stdout, stderr bytes.Buffer
	if r.Stream {
		cmd.Stdout = &redactingWriter{dst: c.Stdout, secrets: r.Secrets}
		cmd.Stderr = &redactingWriter{dst: c.Stderr, secrets: r.Secrets}
	} else {
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
	}
	if err := cmd.Run(); err != nil {
		// Never include arguments/environment or raw execution errors: CLI output
		// may echo a registration token, and exit errors may contain arguments.
		message := Redact(stderr.String(), r.Secrets)
		if len(message) > 4096 {
			message = message[len(message)-4096:]
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%s %s failed (%s): %s", c.Binary, r.Args[0], exitDescription(err), strings.TrimSpace(message))
	}
	return stdout.Bytes(), nil
}
func exitDescription(err error) string {
	if e, ok := err.(*exec.ExitError); ok {
		return fmt.Sprintf("exit %d", e.ExitCode())
	}
	return "could not execute CLI"
}
func Redact(s string, secrets []string) string {
	for _, secret := range secrets {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "[REDACTED]")
		}
	}
	return s
}

// Secret-bearing commands are captured. If accidentally marked as streaming,
// suppress their output entirely rather than leaking split secret writes.
type redactingWriter struct {
	dst     io.Writer
	secrets []string
}

func (w *redactingWriter) Write(p []byte) (int, error) {
	// Streaming commands currently carry no secrets. Secret-bearing run commands
	// are captured instead, so no raw output is forwarded before redaction.
	if len(w.secrets) != 0 {
		return len(p), nil
	}
	if w.dst == nil {
		return len(p), nil
	}
	return w.dst.Write(p)
}
