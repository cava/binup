package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const githubAPI = "https://api.github.com"

type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type Release struct {
	TagName     string    `json:"tag_name"`
	TarballURL  string    `json:"tarball_url"`
	Assets      []Asset   `json:"assets"`
	PublishedAt time.Time `json:"published_at"`
	Prerelease  bool      `json:"prerelease"`
	Draft       bool      `json:"draft"`
}

type GitHubClient struct {
	token  string
	client *http.Client
}

func newGitHubClient() *GitHubClient {
	token := os.Getenv("BINUP_GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if after, ok := strings.CutPrefix(token, "@"); ok {
		data, err := os.ReadFile(after)
		if err == nil {
			token = strings.TrimSpace(string(data))
		}
	}
	return &GitHubClient{
		token:  token,
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *GitHubClient) do(req *http.Request) (*http.Response, error) {
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("User-Agent", "binup")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := strings.TrimSpace(string(body))
		return nil, fmt.Errorf("github api %d: %s (rate-limit remaining=%s reset=%s)",
			resp.StatusCode, msg,
			resp.Header.Get("X-Ratelimit-Remaining"), resp.Header.Get("X-Ratelimit-Reset"))
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := strings.TrimSpace(string(body))
		return nil, fmt.Errorf("github api %d: %s", resp.StatusCode, msg)
	}
	return resp, nil
}

func splitRepo(target string) (owner, repo string, err error) {
	t := strings.TrimPrefix(target, "https://github.com/")
	t = strings.TrimPrefix(t, "http://github.com/")
	t = strings.TrimPrefix(t, "github.com/")
	t = strings.TrimSuffix(t, "/")
	parts := strings.SplitN(t, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid repo %q (expected owner/repo)", target)
	}
	return parts[0], parts[1], nil
}

// latestRelease returns the newest release for owner/repo. If minAge > 0,
// releases published more recently than minAge are skipped, so the newest
// release considered "latest" is guaranteed to be at least minAge old (a
// guard against installing something just published, akin to npm's
// minimum-release-age).
func (c *GitHubClient) latestRelease(owner, repo string, includePre bool, minAge time.Duration) (*Release, error) {
	if !includePre && minAge <= 0 {
		u := fmt.Sprintf("%s/repos/%s/%s/releases/latest", githubAPI, owner, repo)
		req, _ := http.NewRequest("GET", u, nil)
		resp, err := c.do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		var rel Release
		if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
			return nil, err
		}
		return &rel, nil
	}
	u := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=100", githubAPI, owner, repo)
	req, _ := http.NewRequest("GET", u, nil)
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var rels []Release
	if err := json.NewDecoder(resp.Body).Decode(&rels); err != nil {
		return nil, err
	}
	cutoff := time.Now().Add(-minAge)
	for i := range rels {
		if rels[i].Draft || (!includePre && rels[i].Prerelease) {
			continue
		}
		if minAge > 0 && !rels[i].PublishedAt.IsZero() && rels[i].PublishedAt.After(cutoff) {
			continue
		}
		return &rels[i], nil
	}
	if minAge > 0 {
		return nil, fmt.Errorf("no release for %s/%s is at least %s old", owner, repo, minAge)
	}
	return nil, fmt.Errorf("no releases for %s/%s", owner, repo)
}

func (c *GitHubClient) releaseByTag(owner, repo, tag string) (*Release, error) {
	u := fmt.Sprintf("%s/repos/%s/%s/releases/tags/%s", githubAPI, owner, repo, url.PathEscape(tag))
	req, _ := http.NewRequest("GET", u, nil)
	resp, err := c.do(req)
	if err == nil {
		defer resp.Body.Close()
		var rel Release
		if json.NewDecoder(resp.Body).Decode(&rel) == nil && rel.TagName != "" {
			return &rel, nil
		}
	}
	u = fmt.Sprintf("%s/repos/%s/%s/releases?per_page=100", githubAPI, owner, repo)
	req, _ = http.NewRequest("GET", u, nil)
	resp, err = c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var rels []Release
	if err := json.NewDecoder(resp.Body).Decode(&rels); err != nil {
		return nil, err
	}
	for i := range rels {
		if strings.Contains(rels[i].TagName, tag) {
			return &rels[i], nil
		}
	}
	return nil, fmt.Errorf("no release matching tag %q for %s/%s", tag, owner, repo)
}

func (c *GitHubClient) rateLimit() (string, error) {
	req, _ := http.NewRequest("GET", githubAPI+"/rate_limit", nil)
	resp, err := c.do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Resources struct {
			Core struct {
				Limit     int   `json:"limit"`
				Remaining int   `json:"remaining"`
				Reset     int64 `json:"reset"`
			} `json:"core"`
		} `json:"resources"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	core := out.Resources.Core
	reset := time.Unix(core.Reset, 0).Format(time.RFC3339)
	return fmt.Sprintf("core: %d/%d remaining, resets %s", core.Remaining, core.Limit, reset), nil
}
