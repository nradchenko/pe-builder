// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"io"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/nradchenko/pe-builder/action"
	"github.com/nradchenko/pe-builder/plugin"
)

func newPluginsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugins",
		Short: "Discover and inspect plugins",
	}
	cmd.AddCommand(newPluginsListCmd(), newPluginsInfoCmd())
	return cmd
}

func newPluginsListCmd() *cobra.Command {
	var build int
	cmd := &cobra.Command{
		Use:   "list <dir>",
		Short: "List discovered plugins",
		Long: "List the plugins discovered in a drop-in directory: their name, default\n" +
			"enabled state, action count, and vendor id.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			plugins, err := plugin.Discover(args[0], build)
			// Discover returns partial results alongside a parse-error summary;
			// print what parsed, then surface the errors.
			printPluginList(cmd.OutOrStdout(), plugins)
			return err
		},
	}
	cmd.Flags().IntVar(&build, "build", 0, "source build number for .BuildNo sections (e.g. 2600, 3790)")
	return cmd
}

func newPluginsInfoCmd() *cobra.Command {
	var build int
	cmd := &cobra.Command{
		Use:   "info <plugin.inf>",
		Short: "Show parsed details for a plugin",
		Long: "Parse a single plugin .inf file and print its descriptor and a summary of\n" +
			"the actions it compiles to.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := plugin.ParseFile(args[0], build)
			if err != nil {
				return err
			}
			printPluginInfo(cmd.OutOrStdout(), p)
			return nil
		},
	}
	cmd.Flags().IntVar(&build, "build", 0, "source build number for .BuildNo sections (e.g. 2600, 3790)")
	return cmd
}

func printPluginList(w io.Writer, plugins []*plugin.Plugin) {
	// Nothing parsed (e.g. an unreadable directory): print no table header, so a
	// hard failure surfaces as just its error rather than an empty table.
	if len(plugins) == 0 {
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tENABLED\tACTIONS\tID")
	for _, p := range plugins {
		fmt.Fprintf(tw, "%s\t%v\t%d\t%s\n", p.Name, p.Enabled, len(p.Actions), p.Update.ID)
	}
	tw.Flush()
}

func printPluginInfo(w io.Writer, p *plugin.Plugin) {
	fmt.Fprintf(w, "Name:    %s\n", p.Name)
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
