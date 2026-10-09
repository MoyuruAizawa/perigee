package command

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

func TestSecretNeverAppearsInErrorOrStream(t *testing.T) {
	for _, stream := range []bool{false, true} {
		var out bytes.Buffer
		c := CLI{Binary: "/bin/sh", Stdout: &out, Stderr: &out}
		_, err := c.Execute(context.Background(), Request{Args: []string{"-c", `printf '%s' "$PERIGEE_TEST_SECRET" >&2; exit 1`}, Env: append(os.Environ(), "PERIGEE_TEST_SECRET=registration-secret"), Secrets: []string{"registration-secret"}, Stream: stream})
		if err == nil {
			t.Fatal("missing error")
		}
		if strings.Contains(err.Error()+out.String(), "registration-secret") {
			t.Fatal("secret leaked")
		}
		if !stream && !strings.Contains(err.Error(), "[REDACTED]") {
			t.Fatal(err)
		}
	}
}
