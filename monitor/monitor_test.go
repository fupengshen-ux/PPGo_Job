package monitor

import (
	"github.com/george518/PPGo_Job/common"
	"github.com/george518/PPGo_Job/models"
	"testing"
)

func TestMonitorThresholdAndRecovery(t *testing.T) {
	c := DefaultSettings()
	var s AlertState
	high := 95.0
	low := 40.0
	for at := int64(1000); at < 1300; at += 60 {
		var event string
		s, event = Evaluate(s, "cpu", &high, true, c, at)
		if event != "" {
			t.Fatal("five samples are only four minutes")
		}
	}
	var event string
	s, event = Evaluate(s, "cpu", &high, true, c, 1300)
	if event != "firing" || !s.Active {
		t.Fatal(s, event)
	}
	s, event = Evaluate(s, "cpu", &high, true, c, 1360)
	if event != "" {
		t.Fatal("duplicate firing")
	}
	s, event = Evaluate(s, "cpu", nil, false, c, 1420)
	if event != "" || !s.Active {
		t.Fatal("missing sample resolved alarm")
	}
	s, event = Evaluate(s, "cpu", &low, true, c, 1480)
	if event != "" {
		t.Fatal("premature recovery")
	}
	s, event = Evaluate(s, "cpu", &low, true, c, 1540)
	if event != "resolved" || s.Active {
		t.Fatal("missing recovery")
	}
	s = AlertState{}
	s, _ = Evaluate(s, "cpu", &high, true, c, 1000)
	s, event = Evaluate(s, "cpu", &high, true, c, 2000)
	if event != "" || s.PendingSince != 2000 {
		t.Fatal("gap counted toward duration")
	}
}
func TestMonitorAvailability(t *testing.T) {
	c := DefaultSettings()
	var s AlertState
	var event string
	for at := int64(1000); at < 1300; at += 60 {
		s, event = Evaluate(s, "availability", nil, false, c, at)
		if event != "" {
			t.Fatal("unregistered agent alarm")
		}
	}
	s, _ = Evaluate(s, "availability", nil, true, c, 1300)
	for _, at := range []int64{1360, 1420} {
		s, event = Evaluate(s, "availability", nil, false, c, at)
		if event != "" {
			t.Fatal("premature failure")
		}
	}
	s, event = Evaluate(s, "availability", nil, false, c, 1480)
	if event != "firing" {
		t.Fatal(s, event)
	}
	s, event = Evaluate(s, "availability", nil, true, c, 1540)
	if event != "" {
		t.Fatal("premature availability recovery")
	}
	_, event = Evaluate(s, "availability", nil, true, c, 1600)
	if event != "resolved" {
		t.Fatal(event)
	}
}
func TestMonitorMetricsAndCounts(t *testing.T) {
	m := common.HostMetrics{Version: 1, BootID: "a", CPUCores: 4, CPUTotal: 200, CPUIdle: 50, CPUIOWait: 20, MemoryTotal: 100, MemoryAvailable: 20, DiskTotal: 200, DiskAvailable: 50, Load1: 8}
	p := previous{Metrics: common.HostMetrics{BootID: "a", CPUTotal: 100, CPUIdle: 20, CPUIOWait: 10}, At: 1000, Address: "host"}
	s := metricSnapshot(1, m, p, "host", 1060)
	if s.CPU == nil || *s.CPU != 60 || s.Memory == nil || *s.Memory != 80 || *s.LoadPerCore != 2 || *s.DiskFree != 25 {
		t.Fatalf("bad metrics: %+v", s)
	}
	m.BootID = "reboot"
	if metricSnapshot(1, m, p, "host", 1060).CPU != nil {
		t.Fatal("reboot used old baseline")
	}
	m.BootID = "a"
	if metricSnapshot(1, m, p, "host", 1200).CPU != nil {
		t.Fatal("stale baseline used")
	}
	counts := CountAssignments([]*models.Task{{ServerIds: "1, 1,11,0", Status: 1}, {ServerIds: "1,11", Status: 0}, {ServerIds: "1", Status: -1}, {ServerIds: "11", Status: 2}})
	if counts[1] != [2]int{2, 1} || counts[11] != [2]int{3, 1} || counts[0] != [2]int{1, 1} {
		t.Fatal(counts)
	}
	bad := DefaultSettings()
	bad.CPU.Seconds = 0
	if bad.Validate() == nil {
		t.Fatal("invalid duration accepted")
	}
}
