package update

import "testing"

func TestChecksum(t *testing.T) {
	sums := []byte("abc123  reekeer-agent-darwin-arm64\ndef456 *other\n")
	if checksum(sums, "reekeer-agent-darwin-arm64") != "abc123" || checksum(sums, "other") != "def456" || checksum(sums, "x") != "" {
		t.Fatal("bad parse")
	}
}
