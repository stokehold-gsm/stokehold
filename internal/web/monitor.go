package web

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/stokehold-gsm/stokehold/internal/metrics"
)

type monView struct {
	Have              bool
	CPU, Mem, Disk    float64
	Load1             float64
	MemText, DiskText string
	CPUPoints         string
	MemPoints         string
}

func gb(b uint64) string { return fmt.Sprintf("%.1f", float64(b)/(1<<30)) }

func pct(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return 100 * float64(used) / float64(total)
}

func points(vals []float64) string {
	if len(vals) < 2 {
		return ""
	}
	var b strings.Builder
	for i, v := range vals {
		if v > 100 {
			v = 100
		}
		fmt.Fprintf(&b, "%.1f,%.1f ", 120*float64(i)/float64(len(vals)-1), 30-v*0.3)
	}
	return strings.TrimSpace(b.String())
}

func monitorView(c *metrics.Collector) monView {
	if c == nil {
		return monView{}
	}
	h := c.History()
	if len(h) == 0 {
		return monView{}
	}
	last := h[len(h)-1]
	v := monView{Have: true, CPU: last.CPU, Load1: last.Load1,
		Mem: pct(last.MemUsed, last.MemTotal), Disk: pct(last.DiskUsed, last.DiskTotal),
		MemText: gb(last.MemUsed) + " / " + gb(last.MemTotal) + " GB", DiskText: gb(last.DiskUsed) + " / " + gb(last.DiskTotal) + " GB"}
	var cpu, mem []float64
	for _, x := range h {
		cpu = append(cpu, x.CPU)
		mem = append(mem, pct(x.MemUsed, x.MemTotal))
	}
	v.CPUPoints, v.MemPoints = points(cpu), points(mem)
	return v
}

func (s *Server) monitorPage(w http.ResponseWriter, r *http.Request, p *page) {
	p.Mon = monitorView(s.Metrics)
	p.Servers, _ = s.Store.Servers()
	p.Statuses = s.Statuses()
	s.fillNodes(p)
	s.render(w, "monitor", p, http.StatusOK)
}

// Integrations: the Discord webhook and the public status page
