package proc

import (
	"os/exec"
	"runtime"
	"testing"
)

// A running tree's resident memory is read, and none once it was waited
// for: its pid may be another process's by then.
func TestTreeRSS(t *testing.T) {
	name, args := "sleep", []string{"30"}
	if runtime.GOOS == "windows" {
		name, args = "ping", []string{"-n", "30", "127.0.0.1"}
	}
	if _, err := exec.LookPath(name); err != nil {
		t.Skip("no " + name)
	}
	tree, err := StartTree(exec.Command(name, args...))
	if err != nil {
		t.Fatal(err)
	}
	rss, ok := tree.RSS()
	if !ok || rss < 64<<10 || rss > 1<<30 {
		t.Fatalf("a running %s holds %d bytes (%v)", name, rss, ok)
	}
	tree.Kill()
	_ = tree.Wait()
	if _, ok := tree.RSS(); ok {
		t.Fatal("a tree waited for was read")
	}
	if MemTotal() <= 0 && (runtime.GOOS == "darwin" || runtime.GOOS == "linux" || runtime.GOOS == "windows") {
		t.Fatal("this computer's memory isn't known")
	}
}
