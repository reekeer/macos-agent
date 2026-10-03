package host

import (
	"context"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/reekeer/macos-agent/internal/panel"
)

const gb = 1 << 30

var (
	vmPage    = regexp.MustCompile(`page size of (\d+) bytes`)
	vmLine    = regexp.MustCompile(`^(Pages [a-z ]+|Pages occupied by compressor):\s+(\d+)\.`)
	battLevel = regexp.MustCompile(`(\d+)%`)
)

func run(ctx context.Context, name string, args ...string) string {
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func ParseVMStat(text string) (used uint64, ok bool) {
	m := vmPage.FindStringSubmatch(text)
	if m == nil {
		return 0, false
	}
	page, _ := strconv.ParseUint(m[1], 10, 64)
	pages := map[string]uint64{}
	for _, line := range strings.Split(text, "\n") {
		if mm := vmLine.FindStringSubmatch(strings.TrimSpace(line)); mm != nil {
			n, _ := strconv.ParseUint(mm[2], 10, 64)
			pages[mm[1]] = n
		}
	}
	active, wired, compressed := pages["Pages active"], pages["Pages wired down"], pages["Pages occupied by compressor"]
	return (active + wired + compressed) * page, true
}

func ParseLoad(text string) float64 {
	fields := strings.Fields(strings.Trim(text, "{} "))
	if len(fields) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(fields[0], 64)
	return v
}

func ParseBattery(text string) (*int, *bool) {
	m := battLevel.FindStringSubmatch(text)
	if m == nil {
		return nil, nil
	}
	level, _ := strconv.Atoi(m[1])
	plugged := strings.Contains(text, "AC Power")
	return &level, &plugged
}

func FreeGb(path string) float64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0
	}
	return float64(uint64(st.Bavail)*uint64(st.Bsize)) / gb
}

func Stats(ctx context.Context, tartHome, tartVersion string) panel.HostStats {
	s := panel.HostStats{Cpus: runtime.NumCPU(), Tart: tartVersion}
	s.OS = run(ctx, "sw_vers", "-productVersion")
	if total, err := strconv.ParseUint(run(ctx, "sysctl", "-n", "hw.memsize"), 10, 64); err == nil {
		s.MemoryGb = float64(total) / gb
	}
	if used, ok := ParseVMStat(run(ctx, "vm_stat")); ok {
		s.MemoryUsedGb = float64(used) / gb
	}
	s.Load = ParseLoad(run(ctx, "sysctl", "-n", "vm.loadavg"))
	s.DiskFreeGb = FreeGb(tartHome)
	s.Battery, s.Charging = ParseBattery(run(ctx, "pmset", "-g", "batt"))
	return s
}
