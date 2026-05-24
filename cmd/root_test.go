package cmd

import (
	"bytes"
	"strings"
	"testing"
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
