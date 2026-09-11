// Command sitrep is a read-only terminal situation report for a Linux box.
package main

import (
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/zebadrabbit/sitrep/internal/app"
	"github.com/zebadrabbit/sitrep/internal/config"
	"github.com/zebadrabbit/sitrep/internal/doctor"
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
	f.BoolVar(&flags.demo, "demo", false, "run against bundled fixtures; no host access")
	f.BoolVar(&flags.once, "once", false, "render one frame to stdout and exit")
	f.BoolVar(&flags.lite, "lite", false, "single 80x24 screen, no sidebar")
	f.StringVar(&flags.view, "view", "", "layout: full|lite|dense")
	f.StringVar(&flags.size, "size", "", "frame size for --once, e.g. 100x30 (default: terminal)")
	_ = f.MarkHidden("size")
	r.PersistentFlags().BoolVar(&flags.json, "json", false, "machine-readable output")

	r.AddCommand(versionCmd(), doctorCmd(), configCmd(), themeCmd())
	return r
}

func runTUI(cmd *cobra.Command, args []string) error {
	host, _ := os.Hostname()
	m := app.New(app.Options{Hostname: host, Mode: mode(), Demo: flags.demo})
	if flags.once {
		w, h := frameSize()
		r, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
		fmt.Fprintln(cmd.OutOrStdout(), r.(app.Model).Render(w, h))
		return nil
	}
	_, err := tea.NewProgram(m).Run()
	return err
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
			rep := doctor.Run()
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
		&cobra.Command{Use: "edit", Short: "Open the config in $EDITOR", RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("not yet: edit %s by hand", config.Path())
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
	)
	return c
}
