package runnerpkg

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	Archive  = "actions-runner.tar.gz"
	stamp    = "actions-runner.version"
	releases = "https://api.github.com/repos/actions/runner/releases/latest"
)

type release struct {
	Tag    string `json:"tag_name"`
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

var client = &http.Client{Timeout: 10 * time.Minute}

func Current(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, stamp))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func Ensure(ctx context.Context, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releases, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("actions/runner releases: HTTP %d", res.StatusCode)
	}
	var rel release
	if err := json.NewDecoder(res.Body).Decode(&rel); err != nil {
		return "", err
	}
	version := strings.TrimPrefix(rel.Tag, "v")
	if version != "" && version == Current(dir) {
		if _, err := os.Stat(filepath.Join(dir, Archive)); err == nil {
			return version, nil
		}
	}
	want := fmt.Sprintf("actions-runner-osx-arm64-%s.tar.gz", version)
	url := ""
	for _, a := range rel.Assets {
		if a.Name == want {
			url = a.URL
		}
	}
	if url == "" {
		return "", fmt.Errorf("no %s in the release", want)
	}
	if err := download(ctx, url, filepath.Join(dir, Archive)); err != nil {
		return "", err
	}
	return version, os.WriteFile(filepath.Join(dir, stamp), []byte(version), 0o644)
}

func download(ctx context.Context, url, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", url, res.StatusCode)
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, res.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
