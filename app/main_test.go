package main

import (
	"errors"
	"flag"
	"io"
	"testing"
)

func TestParseArgs(t *testing.T) {
	for _, cmd := range []string{"build", "run", "up"} {
		c, p, err := parseArgs([]string{cmd, "-c", "example/config.yaml"}, io.Discard)
		if err != nil || c != cmd || p != "example/config.yaml" {
			t.Fatalf("%s %s %v", c, p, err)
		}
	}
	for _, args := range [][]string{{}, {"start"}, {"run"}, {"build", "-c", "test", "extra"}} {
		if _, _, err := parseArgs(args, io.Discard); err == nil {
			t.Fatal(args)
		}
	}
	if _, _, err := parseArgs([]string{"--help"}, io.Discard); !errors.Is(err, flag.ErrHelp) {
		t.Fatal(err)
	}
}
