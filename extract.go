package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const maxChecksumBody = 256 << 10

// defaultMaxDownloadSize caps a single download unless overridden with
// --max-download-size.
const defaultMaxDownloadSize = 25 << 20

// parseSize parses a human-readable byte size such as "50MB", "1GiB", or a
// bare number of bytes. Suffixes are case-insensitive and accept both decimal
// (KB, MB, GB) and binary (KiB, MiB, GiB) units.
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	i := 0
	for i < len(s) && (s[i] == '.' || (s[i] >= '0' && s[i] <= '9')) {
		i++
	}
	numStr := s[:i]
	if numStr == "" {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	num, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	var mult float64
	switch strings.ToLower(strings.TrimSpace(s[i:])) {
	case "", "b":
		mult = 1
	case "k", "kb":
		mult = 1e3
	case "kib":
		mult = 1 << 10
	case "m", "mb":
		mult = 1e6
	case "mib":
		mult = 1 << 20
	case "g", "gb":
		mult = 1e9
	case "gib":
		mult = 1 << 30
	case "t", "tb":
		mult = 1e12
	case "tib":
		mult = 1 << 40
	default:
		return 0, fmt.Errorf("invalid size unit in %q (use B, KB, MB, GB or KiB, MiB, GiB)", s)
	}
	return int64(num * mult), nil
}

// parseMaxDownloadSize turns the --max-download-size flag into a byte limit,
// falling back to defaultMaxDownloadSize when unset.
func parseMaxDownloadSize(s string) (int64, error) {
	if strings.TrimSpace(s) == "" {
		return defaultMaxDownloadSize, nil
	}
	n, err := parseSize(s)
	if err != nil {
		return 0, fmt.Errorf("invalid --max-download-size: %w", err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("invalid --max-download-size %q: must be positive", s)
	}
	return n, nil
}

// humanBytes formats a byte count like "25.0 MiB".
func humanBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB"}
	v := float64(n)
	i := -1
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}

func httpGet(rawURL, token string, timeout time.Duration) (*http.Response, error) {
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("User-Agent", "binup")
	u, _ := url.Parse(rawURL)
	if token != "" && u != nil && (u.Hostname() == "github.com" || strings.HasSuffix(u.Hostname(), ".github.com")) {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		msg := strings.TrimSpace(string(body))
		if len(msg) > 200 {
			msg = msg[:200] + "..."
		}
		return nil, fmt.Errorf("download %s: %d %s", rawURL, resp.StatusCode, msg)
	}
	return resp, nil
}

func download(rawURL, token string, maxSize int64) (string, string, string, error) {
	if maxSize <= 0 {
		maxSize = defaultMaxDownloadSize
	}
	resp, err := httpGet(rawURL, token, 10*time.Minute)
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()
	tmp, err := os.CreateTemp("", "binup-*")
	if err != nil {
		return "", "", "", err
	}
	hs := sha256.New()
	hm := md5.New()
	lr := io.LimitReader(resp.Body, maxSize+1)
	n, err := io.Copy(io.MultiWriter(tmp, hs, hm), lr)
	tmp.Close()
	if n > maxSize {
		os.Remove(tmp.Name())
		return "", "", "", fmt.Errorf("download exceeds %s limit; raise it with --max-download-size (e.g. --max-download-size=%s)", humanBytes(maxSize), humanBytes(maxSize*2))
	}
	if err != nil {
		os.Remove(tmp.Name())
		return "", "", "", err
	}
	return tmp.Name(), hex.EncodeToString(hs.Sum(nil)), hex.EncodeToString(hm.Sum(nil)), nil
}

func downloadBytes(rawURL, token string) ([]byte, error) {
	resp, err := httpGet(rawURL, token, 5*time.Minute)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, maxChecksumBody))
}

// extractArchive extracts (or, with dryRun, just plans) the files matching
// fileGlob from the archive at path into outDir, returning their destination
// paths. With dryRun true, no files are written; the returned paths are a
// preview of what would happen.
func extractArchive(path, name, outDir, fileGlob string, dryRun bool) ([]string, error) {
	lower := strings.ToLower(name)
	if lower == "" {
		lower = strings.ToLower(path)
	}
	switch {
	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
		return extractTar(path, outDir, fileGlob, true, dryRun)
	case strings.HasSuffix(lower, ".tar.bz2") || strings.HasSuffix(lower, ".tbz2"):
		return extractTarBzip(path, outDir, fileGlob, dryRun)
	case strings.HasSuffix(lower, ".tar.xz") || strings.HasSuffix(lower, ".txz"):
		return extractTarXZ(path, outDir, fileGlob, dryRun)
	case strings.HasSuffix(lower, ".tar"):
		return extractTar(path, outDir, fileGlob, false, dryRun)
	case strings.HasSuffix(lower, ".zip"):
		return extractZip(path, outDir, fileGlob, dryRun)
	case strings.HasSuffix(lower, ".gz"):
		return extractSingleFile(path, name, outDir, fileGlob, ".gz", dryRun)
	case strings.HasSuffix(lower, ".bz2"):
		return extractSingleFile(path, name, outDir, fileGlob, ".bz2", dryRun)
	case strings.HasSuffix(lower, ".xz"):
		return extractXZFile(path, name, outDir, fileGlob, dryRun)
	default:
		dst := filepath.Join(outDir, filepath.Base(name))
		if dst == outDir {
			dst = filepath.Join(outDir, filepath.Base(path))
		}
		if !dryRun {
			if err := copyFile(path, dst); err != nil {
				return nil, err
			}
		}
		return []string{dst}, nil
	}
}

