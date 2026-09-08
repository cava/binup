package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type installOptions struct {
	Repo         string
	File         string
	Tag          string
	To           string
	System       string
	FileGlob     string
	AssetFilters []string
	DownloadOnly bool
	UpgradeOnly  bool
	ShowHash     bool
	VerifySHA256 string
	Source       bool
	PreRelease   bool
	All          bool
	Yes          bool
	MinAge       time.Duration
}

// errAborted signals the user declined an install confirmation prompt; it is
// not a failure and callers should treat it as a clean no-op.
var errAborted = errors.New("aborted")

// confirmPlan prints what an install/extraction would do (target files,
// marked new or overwrite, and the asset's checksum, noting whether it was
// verified against a published checksum) and asks the user to confirm
// before anything is written to disk. It returns errAborted if the user
// declines.
func confirmPlan(label, sha, checksumStatus string, files []string, opts installOptions) error {
	if opts.Yes {
		return nil
	}
	if len(files) == 0 {
		return nil
	}
	fmt.Printf("\nabout to install %s\n\n", label)
	if sha != "" {
		fmt.Printf("  %-9s %s\n", "sha256", sha)
		fmt.Printf("  %-9s %s\n", "checksum", checksumStatus)
		fmt.Println()
	}
	for _, f := range files {
		status := "new"
		if _, err := os.Stat(f); err == nil {
			status = "overwrite"
		}
		fmt.Printf("  %-9s %s\n", status, f)
	}
	fmt.Println()
	if !stdinIsTerminal() {
		return fmt.Errorf("refusing to write files without confirmation in a non-interactive session; pass --yes to proceed")
	}
	fmt.Print("Proceed? [y/N] ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if strings.ToLower(strings.TrimSpace(line)) != "y" {
		return errAborted
	}
	return nil
}

func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

type installResult struct {
	Source string
	Repo   string
	Tag    string
	Asset  string
	Files  []string
	SHA    string
}

func parseSystem(s string) systemInfo {
	if s == "" || s == "all" {
		return systemInfo{os: "all", arch: "all"}
	}
	os_, arch, _ := strings.Cut(s, "/")
	return systemInfo{os: os_, arch: arch}
}

func resolveTargetDir(opts installOptions) string {
	dir := opts.To
	if dir == "" {
		if v := os.Getenv("BINUP_BIN"); v != "" {
			dir = v
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				dir = ".local/bin"
			} else {
				dir = filepath.Join(home, ".local", "bin")
			}
		}
	}
	if strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			dir = filepath.Join(home, dir[2:])
		}
	} else if dir == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			dir = home
		}
	}
	return os.ExpandEnv(dir)
}

func runInstall(opts installOptions) (*installResult, error) {
	out := &installResult{}
	gh := newGitHubClient()
	switch {
	case opts.File != "":
		out.Source = "file"
		return installFromFile(opts, out)
	case opts.Repo != "":
		out.Source = "github"
		return installFromRepo(opts, gh, out)
	default:
		return nil, fmt.Errorf("no target specified")
	}
}

