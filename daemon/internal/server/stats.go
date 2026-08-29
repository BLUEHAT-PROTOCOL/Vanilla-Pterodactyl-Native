package server

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Stats is the wings-compatible stats payload.
type Stats struct {
	Uptime  int64         `json:"uptime"`
	Memory  MemoryStats   `json:"memory"`
	CPU     CPUStats      `json:"cpu"`
	Disk    DiskStats     `json:"disk"`
}

// MemoryStats — current usage vs limit.
type MemoryStats struct {
	Current int64 `json:"current"`
	Limit   int64 `json:"limit"`
}

// CPUStats — percents (absolute 0-100).
type CPUStats struct {
	Absolute float64 `json:"absolute"`
	Used     float64 `json:"used"`
	Limit    float64 `json:"limit"`
}

// DiskStats — current usage vs limit.
type DiskStats struct {
	Current int64 `json:"current"`
	Limit   int64 `json:"limit"`
}

// procCPUSample holds raw jiffies for a process group.
type procCPUSample struct {
	utime, stime uint64
	at           time.Time
}

// CollectStats samples process-group resources and returns wings-style stats.
func (s *Server) CollectStats(q Quota) *Stats {
	s.mu.RLock()
	pid := s.pid
	memLimit := s.Cfg.Settings.Build.MemoryLimit * 1024 * 1024
	cpuLimit := s.Cfg.Settings.Build.CpuLimit
	diskLimit := s.Cfg.Settings.Build.DiskSpace * 1024 * 1024
	s.mu.RUnlock()

	if pid == 0 {
		return nil
	}

	uptime := s.Uptime()

	// cpu + memory across the process group
	var memBytes int64
	var cpuPercent float64

	pids := groupPids(pid)
	cur := sampleCPU(pids)
	for _, p := range pids {
		memBytes += rssOf(p)
	}

	s.mu.RLock()
	prev := s.cpuSample
	s.mu.RUnlock()

	if prev != nil && cur != nil {
		dU := float64(cur.utime - prev.utime)
		dS := float64(cur.stime - prev.stime)
		dT := cur.at.Sub(prev.at).Seconds()
		if dT > 0 {
			hz := 100.0 // CLK_TCK on Linux
			totalJiffies := dU + dS
			cpuPercent = (totalJiffies / hz) / dT * 100.0
		}
	}
	s.mu.Lock()
	s.cpuSample = cur
	s.mu.Unlock()

	if cpuPercent < 0 {
		cpuPercent = 0
	}
	if memBytes < 0 {
		memBytes = 0
	}

	// disk
	var diskUsed int64
	if q != nil {
		diskUsed = q.UsedBytes(s)
	}

	return &Stats{
		Uptime: uptime,
		Memory: MemoryStats{Current: memBytes, Limit: memLimit},
		CPU:    CPUStats{Absolute: round2(cpuPercent), Used: round2(cpuPercent), Limit: float64(cpuLimit)},
		Disk:   DiskStats{Current: diskUsed, Limit: diskLimit},
	}
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}

// groupPids returns all pids whose process group equals pgid.
func groupPids(pgid int) []int {
	var out []int
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if pgrpOf(pid) == pgid {
			out = append(out, pid)
		}
	}
	return out
}

// pgrpOf reads field 5 (pgrp) of /proc/<pid>/stat.
func pgrpOf(pid int) int {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return -1
	}
	return statField(string(data), 5)
}

// rssOf returns VmRSS in bytes from /proc/<pid>/status.
func rssOf(pid int) int64 {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, _ := strconv.ParseInt(fields[1], 10, 64)
				return kb * 1024
			}
		}
	}
	return 0
}

// sampleCPU sums utime+stime jiffies of the given pids.
func sampleCPU(pids []int) *procCPUSample {
	s := &procCPUSample{at: time.Now()}
	for _, pid := range pids {
		data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if err != nil {
			continue
		}
		s.utime += uint64(statField(string(data), 14))
		s.stime += uint64(statField(string(data), 15))
	}
	return s
}

// statField extracts the n-th (1-based) field of a /proc stat line,
// handling the parenthesized comm field correctly.
func statField(line string, n int) int {
	open := strings.IndexByte(line, '(')
	closeIdx := strings.LastIndexByte(line, ')')
	if open < 0 || closeIdx < 0 || closeIdx <= open {
		return -1
	}
	rest := strings.Fields(line[closeIdx+1:])
	// fields after comm start at index 2 in the original; rest[0] = field 3
	idx := n - 3
	if idx < 0 || idx >= len(rest) {
		return -1
	}
	v, err := strconv.Atoi(rest[idx])
	if err != nil {
		return -1
	}
	return v
}
