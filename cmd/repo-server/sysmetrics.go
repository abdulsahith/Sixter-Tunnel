package main

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

/*
Host-wide CPU + memory for the dashboard sidebar.

CPU% is derived from /proc/stat: we sample the jiffy counters, and on the next
call compute the busy delta over the interval. So the FIRST call after boot
returns 0 (no previous sample to diff against) and every call after that is a
real number. Memory comes from /proc/meminfo. Both are Linux-only; on other
platforms we return zeros and the meters simply read "—".
*/

type cpuSample struct {
	idle  uint64
	total uint64
	at    time.Time
}

var (
	cpuMu   sync.Mutex
	lastCPU cpuSample
)

// systemMetrics returns cpu%, mem%, memUsedBytes, memTotalBytes, loadAvg1.
func systemMetrics() (cpuPct, memPct int, memUsed, memTotal int64, load string) {
	if runtime.GOOS != "linux" {
		return 0, 0, 0, 0, ""
	}
	cpuPct = readCPUPercent()
	memPct, memUsed, memTotal = readMemory()
	load = readLoad1()
	return
}

// readCPUPercent diffs /proc/stat against the previous sample.
func readCPUPercent() int {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0
	}
	line := ""
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "cpu ") {
			line = l
			break
		}
	}
	if line == "" {
		return 0
	}
	fields := strings.Fields(line)[1:] // user nice system idle iowait irq softirq steal …
	var total, idle uint64
	for i, f := range fields {
		v, _ := strconv.ParseUint(f, 10, 64)
		total += v
		if i == 3 || i == 4 { // idle + iowait
			idle += v
		}
	}

	cpuMu.Lock()
	defer cpuMu.Unlock()
	prev := lastCPU
	lastCPU = cpuSample{idle: idle, total: total, at: time.Now()}

	if prev.total == 0 || total <= prev.total {
		return 0 // first sample, or counter wrapped
	}
	dTotal := total - prev.total
	dIdle := idle - prev.idle
	if dTotal == 0 {
		return 0
	}
	pct := int(float64(dTotal-dIdle) / float64(dTotal) * 100.0)
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return pct
}

// readMemory parses /proc/meminfo. "used" excludes buffers/cache (MemAvailable),
// which is what a human means by "memory in use".
func readMemory() (pct int, used, total int64) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, 0
	}
	var memTotalKB, memAvailKB int64
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		kb, _ := strconv.ParseInt(f[1], 10, 64)
		switch f[0] {
		case "MemTotal:":
			memTotalKB = kb
		case "MemAvailable:":
			memAvailKB = kb
		}
	}
	if memTotalKB == 0 {
		return 0, 0, 0
	}
	usedKB := memTotalKB - memAvailKB
	total = memTotalKB * 1024
	used = usedKB * 1024
	pct = int(float64(usedKB) / float64(memTotalKB) * 100.0)
	return pct, used, total
}

func readLoad1() string {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return ""
	}
	if f := strings.Fields(string(b)); len(f) > 0 {
		return f[0]
	}
	return ""
}
