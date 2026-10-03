package update

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	repo  = "reekeer/macos-agent"
	asset = "reekeer-agent-darwin-arm64"
)

var client = &http.Client{Timeout: 5 * time.Minute}

func get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", url, res.StatusCode)
	}
	return io.ReadAll(io.LimitReader(res.Body, 200<<20))
}

func Latest(ctx context.Context) (string, error) {
	raw, err := get(ctx, "https://api.github.com/repos/"+repo+"/releases/latest")
	if err != nil {
		return "", err
	}
	var rel struct {
		Tag string `json:"tag_name"`
	}
	if err := json.Unmarshal(raw, &rel); err != nil {
		return "", err
	}
	return rel.Tag, nil
}

func checksum(sums []byte, name string) string {
	scan := bufio.NewScanner(strings.NewReader(string(sums)))
	for scan.Scan() {
		f := strings.Fields(scan.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return f[0]
		}
	}
	return ""
}

func Apply(ctx context.Context, tag, bin string) error {
	base := "https://github.com/" + repo + "/releases/download/" + tag + "/"
	sums, err := get(ctx, base+"checksums.txt")
	if err != nil {
		return err
	}
	want := checksum(sums, asset)
	if want == "" {
		return fmt.Errorf("no checksum for %s", asset)
	}
	body, err := get(ctx, base+asset)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != want {
		return fmt.Errorf("checksum mismatch for %s", tag)
	}
	tmp := bin + ".new"
	if err := os.WriteFile(tmp, body, 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, bin)
}
