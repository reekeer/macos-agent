package tart

import "testing"

func TestParseList(t *testing.T) {
	raw := []byte(`[{"Source":"OCI","Name":"ghcr.io/cirruslabs/macos-tahoe-xcode:27","Size":81,"State":"stopped"},{"Source":"local","Name":"rk-mac-del-1","Running":true}]`)
	vms, err := ParseList(raw)
	if err != nil || len(vms) != 2 {
		t.Fatal(err, vms)
	}
	if vms[0].Source != "OCI" || vms[0].SizeGb != 81 || vms[1].State != "running" {
		t.Fatalf("%+v", vms)
	}
}

func TestProgress(t *testing.T) {
	if p := Progress("pulling disk (61.9 GB compressed)... 37.5%"); p != "37.5%" {
		t.Fatal(p)
	}
	if Progress("pulling manifest...") != "" {
		t.Fatal("no percent expected")
	}
}
