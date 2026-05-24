package cmd

import (
	"bytes"
	"strings"
	"testing"

	"hog/internal/group"
)

func TestRootMetadata(t *testing.T) {
	if rootCmd.Use != "hog" {
		t.Errorf("rootCmd.Use = %q, want %q", rootCmd.Use, "hog")
	}
	if rootCmd.Short == "" {
		t.Error("rootCmd.Short is empty")
	}
}

func TestVersionFlagPrintsVersion(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"--version"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(--version) error = %v", err)
	}
	if !strings.Contains(buf.String(), Version) {
		t.Fatalf("--version output %q does not contain version %q", buf.String(), Version)
	}
}

func TestTopN(t *testing.T) {
	groups := []group.Group{{App: "a"}, {App: "b"}, {App: "c"}}
	if got := topN(groups, 2); len(got) != 2 {
		t.Errorf("topN(_, 2) len = %d, want 2", len(got))
	}
	if got := topN(groups, 5); len(got) != 3 {
		t.Errorf("topN(_, 5) len = %d, want 3 (n >= len is all)", len(got))
	}
	if got := topN(groups, 0); len(got) != 3 {
		t.Errorf("topN(_, 0) len = %d, want 3 (0 is all)", len(got))
	}
}
