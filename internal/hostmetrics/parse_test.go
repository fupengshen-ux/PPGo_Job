package hostmetrics

import "testing"

func TestHostMetricParsing(t *testing.T) {
	total, idle, wait, cores, err := parseCPU("cpu 10 20 30 40 50 60 70 80 90 100\ncpu0 1 2 3 4\ncpu1 1 2 3 4\n")
	if err != nil || total != 360 || idle != 40 || wait != 50 || cores != 2 {
		t.Fatal(total, idle, wait, cores, err)
	}
	if _, _, _, _, err = parseCPU("cpu 1 bad 3 4\ncpu0 1"); err == nil {
		t.Fatal("accepted invalid counter")
	}
	total, available, err := parseMemory("MemTotal: 100 kB\nMemFree: 1 kB\nMemAvailable: 40 kB\n")
	if err != nil || total != 102400 || available != 40960 {
		t.Fatal(total, available, err)
	}
	if _, _, err = parseMemory("MemTotal: 100 kB\nMemFree: 1 kB"); err == nil {
		t.Fatal("MemFree must not replace MemAvailable")
	}
	if _, _, err = parseMemory("MemTotal: 100 kB\nMemAvailable: 200 kB"); err == nil {
		t.Fatal("accepted invalid memory")
	}
}
