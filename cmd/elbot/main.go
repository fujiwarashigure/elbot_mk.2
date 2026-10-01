package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"elbot/internal/app"
	"elbot/internal/launcher"
)

var version = "dev"

func main() {
	startedAt := time.Now()
	opts, err := launcher.ParseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "elbot: %v\n\n", err)
		launcher.WriteUsage(os.Stderr)
		os.Exit(2)
	}
	if opts.Help {
		launcher.WriteUsage(os.Stdout)
		return
	}
	if opts.Version {
		fmt.Fprintf(os.Stdout, "elbot %s\n", version)
		return
	}
	if opts.Command == launcher.CommandConfigCheck {
		summary, err := app.CheckConfig(opts.ConfigPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "elbot: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprint(os.Stdout, summary)
		return
	}
	if opts.Command == launcher.CommandDoctor {
		report, err := app.RunDoctor(context.Background(), app.DoctorOptions{
			ConfigPath: opts.ConfigPath,
			E2E:        opts.DoctorE2E,
			JSON:       opts.DoctorJSON,
			SkipModel:  opts.DoctorNoModel,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "elbot doctor: %v\n", err)
			os.Exit(1)
		}
		if opts.DoctorJSON {
			encoder := json.NewEncoder(os.Stdout)
			encoder.SetIndent("", "  ")
			if err := encoder.Encode(report); err != nil {
				fmt.Fprintf(os.Stderr, "elbot doctor: %v\n", err)
				os.Exit(1)
			}
		} else {
			for _, check := range report.Checks {
				line := fmt.Sprintf("[%s] %-16s %-7s %s", check.Category, check.Name, check.Status, check.Detail)
				if check.Error != "" {
					line += " error=" + check.Error
				}
				fmt.Fprintln(os.Stdout, line)
			}
			fmt.Fprintf(os.Stdout, "config_ok=%v e2e_ok=%v\n", report.ConfigOK, report.E2EOK)
		}
		if !report.ConfigOK || (opts.DoctorE2E && !report.E2EOK) {
			os.Exit(1)
		}
		return
	}

	if opts.Command == launcher.CommandCompletion {
		if err := launcher.WriteCompletion(os.Stdout, opts.Completion); err != nil {
			fmt.Fprintf(os.Stderr, "elbot: %v\n", err)
			os.Exit(2)
		}
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if opts.Mode == app.RunModeCLIOnly {
		if err := app.RunCLIClient(ctx, app.CLIClientOptions{ConfigPath: opts.ConfigPath, ClientName: opts.ClientName}); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			fmt.Fprintf(os.Stderr, "elbot: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if opts.Mode == app.RunModeAuto {
		if err := app.TryRunCLIClient(ctx, app.CLIClientOptions{ConfigPath: opts.ConfigPath, ClientName: opts.ClientName}); err == nil {
			return
		} else if !errors.Is(err, app.ErrCLIClientFallback) {
			if errors.Is(err, context.Canceled) {
				return
			}
			fmt.Fprintf(os.Stderr, "elbot: %v\n", err)
			os.Exit(1)
		}
	}

	if err := app.Run(ctx, app.Options{
		ConfigPath: opts.ConfigPath,
		Version:    version,
		StartedAt:  startedAt,
		Mode:       opts.Mode,
	}); err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		fmt.Fprintf(os.Stderr, "elbot: %v\n", err)
		os.Exit(1)
	}
}
