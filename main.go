package main

import (
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

func reorderArgs(fs *flag.FlagSet, args []string) []string {
	var flags, positionals []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			name := strings.TrimPrefix(a, "--")
			name = strings.TrimPrefix(name, "-")
			hasEq := strings.Contains(name, "=")
			fname := name
			if hasEq {
				fname = strings.SplitN(name, "=", 2)[0]
			}
			flags = append(flags, a)
			if !hasEq {
				if fl := fs.Lookup(fname); fl != nil {
					if _, ok := fl.Value.(interface{ IsBoolFlag() bool }); !ok {
						if i+1 < len(args) {
							flags = append(flags, args[i+1])
							i++
						}
					}
				}
			}
			continue
		}
		positionals = append(positionals, a)
	}
	return append(flags, positionals...)
}

var buildVersion = "dev"

const usage = `binup - install prebuilt binaries from GitHub and check for updates

Usage:
  binup install [OPTIONS] TARGET
  binup upgrade [REPO | all]
  binup list
  binup outdated
  binup remove [REPO]
  binup rate
  binup version

TARGET is one of:
  owner/repo        a GitHub repository (latest release asset is detected)
  FILE              a local archive to extract

Install options:
  --tag=TAG          use the given release tag
  --to=DIR           output directory (default $BINUP_BIN or ~/.local/bin)
  --system=OS/ARCH   target system (use "all" for all)
  --file=GLOB        glob for file extraction (default: executables only; use * for all)
  --asset=STR        filter assets by substring (^ for anti-match, repeatable)
  --all              extract all candidate assets
  --download-only    stop after download (no extraction)
  --upgrade-only     only download if release is newer than installed
  --source           download source tarball instead of release asset
  --pre-release      consider pre-releases when picking latest
  --sha256           print sha256 of downloaded asset
  --verify-sha256=H  verify download matches hex hash H
  --yes, -y          skip the install preview confirmation prompt
  --min-release-age=DUR  ignore releases newer than DUR (e.g. 30m, 3d, 72h);
                          like npm's minimum-release-age, this guards against
                          just-published/compromised releases

Before writing any files, install/upgrade prints the target files (marked
new or overwrite), the asset's checksum, and whether that checksum was
verified against a published checksum file or --verify-sha256, then asks
for confirmation (y/N). Pass --yes to skip the prompt, e.g. for scripts/CI.

Update checking:
  binup keeps a manifest at $XDG_DATA_HOME/binup/manifest.json (default
  ~/.local/share/binup/manifest.json; override with $BINUP_MANIFEST).
  'binup outdated' compares installed tags with the latest GitHub release
  for each tracked repo. 'binup upgrade <repo>' re-installs when a newer
  release is available; 'binup upgrade all' upgrades everything.

Environment:
  BINUP_GITHUB_TOKEN / GITHUB_TOKEN  GitHub auth token
  BINUP_BIN                          default install dir
  BINUP_MANIFEST / XDG_DATA_HOME     manifest path
`

