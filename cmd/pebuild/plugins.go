// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/nradchenko/pe-builder/action"
	"github.com/nradchenko/pe-builder/plugin"
)

func newPluginsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugins",
		Short: "Discover, inspect, and install plugins",
	}
	cmd.AddCommand(newPluginsListCmd(), newPluginsInfoCmd(), newPluginsAddCmd())
	return cmd
}

func newPluginsAddCmd() *cobra.Command {
	var (
		local bool
		force bool
	)
	cmd := &cobra.Command{
		Use:   "add <archive>",
		Short: "Install a plugin from a .cab or .zip archive",
		Long: "Extract a .cab (BartPE-native) or .zip archive into a plugin directory so\n" +
			"the build reads it in place. Installs into the per-user global plugins dir by\n" +
			"default, or the project ./plugins directory with --local.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dest := plugin.GlobalDir()
			if local {
				dest = plugin.LocalPluginsDir
			}
			if dest == "" {
				return fmt.Errorf("cannot determine the plugins directory; set XDG_DATA_HOME or use --local")
			}
			name, err := plugin.Add(args[0], dest, force)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Installed plugin %q to %s\n", name, filepath.Join(dest, name))
			return nil
		},
	}
	cmd.Flags().BoolVar(&local, "local", false, "install into the project ./plugins directory instead of the global dir")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing plugin of the same name")
	return cmd
}

func newPluginsListCmd() *cobra.Command {
	// Listing enumerates the search path without parsing any manifest, so it has no
	// conditional sections to select and takes no build/arch flags — unlike `info`,
	// which parses and does.
	var dir string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List available plugins and where each resolves from",
		Long: "List every plugin available across the search path — the project dir\n" +
			"(--dir), PEBUILD_PLUGIN_PATH, the global dir, and the built-ins — with the\n" +
			"source each resolves from. A more specific source shadows a less specific\n" +
			"one of the same name.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			sp, err := plugin.ManagementSearchPath(dir)
			if err != nil {
				return err
			}
			avail := sp.List()
			printAvailable(cmd.OutOrStdout(), avail)
			printLocalNote(cmd.OutOrStdout(), avail)
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "project plugins directory to include (highest precedence)")
	return cmd
}

func newPluginsInfoCmd() *cobra.Command {
	var (
		build int
		arch  string
		dir   string
	)
	cmd := &cobra.Command{
		Use:   "info <name>",
		Short: "Show parsed details for an available plugin",
		Long: "Resolve a plugin by name across the search path and print its descriptor,\n" +
			"the source it resolved from, and a summary of the actions it compiles to.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			sp, err := plugin.ManagementSearchPath(dir)
			if err != nil {
				return err
			}
			fsys, src, canonical, ok := sp.Resolve(name)
			if !ok {
				return fmt.Errorf("plugin %q not found on the search path", name)
			}
			// Parse under the plugin's canonical on-disk name, not the (possibly
			// differently cased) name the user typed.
			p, err := plugin.ParseFS(fsys, canonical, plugin.Target{Build: build, Arch: arch})
			if err != nil {
				return err
			}
			printPluginInfo(cmd.OutOrStdout(), p, src.Name())
			return nil
		},
	}
	cmd.Flags().IntVar(&build, "build", 0, "source build number for .BuildNo sections (e.g. 2600, 3790)")
	cmd.Flags().StringVar(&arch, "arch", "", "source architecture for .<arch> sections (i386, amd64)")
	cmd.Flags().StringVar(&dir, "dir", "", "project plugins directory to include (highest precedence)")
	return cmd
}

// printLocalNote warns, when the local ./plugins directory contributed to a listing,
// that a build only uses those plugins if its manifest's plugins_dir points there — so
// "list showed it" is not read as "the build will use it". A build never resolves
// ./plugins implicitly.
func printLocalNote(w io.Writer, avail []plugin.Available) {
	for _, a := range avail {
		if a.Source.Name() == "local" {
			fmt.Fprintf(w, "\nNote: %s/ is shown for convenience; a build uses these only if its manifest's plugins_dir points here.\n", plugin.LocalPluginsDir)
			return
		}
	}
}

func printAvailable(w io.Writer, avail []plugin.Available) {
	if len(avail) == 0 {
		fmt.Fprintln(w, "No plugins found.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSOURCE")
	for _, a := range avail {
		fmt.Fprintf(tw, "%s\t%s\n", a.Name, a.Source.Name())
	}
	tw.Flush()
}

func printPluginInfo(w io.Writer, p *plugin.Plugin, source string) {
	fmt.Fprintf(w, "Name:    %s\n", p.Name)
	fmt.Fprintf(w, "Source:  %s\n", source)
	fmt.Fprintf(w, "Display: %s\n", p.Display)
	fmt.Fprintf(w, "Enabled: %v\n", p.Enabled)
	if p.Help != "" {
		fmt.Fprintf(w, "Help:    %s\n", p.Help)
	}
	if p.Config != "" {
		fmt.Fprintf(w, "Config:  %s\n", p.Config)
	}
	if p.Update.Present {
		fmt.Fprintf(w, "Update:  id=%s", p.Update.ID)
		if p.Update.Version != "" {
			fmt.Fprintf(w, " version=%s", p.Update.Version)
		}
		if len(p.Update.Depends) > 0 {
			fmt.Fprintf(w, " depends=%v", p.Update.Depends)
		}
		if p.Update.Parent != "" {
			fmt.Fprintf(w, " parent=%s", p.Update.Parent)
		}
		fmt.Fprintln(w)
	}

	fmt.Fprintf(w, "\nActions: %d\n", len(p.Actions))
	counts := map[action.Kind]int{}
	for _, a := range p.Actions {
		counts[a.Kind]++
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	kinds := make([]string, 0, len(counts))
	for k := range counts {
		kinds = append(kinds, string(k))
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		fmt.Fprintf(tw, "  %s\t%d\n", k, counts[action.Kind(k)])
	}
	tw.Flush()
}
