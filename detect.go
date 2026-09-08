package main

import (
	"runtime"
	"slices"
	"strings"
)

type systemInfo struct {
	os   string
	arch string
}

func currentSystem() systemInfo {
	return systemInfo{runtime.GOOS, runtime.GOARCH}
}

var osAliases = map[string][]string{
	"darwin":  {"darwin", "macos", "mac", "osx"},
	"linux":   {"linux"},
	"windows": {"windows", "win"},
}

var archAliases = map[string][]string{
	"amd64": {"amd64", "x86_64", "x64", "x86-64"},
	"arm64": {"arm64", "aarch64", "aarch-64"},
}

func matchAny(s string, candidates []string) bool {
	s = strings.ToLower(s)
	return slices.ContainsFunc(candidates, func(c string) bool { return strings.Contains(s, c) })
}

func osMatches(name, os string) bool {
	if os == "all" {
		return true
	}
	cands, ok := osAliases[os]
	if !ok {
		cands = []string{strings.ToLower(os)}
	}
	return matchAny(name, cands)
}

func archMatches(name, arch string) bool {
	if arch == "all" || arch == "" {
		return true
	}
	cands, ok := archAliases[arch]
	if !ok {
		cands = []string{strings.ToLower(arch)}
	}
	return matchAny(name, cands)
}

var checksumSuffixes = []string{".sha256", ".sha256sum", ".md5", ".md5sum"}

func detectAsset(assets []Asset, sys systemInfo, filters []string) (Asset, bool) {
	for _, a := range assets {
		name := strings.ToLower(a.Name)
		if isSkippedAsset(name) {
			continue
		}
		ok := true
		for _, f := range filters {
			neg := strings.HasPrefix(f, "^")
			want := strings.ToLower(strings.TrimPrefix(f, "^"))
			contains := strings.Contains(name, want)
			if neg == contains {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		if osMatches(name, sys.os) && archMatches(name, sys.arch) {
			return a, true
		}
	}
	return Asset{}, false
}

func isSkippedAsset(name string) bool {
	return slices.ContainsFunc(checksumSuffixes, func(s string) bool { return strings.HasSuffix(name, s) })
}

func checksumAssetFor(assets []Asset, name string) (Asset, bool) {
	lname := strings.ToLower(name)
	for _, a := range assets {
		an := strings.ToLower(a.Name)
		if !slices.ContainsFunc(checksumSuffixes, func(s string) bool { return strings.HasSuffix(an, s) }) {
			continue
		}
		base := an
		for _, s := range checksumSuffixes {
			base = strings.TrimSuffix(base, s)
		}
		if base == lname || strings.HasPrefix(lname, base) {
			return a, true
		}
	}
	return Asset{}, false
}
