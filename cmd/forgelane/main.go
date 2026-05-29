package main

import (
	"os"

	"github.com/liiujinfu/forgelane/internal/cli"
)

func main() {
	cmd := cli.NewRootCommand(cli.Options{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})

	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
