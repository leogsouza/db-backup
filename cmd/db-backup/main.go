package main

import (
	"context"
	"fmt"
	"os"

	"github.com/leogsouza/db-backup/internal/cli"
	"github.com/leogsouza/db-backup/internal/run"
)

func main() {
	opts, err := cli.Parse(os.Args[1:], os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(2)
	}
	if err := run.Execute(context.Background(), opts); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}
