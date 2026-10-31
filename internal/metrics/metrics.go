// Package metrics samples the machine the panel runs on: CPU, memory, load and
// the disk the game servers live on. It reads /proc, so on other systems the
// sample comes back with an error and the page says so.
package metrics

import (
	"bufio"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Sample struct {
	At                  time.Time
	CPU                 float64 // percent of all cores since the previous sample
	Load1               float64
	MemUsed, MemTotal   uint64
	DiskUsed, DiskTotal uint64
}

type Collector struct {
	Proc string // /proc, replaceable in tests
	Disk string // path whose filesystem is reported

	mu        sync.Mutex
	prevBusy  uint64
	prevTotal uint64
	history   []Sample
	keep      int
	Now       func() time.Time
	diskUsage func(path string) (used, total uint64, err error)
}

func New(keep int) *Collector {
	return &Collector{Proc: "/proc", Disk: "/", keep: keep, Now: time.Now, diskUsage: diskUsage}
}

// Take reads one sample and adds it to the history. The first CPU value is 0:
// there is nothing to compare with yet.
func (c *Collector) Take() (Sample, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := Sample{At: c.Now()}
	busy, total, err := readCPU(c.Proc + "/stat")
	if err != nil {
		return s, err
	}
	if c.prevTotal != 0 && total > c.prevTotal {
		s.CPU = 100 * float64(busy-c.prevBusy) / float64(total-c.prevTotal)
	}
	c.prevBusy, c.prevTotal = busy, total
	if s.MemTotal, s.MemUsed, err = readMem(c.Proc + "/meminfo"); err != nil {
		return s, err
	}
	if b, err := os.ReadFile(c.Proc + "/loadavg"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			s.Load1, _ = strconv.ParseFloat(f[0], 64)
		}
	}
	if used, tot, err := c.diskUsage(c.Disk); err == nil {
		s.DiskUsed, s.DiskTotal = used, tot
	}
	c.history = append(c.history, s)
	if len(c.history) > c.keep {
		c.history = c.history[len(c.history)-c.keep:]
	}
	return s, nil
}

// History returns the kept samples, oldest first.
func (c *Collector) History() []Sample {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Sample(nil), c.history...)
}

func (c *Collector) Last() (Sample, bool) {
	h := c.History()
	if len(h) == 0 {
		return Sample{}, false
	}
	return h[len(h)-1], true
}

func readCPU(path string) (busy, total uint64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return 0, 0, errors.New("empty " + path)
	}
	fields := strings.Fields(sc.Text())
	if len(fields) < 6 || fields[0] != "cpu" {
		return 0, 0, errors.New("unexpected " + path)
	}
	var v []uint64
	for _, x := range fields[1:] {
		n, err := strconv.ParseUint(x, 10, 64)
		if err != nil {
			return 0, 0, err
		}
		v = append(v, n)
		total += n
	}
	idle := v[3] + v[4] // idle and iowait
	return total - idle, total, nil
}

func readMem(path string) (total, used uint64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	var avail uint64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		n, _ := strconv.ParseUint(strings.Fields(rest + " 0")[0], 10, 64)
		switch k {
		case "MemTotal":
			total = n * 1024
		case "MemAvailable":
			avail = n * 1024
		}
	}
	if total == 0 {
		return 0, 0, errors.New("no MemTotal in " + path)
	}
	return total, total - avail, nil
}

// Run samples every interval until stop is closed.
func (c *Collector) Run(stop <-chan struct{}, every time.Duration) {
	c.Take()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			c.Take()
		}
	}
}
