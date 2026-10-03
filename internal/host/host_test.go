package host

import "testing"

func TestParseVMStat(t *testing.T) {
	text := `Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                               12000.
Pages active:                            200000.
Pages inactive:                          150000.
Pages wired down:                        100000.
Pages occupied by compressor:             50000.`
	used, ok := ParseVMStat(text)
	if !ok || used != 350000*16384 {
		t.Fatalf("used=%d ok=%v", used, ok)
	}
}

func TestParseLoad(t *testing.T) {
	if v := ParseLoad("{ 2.61 2.10 1.98 }"); v != 2.61 {
		t.Fatal(v)
	}
}

func TestParseBattery(t *testing.T) {
	level, plugged := ParseBattery("Now drawing from 'Battery Power'\n -InternalBattery-0 (id=1)\t89%; discharging; 6:02 remaining present: true")
	if *level != 89 || *plugged {
		t.Fatal(*level, *plugged)
	}
	level, plugged = ParseBattery("Now drawing from 'AC Power'\n -InternalBattery-0 (id=1)\t100%; charged; 0:00 remaining present: true")
	if *level != 100 || !*plugged {
		t.Fatal(*level, *plugged)
	}
	if l, p := ParseBattery("Now drawing from 'AC Power'"); l != nil || p != nil {
		t.Fatal("desktop mac has no battery")
	}
}
