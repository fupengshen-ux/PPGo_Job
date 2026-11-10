package hostmetrics

import (
	"fmt"
	"strconv"
	"strings"
)

func parseCPU(data string) (total, idle, wait uint64, cores int, err error) {
	for _, line := range strings.Split(data, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if f[0] == "cpu" {
			if len(f) < 5 {
				return 0, 0, 0, 0, fmt.Errorf("invalid CPU counters")
			}
			// guest/guest_nice are already included in user/nice.
			for i := 1; i < len(f) && i <= 8; i++ {
				n, e := strconv.ParseUint(f[i], 10, 64)
				if e != nil {
					return 0, 0, 0, 0, e
				}
				total += n
				if i == 4 {
					idle = n
				}
				if i == 5 {
					wait = n
				}
			}
		} else if strings.HasPrefix(f[0], "cpu") {
			if _, e := strconv.Atoi(strings.TrimPrefix(f[0], "cpu")); e == nil {
				cores++
			}
		}
	}
	if total == 0 || cores == 0 {
		err = fmt.Errorf("missing CPU counters")
	}
	return
}

func parseMemory(data string) (total, available uint64, err error) {
	found := false
	for _, line := range strings.Split(data, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || (f[0] != "MemTotal:" && f[0] != "MemAvailable:") {
			continue
		}
		n, e := strconv.ParseUint(f[1], 10, 64)
		if e != nil {
			return 0, 0, e
		}
		if f[0] == "MemTotal:" {
			total = n * 1024
		} else {
			available = n * 1024
			found = true
		}
	}
	if !found || total == 0 || available > total {
		err = fmt.Errorf("invalid memory counters")
	}
	return
}
