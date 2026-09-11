// Command sitrep is a read-only terminal situation report for a Linux box.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/zebadrabbit/sitrep/internal/app"
	"github.com/zebadrabbit/sitrep/internal/config"
	"github.com/zebadrabbit/sitrep/internal/detect"
	"github.com/zebadrabbit/sitrep/internal/doctor"
	"github.com/zebadrabbit/sitrep/internal/module"
	"github.com/zebadrabbit/sitrep/internal/modules"
	"github.com/zebadrabbit/sitrep/internal/theme"
	"github.com/zebadrabbit/sitrep/internal/version"
)

var flags struct {
	demo, once, lite, json bool
	view, size             string
}

func main() {
	if err := root().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "sitrep:", err)
		os.Exit(1)
	}
}

func root() *cobra.Command {
	r := &cobra.Command{
		Use:           "sitrep [module]",
		Short:         "Read-only situation report for this box",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(*cobra.Command, []string) error {
			cfg, _, err := config.Load()
			if err != nil {
				return err
			}
			return theme.Set(cfg.Theme, config.ThemeDir())
		},
		RunE: runTUI,
	}
	// Every subcommand prints through this writer so ANSI is downsampled to
	// what the terminal supports and stripped entirely when piped.
	r.SetOut(colorprofile.NewWriter(os.Stdout, os.Environ()))
	f := r.Flags()
	f.BoolVar(&flags.once, "once", false, "render one frame to stdout and exit")
	f.BoolVar(&flags.lite, "lite", false, "single 80x24 screen, no sidebar")
	f.StringVar(&flags.view, "view", "", "layout: full|lite|dense")
	f.StringVar(&flags.size, "size", "", "frame size for --once, e.g. 100x30 (default: terminal)")
	_ = f.MarkHidden("size")
	r.PersistentFlags().BoolVar(&flags.json, "json", false, "machine-readable output")
	r.PersistentFlags().BoolVar(&flags.demo, "demo", false, "run against bundled fixtures; no host access")

	r.AddCommand(versionCmd(), doctorCmd(), configCmd(), themeCmd(), snapshotCmd(), modulesCmd())
	return r
}

// resolve registers modules and runs Detect once. cfg is already loaded by
// PersistentPreRunE; reload here is cheap and keeps this self-contained.
func resolve() ([]module.Entry, detect.Env) {
	modules.Register(flags.demo)
	env := detect.Detect()
	env.Demo = flags.demo
	cfg, _, _ := config.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return module.Resolve(ctx, env, cfg), env
}

func runTUI(cmd *cobra.Command, args []string) error {
	if len(args) == 1 && !flags.once {
		return runOneShot(cmd, args[0])
	}
	entries, _ := resolve()
	host, _ := os.Hostname()
	if flags.demo {
		host = "example"
	}
	active := ""
	if len(args) == 1 {
		active = args[0]
	}
	cfg, _, _ := config.Load()
	m := app.New(app.Options{Hostname: host, Mode: mode(), Demo: flags.demo, Entries: entries, Active: active, Dense: cfg.DenseModules})
	if flags.once {
		w, h := frameSize()
		r, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
		fmt.Fprintln(cmd.OutOrStdout(), r.(app.Model).Once(w, h))
		return nil
	}
	_, err := tea.NewProgram(m).Run()
	return err
}

