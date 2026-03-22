package main

import (
	"flag"
	"io"
	"strings"
)

type startupOptions struct {
	Action               string
	Path                 string
	Recursive            bool
	InstallIntegration   bool
	UninstallIntegration bool
}

func parseStartupOptions(args []string) startupOptions {
	opts := startupOptions{}
	fs := flag.NewFlagSet("tigrisfs-gui", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.Action, "integration-action", "", "integration action: show|pin|unpin|unmount")
	fs.StringVar(&opts.Path, "integration-path", "", "absolute mounted path for integration actions")
	fs.BoolVar(&opts.Recursive, "integration-recursive", false, "apply action recursively for directories")
	fs.BoolVar(&opts.InstallIntegration, "install-file-manager-integration", false, "install Finder/Explorer integration")
	fs.BoolVar(&opts.UninstallIntegration, "uninstall-file-manager-integration", false, "uninstall Finder/Explorer integration")
	_ = fs.Parse(args)

	opts.Action = strings.ToLower(strings.TrimSpace(opts.Action))
	opts.Path = strings.TrimSpace(opts.Path)
	return opts
}