type cliFlags struct {
	cmd          string
	tag          string
	to           string
	system       string
	fileGlob     string
	assets       []string
	all          bool
	downloadOnly bool
	upgradeOnly  bool
	source       bool
	preRelease   bool
	showHash     bool
	verifySHA256 string
	yes          bool
	minAge       string
}

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(1)
	}
	cmd := os.Args[1]
	rest := os.Args[2:]

	switch cmd {
	case "version", "--version":
		fmt.Println("binup", buildVersion)
		return
	case "help", "-h", "--help":
		fmt.Print(usage)
		return
	}

	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	cf := cliFlags{fileGlob: "*"}
	fs.StringVar(&cf.tag, "tag", "", "release tag")
	fs.StringVar(&cf.to, "to", "", "output directory")
	fs.StringVar(&cf.system, "system", "", "target system")
	fs.StringVar(&cf.fileGlob, "file", "*", "glob for extraction")
	fs.Func("asset", "asset substring filter (^anti, repeatable)", func(v string) error {
		cf.assets = append(cf.assets, v)
		return nil
	})
	fs.BoolVar(&cf.all, "all", false, "extract all candidate assets")
	fs.BoolVar(&cf.downloadOnly, "download-only", false, "stop after download")
	fs.BoolVar(&cf.upgradeOnly, "upgrade-only", false, "only download if newer")
	fs.BoolVar(&cf.source, "source", false, "download source tarball")
	fs.BoolVar(&cf.preRelease, "pre-release", false, "include pre-releases")
	fs.BoolVar(&cf.showHash, "sha256", false, "print sha256")
	fs.StringVar(&cf.verifySHA256, "verify-sha256", "", "verify sha256")
	fs.BoolVar(&cf.yes, "yes", false, "skip confirmation prompt")
	fs.BoolVar(&cf.yes, "y", false, "skip confirmation prompt (shorthand)")
	fs.StringVar(&cf.minAge, "min-release-age", "", "ignore releases newer than this (e.g. 30m, 3d, 72h)")
	rest = reorderArgs(fs, rest)
	if err := fs.Parse(rest); err != nil {
		os.Exit(2)
	}
	cf.cmd = cmd
	args := fs.Args()

	if err := run(cf, args); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(cf cliFlags, args []string) error {
	minAge, err := parseMinAge(cf.minAge)
	if err != nil {
		return err
	}
	switch cf.cmd {
	case "install":
		if len(args) == 0 {
			return fmt.Errorf("install requires a TARGET (owner/repo or FILE)")
		}
		t := args[0]
		opts := installOptions{
			Tag: cf.tag, To: cf.to, System: cf.system, FileGlob: cf.fileGlob,
			AssetFilters: cf.assets, All: cf.all, DownloadOnly: cf.downloadOnly,
			UpgradeOnly: cf.upgradeOnly, Source: cf.source, PreRelease: cf.preRelease,
			ShowHash: cf.showHash, VerifySHA256: cf.verifySHA256,
			Yes: cf.yes, MinAge: minAge,
		}
		if isGitHubRepoURL(t) {
			opts.Repo = stripGitHubURL(t)
		} else if _, err := os.Stat(t); err == nil && (!strings.Contains(t, "/") || strings.Contains(t, "\\")) {
			opts.File = t
		} else {
			opts.Repo = t
		}
		_, err := runInstall(opts)
		if errors.Is(err, errAborted) {
			fmt.Println("aborted")
			return nil
		}
		return err
	case "upgrade":
		return runUpgrade(cf, args, minAge)
	case "list":
		return runList()
	case "outdated":
		return runOutdated(minAge)
	case "remove":
		return runRemove(args)
	case "rate":
		gh := newGitHubClient()
		s, err := gh.rateLimit()
		if err != nil {
			return err
		}
		fmt.Println(s)
		return nil
	default:
		return fmt.Errorf("unknown command %q (see 'binup help')", cf.cmd)
	}
}

// parseMinAge parses --min-release-age. A bare integer is minutes (matching
// npm's minimum-release-age); a value with a unit suffix (m, h, d, ...) is a
// duration, e.g. "30m", "72h", "3d".
func parseMinAge(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	if n, err := strconv.Atoi(s); err == nil {
		return time.Duration(n) * time.Minute, nil
	}
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.ParseFloat(days, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid --min-release-age %q: %w", s, err)
		}
		return time.Duration(n * float64(24*time.Hour)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid --min-release-age %q: %w", s, err)
	}
	return d, nil
}

func isGitHubRepoURL(t string) bool {
	u, err := url.Parse(t)
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Host)
	if h != "github.com" && !strings.HasSuffix(h, ".github.com") {
		return false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) >= 2 && (parts[1] == "releases" || parts[1] == "archive") {
		return false
	}
	return len(parts) >= 2 && parts[0] != "" && parts[1] != ""
}

