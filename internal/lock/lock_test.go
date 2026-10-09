package lock

import "testing"

func TestExclusiveAndReleased(t *testing.T) {
	dir := t.TempDir()
	release, err := acquireAt(dir, "pool")
	if err != nil {
		t.Fatal(err)
	}
	if another, err := acquireAt(dir, "pool"); err == nil {
		another()
		t.Fatal("duplicate lock acquired")
	}
	release()
	release, err = acquireAt(dir, "pool")
	if err != nil {
		t.Fatal(err)
	}
	release()
}