func installFromRepo(opts installOptions, gh *GitHubClient, out *installResult) (*installResult, error) {
	owner, repo, err := splitRepo(opts.Repo)
	if err != nil {
		return nil, err
	}
	out.Repo = owner + "/" + repo
	var rel *Release
	if opts.Tag != "" {
		rel, err = gh.releaseByTag(owner, repo, opts.Tag)
	} else {
		rel, err = gh.latestRelease(owner, repo, opts.PreRelease, opts.MinAge)
	}
	if err != nil {
		return nil, err
	}
	out.Tag = rel.TagName
	sys := parseSystem(opts.System)
	if sys.os == "all" && sys.arch == "all" {
		sys = currentSystem()
	}
	if opts.UpgradeOnly {
		m, _ := loadManifest()
		if rec, ok := m.Tools[out.Repo]; ok {
			if compareVersions(rel.TagName, rec.Tag) <= 0 {
				return nil, fmt.Errorf("already up-to-date (%s)", rec.Tag)
			}
		}
	}
	var asset Asset
	if opts.Source {
		asset = Asset{Name: repo + "-source.tar.gz", BrowserDownloadURL: rel.TarballURL}
		if rel.TarballURL == "" {
			return nil, fmt.Errorf("no source tarball for release")
		}
	} else {
		var ok bool
		asset, ok = detectAsset(rel.Assets, sys, opts.AssetFilters)
		if !ok {
			if opts.All {
				return installAllAssets(rel, opts, out, gh)
			}
			return nil, fmt.Errorf("no suitable asset found for %s/%s on %s_%s", owner, repo, sys.os, sys.arch)
		}
	}
	out.Asset = asset.Name
	tmpPath, sha, md5sum, err := download(asset.BrowserDownloadURL, gh.token)
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpPath)
	out.SHA = sha
	checksumStatus := "unverified, no checksum published for this asset"
	if chk, found := checksumAssetFor(rel.Assets, asset.Name); found {
		fmt.Printf("verifying checksum via %s\n", chk.Name)
		if err := verifySiblingChecksum(chk, asset.Name, sha, md5sum, gh); err != nil {
			return nil, err
		}
		checksumStatus = "verified via " + chk.Name
	}
	if opts.VerifySHA256 != "" {
		if !strings.EqualFold(strings.TrimSpace(opts.VerifySHA256), sha) {
			return nil, fmt.Errorf("sha256 mismatch: expected %s got %s", opts.VerifySHA256, sha)
		}
		checksumStatus = "verified via --verify-sha256"
	}
	if opts.ShowHash {
		fmt.Println("sha256:", sha)
	}
	toDir := resolveTargetDir(opts)
	if err := os.MkdirAll(toDir, 0755); err != nil {
		return nil, err
	}
	if opts.DownloadOnly {
		final := filepath.Join(toDir, asset.Name)
		if err := confirmPlan(out.Asset, out.SHA, checksumStatus, []string{final}, opts); err != nil {
			return nil, err
		}
		if err := copyFile(tmpPath, final); err != nil {
			return nil, err
		}
		os.Chmod(final, 0755)
		out.Files = []string{final}
	} else {
		planned, err := extractArchive(tmpPath, asset.Name, toDir, opts.FileGlob, true)
		if err != nil {
			return nil, err
		}
		if len(planned) == 0 {
			return nil, fmt.Errorf("no files extracted matching %q", opts.FileGlob)
		}
		if err := confirmPlan(out.Asset, out.SHA, checksumStatus, planned, opts); err != nil {
			return nil, err
		}
		files, err := extractArchive(tmpPath, asset.Name, toDir, opts.FileGlob, false)
		if err != nil {
			return nil, err
		}
		out.Files = files
		for _, f := range files {
			os.Chmod(f, 0755)
		}
	}
	m, _ := loadManifest()
	m.Tools[out.Repo] = &ToolRecord{
		Repo: out.Repo, Source: "github", Tag: out.Tag,
		Asset: out.Asset, Files: out.Files, Target: toDir,
		FileGlob: opts.FileGlob, Filters: opts.AssetFilters,
		SHA256: out.SHA, InstalledAt: time.Now().UTC(),
	}
	m.save()
	fmt.Printf("installed %s@%s -> %s\n", out.Repo, out.Tag, strings.Join(out.Files, ", "))
	return out, nil
}

func installAllAssets(rel *Release, opts installOptions, out *installResult, gh *GitHubClient) (*installResult, error) {
	sys := parseSystem(opts.System)
	if sys.os == "all" && sys.arch == "all" {
		sys = currentSystem()
	}
	toDir := resolveTargetDir(opts)
	if err := os.MkdirAll(toDir, 0755); err != nil {
		return nil, err
	}
	for _, a := range rel.Assets {
		if isSkippedAsset(strings.ToLower(a.Name)) {
			continue
		}
		if !osMatches(a.Name, sys.os) || !archMatches(a.Name, sys.arch) {
			continue
		}
		tmp, sha, md5sum, err := download(a.BrowserDownloadURL, gh.token)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warn: %s: %v\n", a.Name, err)
			continue
		}
		checksumStatus := "unverified, no checksum published for this asset"
		if chk, found := checksumAssetFor(rel.Assets, a.Name); found {
			if err := verifySiblingChecksum(chk, a.Name, sha, md5sum, gh); err != nil {
				os.Remove(tmp)
				fmt.Fprintf(os.Stderr, "warn: checksum %s: %v\n", a.Name, err)
				continue
			}
			checksumStatus = "verified via " + chk.Name
		}
		planned, err := extractArchive(tmp, a.Name, toDir, opts.FileGlob, true)
		if err != nil {
			os.Remove(tmp)
			fmt.Fprintf(os.Stderr, "warn: extract %s: %v\n", a.Name, err)
			continue
		}
		if err := confirmPlan(a.Name, sha, checksumStatus, planned, opts); err != nil {
			os.Remove(tmp)
			if errors.Is(err, errAborted) {
				fmt.Printf("skipped %s\n", a.Name)
				continue
			}
			return out, err
		}
		files, err := extractArchive(tmp, a.Name, toDir, opts.FileGlob, false)
		os.Remove(tmp)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warn: extract %s: %v\n", a.Name, err)
			continue
		}
		out.Files = append(out.Files, files...)
	}
	fmt.Printf("installed %d asset(s) -> %s\n", len(out.Files), toDir)
	return out, nil
}