// runOneShot is `sitrep <module> [--json]`: collect once, print, exit.
// Modules the target depends on (ports → docker) are collected first.
func runOneShot(cmd *cobra.Command, id string) error {
	entries, _ := resolve()
	m, ok := module.Lookup(id)
	if !ok || m.Interval() == 0 {
		return fmt.Errorf("unknown module %q (see `sitrep modules list`)", id)
	}
	for _, e := range module.OneShotOrder(entries) {
		if e.Module.ID() == id {
			break
		}
		if e.Enabled && e.Module.Interval() > 0 && contains(m.Flags().After, e.Module.ID()) {
			ctx, cancel := context.WithTimeout(context.Background(), module.Timeout(e.Module))
			_, _ = e.Module.Collect(ctx)
			cancel()
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), module.Timeout(m))
	defer cancel()
	d, err := m.Collect(ctx)
	if err != nil {
		return err
	}
	if flags.json {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(d)
	}
	w, _ := frameSize()
	fmt.Fprintln(cmd.OutOrStdout(), m.View(d, w, 0))
	return nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func snapshotCmd() *cobra.Command {
	var out string
	c := &cobra.Command{
		Use:   "snapshot",
		Short: "Dump every enabled module's current data as JSON",
		RunE: func(cmd *cobra.Command, _ []string) error {
			entries, _ := resolve()
			snap := map[string]any{}
			for _, e := range module.OneShotOrder(entries) {
				if !e.Enabled || e.Module.Interval() == 0 {
					continue
				}
				ctx, cancel := context.WithTimeout(context.Background(), module.Timeout(e.Module))
				d, err := e.Module.Collect(ctx)
				cancel()
				if err != nil {
					snap[e.Module.ID()] = map[string]string{"error": err.Error()}
					continue
				}
				snap[e.Module.ID()] = d
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(snap)
		},
	}
	c.Flags().StringVarP(&out, "output", "o", "json", "output format (json)")
	return c
}

func modulesCmd() *cobra.Command {
	c := &cobra.Command{Use: "modules", Short: "List and inspect modules"}
	c.AddCommand(
		&cobra.Command{Use: "list", Short: "id, state, and detect reason for every module", RunE: func(cmd *cobra.Command, _ []string) error {
			entries, _ := resolve()
			if flags.json {
				type row struct {
					ID, State, Reason string
					Enabled           bool
				}
				rows := []row{}
				for _, e := range entries {
					rows = append(rows, row{e.Module.ID(), e.Avail.State.String(), e.Avail.Reason, e.Enabled})
				}
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(rows)
			}
			for _, e := range entries {
				state := "disabled"
				if e.Enabled {
					state = "enabled"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%-10s %-9s %-12s %s\n", e.Module.ID(), state, e.Avail.State, e.Avail.Reason)
			}
			return nil
		}},
		&cobra.Command{Use: "info <id>", Short: "What a module collects, needs, and shells out to", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			modules.Register(flags.demo)
			m, ok := module.Lookup(args[0])
			if !ok {
				ids := []string{}
				for _, x := range module.All() {
					ids = append(ids, x.ID())
				}
				sort.Strings(ids)
				return fmt.Errorf("unknown module %q; have %s", args[0], strings.Join(ids, ", "))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s — %s\n\n%s\n", m.ID(), m.Title(), m.Info())
			return nil
		}},
		&cobra.Command{Use: "enable <id>", Short: "Enable a module; acknowledges appliance_sensitive once", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			return setEnabled(cmd, args[0], true)
		}},
		&cobra.Command{Use: "disable <id>", Short: "Disable a module", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			return setEnabled(cmd, args[0], false)
		}},
	)
	return c
}

// setEnabled edits config: disabled list, and on an appliance the one-time
// ack for appliance_sensitive modules (HANDOFF §1: warn once, then trust).
func setEnabled(cmd *cobra.Command, id string, on bool) error {
	modules.Register(false)
	m, ok := module.Lookup(id)
	if !ok {
		return fmt.Errorf("unknown module %q", id)
	}
	cfg, _, err := config.Load()
	if err != nil {
		return err
	}
	env := detect.Detect()
	if on && m.Flags().ApplianceSensitive && env.Appliance != "" && !cfg.Acked(id) {
		fmt.Fprintf(cmd.OutOrStdout(), "%s looks like a %s appliance. %s reads its shares/exports directly; the\nappliance UI is the source of truth. Enable anyway? [y/N] ", id, env.Appliance, m.Title())
		line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
		if l := strings.ToLower(strings.TrimSpace(line)); l != "y" && l != "yes" {
			return fmt.Errorf("not enabled")
		}
		cfg.Acks = append(cfg.Acks, id)
	}
	cfg.Disabled = slices.DeleteFunc(cfg.Disabled, func(s string) bool { return s == id })
	if !on {
		cfg.Disabled = append(cfg.Disabled, id)
	}
	if err := os.MkdirAll(config.Dir(), 0o755); err != nil {
		return err
	}
	if err := cfg.Save(config.Path()); err != nil {
		return err
	}
	state := "disabled"
	if on {
		state = "enabled"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s %s (%s)\n", id, state, config.Path())
	return nil
}

func mode() app.Mode {
	switch {
	case flags.view == "dense":
		return app.Dense
	case flags.lite || flags.view == "lite":
		return app.Lite
	}
	return app.Full
}

// frameSize honors --size, then the terminal, then 100x30.
func frameSize() (int, int) {
	var w, h int
	if n, _ := fmt.Sscanf(flags.size, "%dx%d", &w, &h); n == 2 {
		return w, h
	}
	if w, h, err := term.GetSize(os.Stdout.Fd()); err == nil {
		return w, h
	}
	return 100, 30
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version, commit, go version, build date",
		Run: func(cmd *cobra.Command, _ []string) {
			if flags.json {
				fmt.Fprintf(cmd.OutOrStdout(), "{\"version\":%q,\"commit\":%q,\"go\":%q,\"date\":%q}\n",
					version.Version, version.Commit, version.GoVersion(), version.Date)
				return
			}
			fmt.Fprintf(cmd.OutOrStdout(), "sitrep %s (%s) %s built %s\n",
				version.Version, version.Commit, version.GoVersion(), version.Date)
		},
	}
}

func doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check environment, privileges, config and modules; exit 1 on any ○",
		RunE: func(cmd *cobra.Command, _ []string) error {
			entries, env := resolve()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			rep := doctor.Run(ctx, env, entries)
			if err := rep.Write(cmd.OutOrStdout(), flags.json); err != nil {
				return err
			}
			if rep.Failed() {
				os.Exit(1)
			}
			return nil
		},
	}
}

