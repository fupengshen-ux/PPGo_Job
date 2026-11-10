package common

// HostMetrics is a read-only, versioned Agent response. Counters are cumulative;
// the controller computes CPU usage between successful samples.
type HostMetrics struct {
	Version                  int
	BootID                   string
	CPUCores                 int
	CPUTotal                 uint64
	CPUIdle                  uint64
	CPUIOWait                uint64
	MemoryTotal              uint64
	MemoryAvailable          uint64
	Load1, Load5, Load15     float64
	DiskPath                 string
	DiskTotal, DiskAvailable uint64
	Errors                   []string
}
