//go:build windows

package hostmetrics

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32                 = windows.NewLazySystemDLL("kernel32.dll")
	procGetSystemTimes       = kernel32.NewProc("GetSystemTimes")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
)

type windowsProbe struct {
	prevBusy, prevTotal uint64
	primed              bool
}

func newProbe() probe { return &windowsProbe{} }

func filetimeTicks(ft windows.Filetime) uint64 {
	return uint64(ft.HighDateTime)<<32 | uint64(ft.LowDateTime)
}

// cpuPercent uses GetSystemTimes; kernel time there includes idle time.
func (p *windowsProbe) cpuPercent() (float64, bool) {
	var idle, kernel, user windows.Filetime
	ret, _, _ := procGetSystemTimes.Call(
		uintptr(unsafe.Pointer(&idle)),
		uintptr(unsafe.Pointer(&kernel)),
		uintptr(unsafe.Pointer(&user)),
	)
	if ret == 0 {
		return 0, false
	}
	total := filetimeTicks(kernel) + filetimeTicks(user)
	busy := total - filetimeTicks(idle)
	prevBusy, prevTotal, primed := p.prevBusy, p.prevTotal, p.primed
	p.prevBusy, p.prevTotal, p.primed = busy, total, true
	if !primed {
		return 0, false
	}
	return tickPercent(prevBusy, prevTotal, busy, total)
}

// memoryStatusEx mirrors MEMORYSTATUSEX.
type memoryStatusEx struct {
	length               uint32
	memoryLoad           uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

func (p *windowsProbe) memory() (used, total uint64, ok bool) {
	var status memoryStatusEx
	status.length = uint32(unsafe.Sizeof(status))
	ret, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&status)))
	if ret == 0 || status.totalPhys == 0 || status.availPhys > status.totalPhys {
		return 0, 0, false
	}
	return status.totalPhys - status.availPhys, status.totalPhys, true
}
