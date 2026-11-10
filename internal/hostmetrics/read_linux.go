package hostmetrics

import (
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/george518/PPGo_Job/common"
)

func Read(path string) (common.HostMetrics, error) {
	m := common.HostMetrics{Version: 1, DiskPath: path}
	if m.DiskPath == "" {
		m.DiskPath = "/"
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err == nil {
		m.BootID = strings.TrimSpace(string(boot))
	}
	stat, err := os.ReadFile("/proc/stat")
	if err == nil {
		m.CPUTotal, m.CPUIdle, m.CPUIOWait, m.CPUCores, err = parseCPU(string(stat))
	}
	if err != nil || m.BootID == "" {
		m.CPUTotal = 0
		m.Errors = append(m.Errors, "cpu")
	}
	mem, err := os.ReadFile("/proc/meminfo")
	if err == nil {
		m.MemoryTotal, m.MemoryAvailable, err = parseMemory(string(mem))
	}
	if err != nil {
		m.MemoryTotal = 0
		m.Errors = append(m.Errors, "memory")
	}
	load, err := os.ReadFile("/proc/loadavg")
	if err == nil {
		_, err = fmt.Sscan(string(load), &m.Load1, &m.Load5, &m.Load15)
	}
	if err != nil {
		m.Errors = append(m.Errors, "load")
	}
	var disk syscall.Statfs_t
	if err = syscall.Statfs(m.DiskPath, &disk); err == nil && disk.Blocks > 0 {
		m.DiskTotal = disk.Blocks * uint64(disk.Bsize)
		m.DiskAvailable = disk.Bavail * uint64(disk.Bsize)
	} else {
		m.Errors = append(m.Errors, "disk")
	}
	return m, nil
}
