// Package sensors reads system health metrics: CPU/GPU temperatures,
// load, memory, disk, and fan status (when available).
package sensors

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Snapshot holds a point-in-time reading of system health.
type Snapshot struct {
	Time    time.Time `json:"time"`
	Uptime  string    `json:"uptime"`
	Load    Load      `json:"load"`
	CPU     CPUTemp   `json:"cpu"`
	GPU     GPUTemp   `json:"gpu"`
	Memory  MemInfo   `json:"memory"`
	Disk    DiskInfo  `json:"disk"`
}

// Load holds the three load averages.
type Load struct {
	Avg1  float64 `json:"avg1"`
	Avg5  float64 `json:"avg5"`
	Avg15 float64 `json:"avg15"`
}

// CPUTemp holds CPU temperature readings.
type CPUTemp struct {
	Package float64 `json:"package"`
	Core0   float64 `json:"core0"`
	Core1   float64 `json:"core1"`
	Core2   float64 `json:"core2"`
	Core3   float64 `json:"core3"`
	Max     float64 `json:"max"`
}

// GPUTemp holds GPU temperature and utilization.
type GPUTemp struct {
	Temp        float64 `json:"temp"`
	Utilization int     `json:"utilization_pct"`
	MemoryUsed  int64   `json:"memory_used_mb"`
	MemoryTotal int64   `json:"memory_total_mb"`
	FanSpeed    int     `json:"fan_speed_pct"`
}

// MemInfo holds system memory statistics.
type MemInfo struct {
	Total     int64 `json:"total_mb"`
	Available int64 `json:"available_mb"`
	Used      int64 `json:"used_mb"`
}

// DiskInfo holds disk usage for a mount point.
type DiskInfo struct {
	Mount string `json:"mount"`
	Total int64  `json:"total_gb"`
	Used  int64  `json:"used_gb"`
	Free  int64  `json:"free_gb"`
}

// Collect gathers all sensor readings into a Snapshot. It never fails —
// missing sensors return zero values rather than errors.
func Collect() Snapshot {
	s := Snapshot{Time: time.Now()}
	s.Uptime = readFileFirstLine("/proc/uptime")
	s.Load = readLoad()
	s.CPU = readCPUTemp()
	s.GPU = readGPUTemp()
	s.Memory = readMemory()
	s.Disk = readDisk("/")
	return s
}

func readLoad() Load {
	raw, _ := os.ReadFile("/proc/loadavg")
	parts := strings.Fields(string(raw))
	l := Load{}
	if len(parts) >= 3 {
		l.Avg1, _ = strconv.ParseFloat(parts[0], 64)
		l.Avg5, _ = strconv.ParseFloat(parts[1], 64)
		l.Avg15, _ = strconv.ParseFloat(parts[2], 64)
	}
	return l
}

func readCPUTemp() CPUTemp {
	c := CPUTemp{}
	// Read from hwmon sensors. Paths vary by kernel/motherboard.
	// The most common pattern: /sys/class/hwmon/hwmon*/temp*_input
	dirs, _ := os.ReadDir("/sys/class/hwmon")
	for _, d := range dirs {
		entries, _ := os.ReadDir("/sys/class/hwmon/" + d.Name())
		for _, e := range entries {
			raw, err := os.ReadFile("/sys/class/hwmon/" + d.Name() + "/" + e.Name())
			if err != nil {
				continue
			}
			val := parseMilliC(string(raw))
			name := e.Name()
			switch {
			case strings.Contains(name, "temp1_input"):
				c.Package = val
			case strings.Contains(name, "temp2_input"):
				c.Core0 = val
			case strings.Contains(name, "temp3_input"):
				c.Core1 = val
			case strings.Contains(name, "temp4_input"):
				c.Core2 = val
			case strings.Contains(name, "temp5_input"):
				c.Core3 = val
			}
		}
	}
	if c.Package > 0 { c.Max = c.Package }
	if c.Core0 > c.Max { c.Max = c.Core0 }
	if c.Core1 > c.Max { c.Max = c.Core1 }
	if c.Core2 > c.Max { c.Max = c.Core2 }
	if c.Core3 > c.Max { c.Max = c.Core3 }
	return c
}

func readGPUTemp() GPUTemp {
	g := GPUTemp{}
	out, err := exec.Command("nvidia-smi",
		"--query-gpu=temperature.gpu,utilization.gpu,memory.used,memory.total,fan.speed",
		"--format=csv,noheader,nounits",
	).Output()
	if err != nil {
		return g
	}
	parts := strings.Split(strings.TrimSpace(string(out)), ",")
	if len(parts) >= 5 {
		g.Temp, _ = strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
		g.Utilization, _ = strconv.Atoi(strings.TrimSpace(parts[1]))
		g.MemoryUsed, _ = strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64)
		g.MemoryTotal, _ = strconv.ParseInt(strings.TrimSpace(parts[3]), 10, 64)
		g.FanSpeed, _ = strconv.Atoi(strings.TrimSpace(parts[4]))
	}
	return g
}

func readMemory() MemInfo {
	m := MemInfo{}
	raw, _ := os.ReadFile("/proc/meminfo")
	for _, line := range strings.Split(string(raw), "\n") {
		switch {
		case strings.HasPrefix(line, "MemTotal:"):
			m.Total = parseKB(line) / 1024
		case strings.HasPrefix(line, "MemAvailable:"):
			m.Available = parseKB(line) / 1024
		}
	}
	m.Used = m.Total - m.Available
	return m
}

func readDisk(mount string) DiskInfo {
	d := DiskInfo{Mount: mount}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(mount, &stat); err == nil {
		d.Total = int64(stat.Blocks*uint64(stat.Bsize)) / 1e9
		d.Free  = int64(stat.Bavail*uint64(stat.Bsize)) / 1e9
		d.Used  = d.Total - d.Free
	}
	return d
}

func readFileFirstLine(path string) string {
	raw, _ := os.ReadFile(path)
	line, _, _ := strings.Cut(string(raw), "\n")
	return line
}

func parseMilliC(raw string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	return v / 1000.0
}

func parseKB(line string) int64 {
	parts := strings.Fields(line)
	if len(parts) >= 2 {
		v, _ := strconv.ParseInt(parts[1], 10, 64)
		return v
	}
	return 0
}
