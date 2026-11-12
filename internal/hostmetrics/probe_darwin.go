//go:build darwin

package hostmetrics

import (
	"context"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type darwinProbe struct{}

func newProbe() probe { return darwinProbe{} }

// cpuPercent sums every process's share from ps and spreads it over the
// cores. The host tick counters need a Mach call that a cgo-free build cannot
// make; ps is always present and costs a few milliseconds per sample. Inside
// a sandbox that forbids it the reading is simply unavailable.
func (darwinProbe) cpuPercent() (float64, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/bin/ps", "-A", "-o", "%cpu=").Output()
	if err != nil {
		return 0, false
	}
	return sumProcessPercent(string(out), runtime.NumCPU())
}

func sumProcessPercent(out string, cores int) (float64, bool) {
	if cores < 1 {
		return 0, false
	}
	var sum float64
	var seen bool
	for _, field := range strings.Fields(out) {
		v, err := strconv.ParseFloat(strings.ReplaceAll(field, ",", "."), 64)
		if err != nil {
			continue
		}
		sum += v
		seen = true
	}
	if !seen {
		return 0, false
	}
	return sum / float64(cores), true
}

// memory reports physical memory, and how much of it the kernel's memory
// pressure level counts as in use (kern.memorystatus_level is percent free).
func (darwinProbe) memory() (used, total uint64, ok bool) {
	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil || total == 0 {
		return 0, 0, false
	}
	free, err := unix.SysctlUint32("kern.memorystatus_level")
	if err != nil || free > 100 {
		return 0, 0, false
	}
	return total / 100 * uint64(100-free), total, true
}
