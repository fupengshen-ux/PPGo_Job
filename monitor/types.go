package monitor

import (
	"fmt"
	"math"
)

const Retention = int64(72 * 3600)

type Rule struct {
	Threshold float64 `json:"threshold"`
	Seconds   int64   `json:"seconds"`
}
type Settings struct {
	Enabled      bool  `json:"enabled"`
	CPU          Rule  `json:"cpu"`
	Memory       Rule  `json:"memory"`
	Disk         Rule  `json:"disk"`
	FailureCount int   `json:"failure_count"`
	RecipientIDs []int `json:"recipient_ids"`
}

func DefaultSettings() Settings {
	return Settings{CPU: Rule{90, 300}, Memory: Rule{90, 180}, Disk: Rule{10, 180}, FailureCount: 3, RecipientIDs: []int{}}
}
func (c Settings) Validate() error {
	for _, r := range []Rule{c.CPU, c.Memory, c.Disk} {
		if math.IsNaN(r.Threshold) || math.IsInf(r.Threshold, 0) || r.Threshold <= 0 || r.Threshold >= 100 || r.Seconds < 60 || r.Seconds > 86400 {
			return fmt.Errorf("阈值须在 0～100 之间，持续时间须为 60～86400 秒")
		}
	}
	if c.FailureCount < 2 || c.FailureCount > 60 {
		return fmt.Errorf("连续失败次数须为 2～60")
	}
	if len(c.RecipientIDs) > 50 || (c.Enabled && len(c.RecipientIDs) == 0) {
		return fmt.Errorf("请选择 1～50 位有效邮件收件人")
	}
	return nil
}

type Snapshot struct {
	ServerID        int      `json:"server_id"`
	Name            string   `json:"name"`
	GroupID         int      `json:"group_id"`
	ConnectionType  int      `json:"connection_type"`
	AlertEnabled    bool     `json:"alert_enabled"`
	CollectedAt     int64    `json:"collected_at"`
	LastSuccess     int64    `json:"last_success"`
	Status          string   `json:"status"`
	Errors          []string `json:"errors"`
	CPUCores        int      `json:"cpu_cores"`
	CPU             *float64 `json:"cpu"`
	Memory          *float64 `json:"memory"`
	MemoryTotal     uint64   `json:"memory_total"`
	MemoryAvailable uint64   `json:"memory_available"`
	Load1           *float64 `json:"load1"`
	Load5           *float64 `json:"load5"`
	Load15          *float64 `json:"load15"`
	LoadPerCore     *float64 `json:"load_per_core"`
	DiskFree        *float64 `json:"disk_free"`
	DiskPath        string   `json:"disk_path"`
	DiskTotal       uint64   `json:"disk_total"`
	DiskAvailable   uint64   `json:"disk_available"`
	Assigned        *int     `json:"assigned"`
	Enabled         *int     `json:"enabled"`
}

type AlertState struct {
	PendingSince int64
	LastEval     int64
	Failures     int
	Recoveries   int
	Seen         bool
	Active       bool
	Incident     string
	StartedAt    int64
}

// Evaluate uses timestamps, not sample counts, for resource duration gates.
// Missing observations never resolve an active resource alarm.
func Evaluate(s AlertState, metric string, value *float64, reachable bool, cfg Settings, now int64) (AlertState, string) {
	if now <= s.LastEval {
		return s, ""
	}
	if now-s.LastEval > 90 {
		s.PendingSince = 0
		s.Failures = 0
		s.Recoveries = 0
	}
	s.LastEval = now
	bad := false
	seconds := int64(0)
	if metric == "availability" {
		if reachable {
			s.Seen = true
			s.Failures = 0
		} else {
			s.Failures++
		}
		if !s.Seen {
			return s, ""
		}
		bad = !reachable
	} else {
		if value == nil {
			s.PendingSince = 0
			s.Recoveries = 0
			return s, ""
		}
		r := cfg.CPU
		if metric == "memory" {
			r = cfg.Memory
		}
		if metric == "disk" {
			r = cfg.Disk
		}
		seconds = r.Seconds
		bad = *value >= r.Threshold
		if metric == "disk" {
			bad = *value < r.Threshold
		}
	}
	if bad {
		s.Recoveries = 0
		if s.PendingSince == 0 {
			s.PendingSince = now
		}
		ready := now-s.PendingSince >= seconds
		if metric == "availability" {
			ready = s.Failures >= cfg.FailureCount
		}
		if !s.Active && ready {
			s.Active = true
			s.StartedAt = s.PendingSince
			return s, "firing"
		}
	} else {
		s.PendingSince = 0
		if s.Active {
			s.Recoveries++
			if s.Recoveries >= 2 {
				s.Active = false
				s.Recoveries = 0
				return s, "resolved"
			}
		}
	}
	return s, ""
}
