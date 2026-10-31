package metrics

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func proc(t *testing.T, stat, mem, load string) string {
	t.Helper()
	d := t.TempDir()
	for name, body := range map[string]string{"stat": stat, "meminfo": mem, "loadavg": load} {
		if err := os.WriteFile(filepath.Join(d, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func TestSampleReadsCPUMemoryAndLoad(t *testing.T) {
	d := proc(t, "cpu  100 0 100 700 100 0 0 0\ncpu0 1 1 1 1 1 0 0 0\n",
		"MemTotal:        1000 kB\nMemFree: 100 kB\nMemAvailable:     250 kB\n", "0.52 0.40 0.30 1/200 99\n")
	c := New(5)
	c.Proc, c.diskUsage = d, func(string) (uint64, uint64, error) { return 40, 100, nil }
	s, err := c.Take()
	if err != nil {
		t.Fatal(err)
	}
	if s.CPU != 0 || s.MemTotal != 1000*1024 || s.MemUsed != 750*1024 || s.Load1 != 0.52 || s.DiskUsed != 40 {
		t.Errorf("first sample: %+v", s)
	}
	// 200 more busy ticks out of 400 in total: half the CPU.
	os.WriteFile(filepath.Join(d, "stat"), []byte("cpu  200 0 200 800 200 0 0 0\n"), 0o644)
	if s, _ = c.Take(); s.CPU < 49.9 || s.CPU > 50.1 {
		t.Errorf("cpu = %v, want 50", s.CPU)
	}
}

func TestHistoryKeepsTheNewestOnly(t *testing.T) {
	d := proc(t, "cpu  1 0 1 1 1 0 0 0\n", "MemTotal: 10 kB\nMemAvailable: 5 kB\n", "0 0 0 1/1 1\n")
	c := New(3)
	c.Proc, c.diskUsage = d, func(string) (uint64, uint64, error) { return 0, 0, nil }
	now := time.Unix(1000, 0)
	c.Now = func() time.Time { now = now.Add(time.Minute); return now }
	for i := 0; i < 5; i++ {
		c.Take()
	}
	h := c.History()
	if len(h) != 3 || !h[0].At.Before(h[2].At) {
		t.Fatalf("history = %d entries", len(h))
	}
	if last, ok := c.Last(); !ok || !last.At.Equal(h[2].At) {
		t.Error("Last is not the newest sample")
	}
}

func TestMissingProcIsAnError(t *testing.T) {
	c := New(3)
	c.Proc = filepath.Join(t.TempDir(), "nope")
	if _, err := c.Take(); err == nil {
		t.Error("a system without /proc must report an error, not zeros")
	}
}
