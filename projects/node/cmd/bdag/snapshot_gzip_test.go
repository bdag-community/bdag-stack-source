package main

import (
	"testing"

	"github.com/urfave/cli/v2"
)

func TestSnapshotExportGzipLevel(t *testing.T) {
	for _, level := range []int{-1, 1, 2, 9} {
		if err := validateSnapshotGzipLevel(level); err != nil {
			t.Fatal(err)
		}
	}
	for _, level := range []int{-2, 0, 10} {
		if validateSnapshotGzipLevel(level) == nil {
			t.Fatalf("accepted unsupported level %d", level)
		}
	}
	found := false
	for _, sub := range snapshotCmd().Subcommands {
		for _, flag := range sub.Flags {
			if flag.Names()[0] == "gzip-level" {
				if sub.Name != "export" || flag.(*cli.IntFlag).Value != -1 {
					t.Fatal("compression flag changed default or non-export command")
				}
				found = true
			}
		}
	}
	if !found {
		t.Fatal("missing export compression flag")
	}
}