func configCmd() *cobra.Command {
	c := &cobra.Command{Use: "config", Short: "Show or create the config file"}
	c.AddCommand(
		&cobra.Command{Use: "path", Short: "Print the config file path", Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintln(cmd.OutOrStdout(), config.Path())
		}},
		&cobra.Command{Use: "show", Short: "Print the effective config", RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, _, err := config.Load()
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), cfg.String())
			return nil
		}},
		&cobra.Command{Use: "init", Short: "Write a default config file", RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := config.Init()
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "wrote", p)
			return nil
		}},
		&cobra.Command{Use: "edit", Short: "Open the config in $EDITOR (creates it first if absent)", RunE: func(*cobra.Command, []string) error {
			if _, err := os.Stat(config.Path()); err != nil {
				if _, err := config.Init(); err != nil {
					return err
				}
			}
			ed := os.Getenv("VISUAL")
			if ed == "" {
				ed = os.Getenv("EDITOR")
			}
			if ed == "" {
				ed = "vi"
			}
			c := exec.Command(ed, config.Path()) // the one exec outside collect.Run: it is the user's editor, on the user's file
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
			return c.Run()
		}},
	)
	return c
}

func themeCmd() *cobra.Command {
	c := &cobra.Command{Use: "theme", Short: "List or inspect themes"}
	c.AddCommand(
		&cobra.Command{Use: "list", Short: "List available themes", Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintln(cmd.OutOrStdout(), strings.Join(theme.List(config.ThemeDir()), "\n"))
		}},
		&cobra.Command{Use: "show", Short: "Print the active theme name", Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintln(cmd.OutOrStdout(), theme.Current().Name)
		}},
		&cobra.Command{Use: "set <name>", Short: "Select a theme in config", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := theme.Build(args[0], config.ThemeDir()); err != nil {
				return err
			}
			cfg, _, err := config.Load()
			if err != nil {
				return err
			}
			cfg.Theme = args[0]
			if err := os.MkdirAll(config.Dir(), 0o755); err != nil {
				return err
			}
			if err := cfg.Save(config.Path()); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "theme %s (%s)\n", args[0], config.Path())
			return nil
		}},
	)
	return c
}
