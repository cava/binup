package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestLooksLikeDoc(t *testing.T) {
	cases := map[string]bool{
		"README.md":    true,
		"README":       true,
		"LICENSE":      true,
		"LICENSE.txt":  true,
		"LICENSE.md":   true,
		"CHANGELOG":    true,
		"CHANGELOG.md": true,
	}
	for name, want := range cases {
		if got := looksLikeDoc(name); got != want {
			t.Errorf("looksLikeDoc(%q) = %v, want %v", name, got, want)
		}
	}
}

type fakeMember struct {
	name string
	exec bool
}

func selectFakeExecs(members []fakeMember, fileGlob string) ([]fakeMember, error) {
	return selectExecs(members, fileGlob,
		func(m fakeMember) string { return m.name },
		func(m fakeMember) bool { return m.exec },
		"archive.zip")
}

func TestSelectExecsWithExecutableBit(t *testing.T) {
	members := []fakeMember{{"README.md", false}, {"tool", true}}
	got, err := selectFakeExecs(members, "*")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].name != "tool" {
		t.Fatalf("got %v, want [tool]", got)
	}
}

func TestSelectExecsMultipleExecutablesIsAmbiguous(t *testing.T) {
	members := []fakeMember{{"tool", true}, {"tool-helper", true}}
	_, err := selectFakeExecs(members, "*")
	if err == nil {
		t.Fatal("expected an error for multiple executables, got nil")
	}
}

func TestSelectExecsFallsBackToSoleNonDocWhenNoExecBit(t *testing.T) {
	// Mimics archives (like some cross-built zips) that don't preserve the
	// Unix executable bit on any member.
	members := []fakeMember{{"README.md", false}, {"LICENSE", false}, {"tool", false}}
	got, err := selectFakeExecs(members, "*")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].name != "tool" {
		t.Fatalf("got %v, want [tool]", got)
	}
}

func TestSelectExecsAmbiguousNonDocsWithNoExecBit(t *testing.T) {
	members := []fakeMember{{"tool", false}, {"tool-helper", false}}
	_, err := selectFakeExecs(members, "*")
	if err == nil {
		t.Fatal("expected an error for ambiguous non-doc candidates, got nil")
	}
}

func TestSelectExecsNoCandidatesAtAll(t *testing.T) {
	members := []fakeMember{{"README.md", false}, {"LICENSE", false}}
	_, err := selectFakeExecs(members, "*")
	if err == nil {
		t.Fatal("expected an error when every member looks like a doc, got nil")
	}
}

// writeTestZip builds a zip file at dir/name whose entries have the given
// (name, mode, content) and no executable bit set, mirroring archives built
// by toolchains that don't preserve Unix permissions.
func writeTestZip(t *testing.T, dir, name string, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for entryName, content := range entries {
		w, err := zw.Create(entryName)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExtractZipWithoutExecBitPicksSoleBinary(t *testing.T) {
	dir := t.TempDir()
	zipPath := writeTestZip(t, dir, "tool.zip", map[string]string{
		"tool-v1.0.0/README.md": "docs",
		"tool-v1.0.0/LICENSE":   "license text",
		"tool-v1.0.0/tool":      "pretend binary contents",
	})
	files, err := extractArchive(zipPath, "tool.zip", dir, "*", false)
	if err != nil {
		t.Fatalf("extractArchive: %v", err)
	}
	if len(files) != 1 || filepath.Base(files[0]) != "tool" {
		t.Fatalf("got %v, want a single extracted file named tool", files)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "pretend binary contents" {
		t.Fatalf("unexpected extracted content: %q", data)
	}
}
