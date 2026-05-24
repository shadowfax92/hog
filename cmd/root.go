package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Version is stamped at build time via -ldflags "-X hog/cmd.Version=...".
var Version = "dev"

var rootCmd = &cobra.Command{
	Use:           "hog",
	Short:         "Find the apps hogging your Mac's CPU and memory",
	Long:          "hog samples running processes for a few seconds, groups them by their owning app\n(so Chrome's many helpers read as one app), and prints a color-coded table of the\nheaviest CPU and memory users. Use `hog kill <app>` to terminate a heavy group.",
	Version:       Version,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the root command and is the single entry point from main.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