func matchesGlob(name, pattern string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	matched, _ := filepath.Match(pattern, filepath.Base(name))
	return matched
}

func selectExecs[T any](members []T, fileGlob string, name func(T) string, isExec func(T) bool, archiveName string) ([]T, error) {
	if fileGlob != "" && fileGlob != "*" {
		var out []T
		for _, m := range members {
			if matchesGlob(name(m), fileGlob) {
				out = append(out, m)
			}
		}
		return out, nil
	}
	var execs []T
	var names []string
	for _, m := range members {
		if isExec(m) {
			execs = append(execs, m)
			names = append(names, name(m))
		}
	}
	if len(execs) != 1 {
		if len(execs) == 0 {
			return nil, fmt.Errorf("%s: no member with the executable bit set; pass --file GLOB to choose", archiveName)
		}
		return nil, fmt.Errorf("%s: %d members with the executable bit set (%s); pass --file GLOB to choose one",
			archiveName, len(execs), strings.Join(names, ", "))
	}
	return execs, nil
}

func extractTar(path, outDir, fileGlob string, gzipped, dryRun bool) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var r io.Reader = f
	if gzipped {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		r = gz
	}
	return extractTarMembers(tar.NewReader(r), outDir, fileGlob, filepath.Base(path), dryRun)
}

func extractTarBzip(path, outDir, fileGlob string, dryRun bool) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return extractTarMembers(tar.NewReader(bzip2.NewReader(f)), outDir, fileGlob, filepath.Base(path), dryRun)
}

func extractTarMembers(tr *tar.Reader, outDir, fileGlob, archiveName string, dryRun bool) ([]string, error) {
	type member struct {
		name string
		mode os.FileMode
		data []byte
	}
	var members []member
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeReg || !matchesGlob(hdr.Name, fileGlob) {
			continue
		}
		var data []byte
		if !dryRun {
			data, err = io.ReadAll(tr)
			if err != nil {
				return nil, err
			}
		}
		members = append(members, member{hdr.Name, os.FileMode(hdr.Mode), data})
	}
	selected, err := selectExecs(members, fileGlob,
		func(m member) string { return m.name },
		func(m member) bool { return m.mode&0111 != 0 },
		archiveName)
	if err != nil {
		return nil, err
	}
	var written []string
	for _, m := range selected {
		dst := filepath.Join(outDir, filepath.Base(m.name))
		if !dryRun {
			if err := writeFile(bytes.NewReader(m.data), dst, m.mode); err != nil {
				return written, err
			}
		}
		written = append(written, dst)
	}
	return written, nil
}

func extractZip(path, outDir, fileGlob string, dryRun bool) ([]string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	type member struct {
		name string
		mode os.FileMode
		file *zip.File
	}
	var members []member
	for _, f := range r.File {
		if !f.FileInfo().IsDir() {
			members = append(members, member{f.Name, f.Mode(), f})
		}
	}
	selected, err := selectExecs(members, fileGlob,
		func(m member) string { return m.name },
		func(m member) bool { return m.mode&0111 != 0 },
		filepath.Base(path))
	if err != nil {
		return nil, err
	}
	var written []string
	for _, m := range selected {
		dst := filepath.Join(outDir, filepath.Base(m.name))
		if !dryRun {
			rc, err := m.file.Open()
			if err != nil {
				return written, err
			}
			if err := writeFile(rc, dst, m.mode); err != nil {
				rc.Close()
				return written, err
			}
			rc.Close()
		}
		written = append(written, dst)
	}
	return written, nil
}

func extractSingleFile(path, name, outDir, fileGlob, ext string, dryRun bool) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	src := name
	if src == "" {
		src = path
	}
	base := strings.TrimSuffix(strings.ToLower(filepath.Base(src)), ext)
	if !matchesGlob(base, fileGlob) {
		return nil, nil
	}
	dst := filepath.Join(outDir, base)
	if dryRun {
		return []string{dst}, nil
	}
	var r io.Reader = f
	switch ext {
	case ".gz":
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		r = gz
	case ".bz2":
		r = bzip2.NewReader(f)
	}
	return []string{dst}, writeFile(r, dst, 0755)
}

func extractXZFile(path, name, outDir, fileGlob string, dryRun bool) ([]string, error) {
	src := name
	if src == "" {
		src = path
	}
	base := strings.TrimSuffix(filepath.Base(src), ".xz")
	if !matchesGlob(base, fileGlob) {
		return nil, nil
	}
	dst := filepath.Join(outDir, base)
	if dryRun {
		return []string{dst}, nil
	}
	r, err := openXZ(path)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return []string{dst}, writeFile(r, dst, 0755)
}

func extractTarXZ(path, outDir, fileGlob string, dryRun bool) ([]string, error) {
	r, err := openXZ(path)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return extractTarMembers(tar.NewReader(r), outDir, fileGlob, filepath.Base(path), dryRun)
}

func writeFile(r io.Reader, dst string, mode os.FileMode) error {
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, r)
	return err
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
