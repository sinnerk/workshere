// Command workshere explains why code that passes on this machine can still
// fail in CI. It is read-only: it never edits a file, never runs a build, and
// never touches the network.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/sinnerk/workshere/internal/checks"
	"github.com/sinnerk/workshere/internal/gitx"
	"github.com/sinnerk/workshere/internal/report"
	"github.com/sinnerk/workshere/internal/scan"
)

const toolName = "workshere"

// version is stamped by the release build.
var version = "0.1.0"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet(toolName, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = usage(fs)

	asJSON := fs.Bool("json", false, "emit a machine-readable report")
	noColor := fs.Bool("no-color", false, "disable ANSI colour")
	minFlag := fs.String("min-severity", "low", "exit 1 only at or above this level: low, medium, high")
	root := fs.String("root", ".", "directory inside the repository to inspect")
	list := fs.Bool("list-checks", false, "list the checks and exit")
	showVersion := fs.Bool("version", false, "print the version and exit")
	redact := fs.Bool("redact", false, "replace the home directory in output with %USERPROFILE%")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *showVersion {
		fmt.Printf("%s %s\n", toolName, version)
		return 0
	}
	if *list {
		for _, c := range checks.All() {
			fmt.Printf("%-24s %s\n", c.Name, c.Description)
		}
		return 0
	}

	min, ok := report.ParseSeverity(*minFlag)
	if !ok {
		fmt.Fprintf(os.Stderr, "%s: unknown severity %q (use low, medium or high)\n", toolName, *minFlag)
		return 2
	}

	ctx, err := scan.Load(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", toolName, err)
		if errors.Is(err, gitx.ErrNotARepo) {
			fmt.Fprintln(os.Stderr, "run inside a git repository, or point --root at one")
		}
		return 2
	}

	r := report.Build(toolName, version, ctx.Root, checks.Run(ctx), ctx.Warnings)
	if *redact {
		r = redactReport(r)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(r); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", toolName, err)
			return 2
		}
	} else {
		report.Text(os.Stdout, r, min, colorEnabled(*noColor))
	}

	if r.Total(min) > 0 {
		return 1
	}
	return 0
}

func usage(fs *flag.FlagSet) func() {
	return func() {
		out := fs.Output()
		fmt.Fprintf(out, "%s %s — why it works here and fails there\n\n", toolName, version)
		fmt.Fprintf(out, "Usage:\n  %s [flags]\n\n", toolName)
		fmt.Fprintf(out, "Run inside a git repository. Exit codes: 0 clean, 1 findings, 2 usage.\n\nFlags:\n")
		fs.PrintDefaults()
	}
}

// redactReport removes the local home directory from everything a user might
// paste into an issue.
func redactReport(r report.Report) report.Report {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return r
	}

	replacements := [][2]string{
		{strings.ReplaceAll(home, `\`, "/"), "%USERPROFILE%"},
		{home, "%USERPROFILE%"},
	}
	for _, pair := range replacements {
		r.Root = strings.ReplaceAll(r.Root, pair[0], pair[1])
		for i := range r.Findings {
			f := &r.Findings[i]
			f.File = strings.ReplaceAll(f.File, pair[0], pair[1])
			f.Detail = strings.ReplaceAll(f.Detail, pair[0], pair[1])
			f.Evidence = strings.ReplaceAll(f.Evidence, pair[0], pair[1])
			f.Fix = strings.ReplaceAll(f.Fix, pair[0], pair[1])
		}
		for i := range r.Warnings {
			r.Warnings[i] = strings.ReplaceAll(r.Warnings[i], pair[0], pair[1])
		}
	}
	return r
}

func colorEnabled(noColor bool) bool {
	if noColor || os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	info, err := os.Stdout.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	if runtime.GOOS == "windows" {
		// PowerShell 5.1 in conhost does not process ANSI escapes; modern
		// terminals set one of these.
		return os.Getenv("WT_SESSION") != "" ||
			os.Getenv("TERM") != "" ||
			os.Getenv("ConEmuANSI") == "ON"
	}
	return true
}
