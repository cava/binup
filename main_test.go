package main

import "testing"

func TestNormalizeRepoID(t *testing.T) {
	cases := map[string]string{
		"pimalaya/himalaya":                             "pimalaya/himalaya",
		"https://github.com/pimalaya/himalaya":          "pimalaya/himalaya",
		"http://github.com/pimalaya/himalaya":           "pimalaya/himalaya",
		"github.com/pimalaya/himalaya":                  "pimalaya/himalaya",
		"https://github.com/pimalaya/himalaya.git":      "pimalaya/himalaya",
		"https://github.com/pimalaya/himalaya/releases": "pimalaya/himalaya",
		"https://github.com/pimalaya/himalaya/":         "pimalaya/himalaya",
		"himalaya.tar.gz":                               "himalaya.tar.gz",
		"./himalaya.tar.gz":                             "./himalaya.tar.gz",
		"/tmp/archives/himalaya.tar.gz":                 "/tmp/archives/himalaya.tar.gz",
		"https://gitlab.com/pimalaya/himalaya":          "https://gitlab.com/pimalaya/himalaya",
	}
	for in, want := range cases {
		if got := normalizeRepoID(in); got != want {
			t.Errorf("normalizeRepoID(%q) = %q, want %q", in, got, want)
		}
	}
}