func stripGitHubURL(t string) string {
	u, err := url.Parse(t)
	if err != nil {
		return t
	}
	h := strings.ToLower(u.Host)
	if h != "github.com" && !strings.HasSuffix(h, ".github.com") {
		return t
	}
	path := strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git")
	parts := strings.SplitN(path, "/", 3)
	if len(parts) >= 2 {
		return parts[0] + "/" + parts[1]
	}
	return t
}

func runUpgrade(cf cliFlags, args []string, minAge time.Duration) error {
	m, err := loadManifest()
	if err != nil {
		return err
	}
	target := ""
	if len(args) > 0 {
		target = args[0]
	}
	ids := m.keys()
	if target != "" && target != "all" {
		ids = []string{target}
	}
	if len(ids) == 0 {
		fmt.Println("nothing installed")
		return nil
	}
	gh := newGitHubClient()
	for _, id := range ids {
		rec, ok := m.Tools[id]
		if !ok {
			return fmt.Errorf("not tracked: %s", id)
		}
		if rec.Source != "github" {
			fmt.Printf("skip %s (source=%s, cannot check for updates)\n", id, rec.Source)
			continue
		}
		owner, repo, err := splitRepo(id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skip %s: %v\n", id, err)
			continue
		}
		rel, err := gh.latestRelease(owner, repo, false, minAge)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skip %s: %v\n", id, err)
			continue
		}
		if compareVersions(rel.TagName, rec.Tag) <= 0 {
			fmt.Printf("%s up-to-date (%s)\n", id, rec.Tag)
			continue
		}
		fmt.Printf("upgrading %s: %s -> %s\n", id, rec.Tag, rel.TagName)
		opts := installOptions{
			Repo: id, To: rec.Target, FileGlob: rec.FileGlob,
			AssetFilters: rec.Filters,
			PreRelease:   cf.preRelease, Yes: cf.yes, MinAge: minAge,
		}
		if _, err := runInstall(opts); err != nil {
			if errors.Is(err, errAborted) {
				fmt.Printf("skipped %s\n", id)
				continue
			}
			fmt.Fprintf(os.Stderr, "failed %s: %v\n", id, err)
		}
	}
	return nil
}

func runList() error {
	m, err := loadManifest()
	if err != nil {
		return err
	}
	if len(m.Tools) == 0 {
		fmt.Println("(no tools tracked; install something with 'binup install owner/repo')")
		return nil
	}
	for _, id := range m.keys() {
		rec := m.Tools[id]
		fmt.Printf("%-30s %s  %s\n", id, rec.Tag, rec.Asset)
	}
	return nil
}

func runOutdated(minAge time.Duration) error {
	m, err := loadManifest()
	if err != nil {
		return err
	}
	if len(m.Tools) == 0 {
		fmt.Println("(no tools tracked)")
		return nil
	}
	gh := newGitHubClient()
	any := false
	for _, id := range m.keys() {
		rec := m.Tools[id]
		if rec.Source != "github" {
			fmt.Printf("%-30s (source=%s, cannot check)\n", id, rec.Source)
			continue
		}
		owner, repo, err := splitRepo(id)
		if err != nil {
			continue
		}
		rel, err := gh.latestRelease(owner, repo, false, minAge)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", id, err)
			continue
		}
		if compareVersions(rel.TagName, rec.Tag) > 0 {
			any = true
			fmt.Printf("%-30s %s -> %s  (update available)\n", id, rec.Tag, rel.TagName)
		} else {
			fmt.Printf("%-30s %s  (up-to-date)\n", id, rel.TagName)
		}
	}
	if !any {
		fmt.Println("all tracked tools up-to-date")
	}
	return nil
}

func runRemove(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("remove requires a REPO")
	}
	id := args[0]
	m, err := loadManifest()
	if err != nil {
		return err
	}
	rec, ok := m.Tools[id]
	if !ok {
		return fmt.Errorf("not tracked: %s", id)
	}
	for _, f := range rec.Files {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "warn: remove %s: %v\n", f, err)
		} else {
			fmt.Println("removed", f)
		}
	}
	delete(m.Tools, id)
	if err := m.save(); err != nil {
		return err
	}
	fmt.Printf("untracked %s\n", id)
	return nil
}