func installFromFile(opts installOptions, out *installResult) (*installResult, error) {
	out.Repo = opts.File
	out.Asset = filepath.Base(opts.File)
	toDir := resolveTargetDir(opts)
	if err := os.MkdirAll(toDir, 0755); err != nil {
		return nil, err
	}
	planned, err := extractArchive(opts.File, opts.File, toDir, opts.FileGlob, true)
	if err != nil {
		return nil, err
	}
	out.SHA, _ = fileSHA256(opts.File)
	checksumStatus := "unverified, no checksum available for a local file"
	if opts.VerifySHA256 != "" {
		if !strings.EqualFold(strings.TrimSpace(opts.VerifySHA256), out.SHA) {
			return nil, fmt.Errorf("sha256 mismatch: expected %s got %s", opts.VerifySHA256, out.SHA)
		}
		checksumStatus = "verified via --verify-sha256"
	}
	if err := confirmPlan(out.Asset, out.SHA, checksumStatus, planned, opts); err != nil {
		return nil, err
	}
	files, err := extractArchive(opts.File, opts.File, toDir, opts.FileGlob, false)
	if err != nil {
		return nil, err
	}
	out.Files = files
	for _, f := range files {
		os.Chmod(f, 0755)
	}
	m, _ := loadManifest()
	m.Tools[opts.File] = &ToolRecord{
		Repo: opts.File, Source: "file", Asset: out.Asset,
		Files: out.Files, Target: toDir,
		InstalledAt: time.Now().UTC(),
	}
	m.save()
	fmt.Printf("extracted %s -> %s\n", opts.File, strings.Join(out.Files, ", "))
	return out, nil
}

func verifySiblingChecksum(chk Asset, assetName, gotSHA, gotMD5 string, gh *GitHubClient) error {
	data, err := downloadBytes(chk.BrowserDownloadURL, gh.token)
	if err != nil {
		return fmt.Errorf("download checksum: %w", err)
	}
	want := parseChecksumFile(string(data), assetName)
	if want == "" {
		return fmt.Errorf("could not find checksum for %s in %s", assetName, chk.Name)
	}
	var got string
	switch len(want) {
	case 64:
		got = gotSHA
	case 32:
		got = gotMD5
	default:
		return fmt.Errorf("unsupported checksum length %d in %s (only sha256/md5 supported)", len(want), chk.Name)
	}
	if !strings.EqualFold(want, got) {
		return fmt.Errorf("checksum mismatch: expected %s got %s", want, got)
	}
	return nil
}

func parseChecksumFile(content, assetName string) string {
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 0 {
			continue
		}
		var hash, file string
		if len(fields) == 1 {
			hash = fields[0]
		} else {
			hash = fields[0]
			file = strings.TrimPrefix(fields[1], "*")
		}
		if (file == "" || file == assetName || strings.HasSuffix(file, assetName)) && (len(hash) == 64 || len(hash) == 32) {
			valid := true
			for _, c := range hash {
				if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
					valid = false
					break
				}
			}
			if valid {
				return hash
			}
		}
	}
	return ""
}

func compareVersions(a, b string) int {
	av := extractVersion(a)
	bv := extractVersion(b)
	if len(av) == 0 || len(bv) == 0 {
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
		return 0
	}
	for i := 0; i < len(av) || i < len(bv); i++ {
		var x, y int
		if i < len(av) {
			x = av[i]
		}
		if i < len(bv) {
			y = bv[i]
		}
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	return 0
}

func extractVersion(s string) []int {
	isPre := strings.Contains(s, "-")
	s = strings.TrimPrefix(s, "v")
	var nums []int
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r < '0' || r > '9' }) {
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				break
			}
			n = n*10 + int(c-'0')
		}
		nums = append(nums, n)
	}
	if len(nums) == 0 {
		return nil
	}
	if isPre {
		nums = append(nums, -1)
	}
	return nums
}
