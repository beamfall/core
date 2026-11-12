//go:build linux

package hostmetrics

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

type linuxProbe struct {
	prevBusy, prevTotal uint64
	primed              bool
}

func newProbe() probe { return &linuxProbe{} }

// cpuPercent reads the aggregate "cpu" line of /proc/stat: user nice system
// idle iowait irq softirq steal. Idle and iowait are the not-busy share.
func (p *linuxProbe) cpuPercent() (float64, bool) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, false
	}
	line, _, _ := strings.Cut(string(data), "\n")
	busy, total, ok := parseProcStatCPU(line)
	if !ok {
		return 0, false
	}
	prevBusy, prevTotal, primed := p.prevBusy, p.prevTotal, p.primed
	p.prevBusy, p.prevTotal, p.primed = busy, total, true
	if !primed {
		return 0, false
	}
	return tickPercent(prevBusy, prevTotal, busy, total)
}

func parseProcStatCPU(line string) (busy, total uint64, ok bool) {
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, false
	}
	var idle uint64
	for i, field := range fields[1:] {
		if i >= 8 { // guest time is already counted inside user and nice
			break
		}
		v, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		total += v
		if i == 3 || i == 4 {
			idle += v
		}
	}
	return total - idle, total, true
}

func (p *linuxProbe) memory() (used, total uint64, ok bool) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, false
	}
	defer func() { _ = f.Close() }()
	var available uint64
	var haveTotal, haveAvailable bool
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		key, rest, found := strings.Cut(scanner.Text(), ":")
		if !found || (key != "MemTotal" && key != "MemAvailable") {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		kb, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		if key == "MemTotal" {
			total, haveTotal = kb*1024, true
		} else {
			available, haveAvailable = kb*1024, true
		}
	}
	if !haveTotal || !haveAvailable || available > total {
		return 0, 0, false
	}
	return total - available, total, true
}
