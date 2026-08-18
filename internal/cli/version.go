package cli

import (
	"fmt"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
)

// pseudoVersionRE matches the timestamp-and-hash core of a module pseudo-version
// (e.g. v0.0.0-20260729180844-5da090baf395). Recent Go synthesizes one into
// Main.Version even for a local `go build` of the main module; we treat that as
// "not a real release" and keep the dev sentinel, reserving Main.Version for an
// actual tagged `go install <module>/cmd/dbosctl@vX`.
var pseudoVersionRE = regexp.MustCompile(`[0-9]{14}-[0-9a-f]{12}`)

// version, commit, and date are overwritten at release-build time via
// `-ldflags -X` (see the Versioning & release section of AGENTS.md). A plain
// `go build` leaves them at these defaults and relies on the VCS stamps that
// go build/go install bake into runtime/debug.BuildInfo.
var (
	version = "dev"
	commit  = ""
	date    = ""
)

type buildInfo struct {
	Version   string
	Commit    string
	Date      string
	Dirty     bool
	GoVersion string
	OS        string
	Arch      string
}

// resolveBuildInfo layers three sources in priority order: the -ldflags -X
// values, then the VCS stamps and module version that go build/go install bake
// in, then the "dev" sentinel.
func resolveBuildInfo() buildInfo {
	bi := buildInfo{
		Version:   version,
		Commit:    commit,
		Date:      date,
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}

	// A Nix build of a dirty tree stamps self.dirtyRev, suffixed -dirty.
	if c, found := strings.CutSuffix(bi.Commit, "-dirty"); found {
		bi.Commit = c
		bi.Dirty = true
	}

	info, ok := debug.ReadBuildInfo()
	if !ok {
		return bi
	}
	// `go install <module>/cmd/dbosctl@vX` records a real semver in Main.Version; a
	// local `go build` records "(devel)". Prefer it only when ldflags left the
	// sentinel in place.
	if bi.Version == "dev" {
		if v := info.Main.Version; v != "" && v != "(devel)" && !pseudoVersionRE.MatchString(v) {
			bi.Version = v
		}
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if bi.Commit == "" {
				bi.Commit = s.Value
			}
		case "vcs.time":
			if bi.Date == "" {
				bi.Date = s.Value
			}
		case "vcs.modified":
			if !bi.Dirty {
				bi.Dirty = s.Value == "true"
			}
		}
	}
	return bi
}

// short renders a one-line version such as `v1.2.0 (abc1234, 2026-07-28)` or
// `dev (abc1234, dirty)`, used for `dbosctl --version`.
func (bi buildInfo) short() string {
	var parts []string
	if c := bi.shortCommit(); c != "" {
		parts = append(parts, c)
	}
	if bi.Date != "" {
		parts = append(parts, bi.Date)
	}
	if bi.Dirty {
		parts = append(parts, "dirty")
	}
	if len(parts) == 0 {
		return bi.Version
	}
	return fmt.Sprintf("%s (%s)", bi.Version, strings.Join(parts, ", "))
}

func (bi buildInfo) shortCommit() string {
	if len(bi.Commit) > 7 {
		return bi.Commit[:7]
	}
	return bi.Commit
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version, build, and platform information",
	Long: `Print the dbosctl version, build metadata, and platform.

This is the DBOS Conductor CLI (the 'dbosctl' Go binary), distinct from the
language SDKs' own tooling — the DBOS Python SDK ships a separate 'dbos'
script.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		bi := resolveBuildInfo()
		w := cmd.OutOrStdout()
		fmt.Fprintf(w, "dbosctl (DBOS Conductor CLI) %s\n", bi.Version)
		if bi.Commit != "" {
			dirty := ""
			if bi.Dirty {
				dirty = " (dirty)"
			}
			fmt.Fprintf(w, "  commit    %s%s\n", bi.shortCommit(), dirty)
		}
		if bi.Date != "" {
			fmt.Fprintf(w, "  built     %s\n", bi.Date)
		}
		fmt.Fprintf(w, "  go        %s\n", bi.GoVersion)
		fmt.Fprintf(w, "  platform  %s/%s\n", bi.OS, bi.Arch)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
