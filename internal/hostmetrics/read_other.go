//go:build !linux

package hostmetrics

import (
	"fmt"
	"github.com/george518/PPGo_Job/common"
)

func Read(_ string) (common.HostMetrics, error) {
	return common.HostMetrics{}, fmt.Errorf("host metrics: unsupported OS (Linux required)")
}
