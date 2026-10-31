package web

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// jobStep is one stage of an install as the progress page shows it.
type jobStep struct {
	N      int
	Title  string
	State  string // wait | run | done | fail
	Detail string // what the log says about it
	Note   string // the error, for a failed step
}

var stepLine = regexp.MustCompile(`^\[(\d+)/(\d+)\] (.*?)(?: \.\.\.)?$`)

// jobSteps reads the "[n/m] ..." lines runInstall writes and turns them into
// stages. The raw log stays on the page for anyone who wants the details.
func jobSteps(lang string, j job) []jobStep {
	total := 3
	if m := stepLine.FindStringSubmatch(firstStepLine(j.Log)); m != nil {
		fmt.Sscan(m[2], &total)
	}
	if total < 1 || total > 9 {
		total = 3
	}
	steps := make([]jobStep, total)
	for i := range steps {
		steps[i] = jobStep{N: i + 1, State: "wait", Title: tr(lang, fmt.Sprintf("job_step%d", i+1))}
		if steps[i].Title == "" {
			steps[i].Title = fmt.Sprintf("%d", i+1)
		}
	}
	cur := -1
	for _, line := range j.Log {
		if m := stepLine.FindStringSubmatch(line); m != nil {
			var n int
			fmt.Sscan(m[1], &n)
			if n < 1 || n > total {
				continue
			}
			cur = n - 1
			steps[cur].Detail = m[3]
			steps[cur].State = "run"
			// a step that only reports ("packages: none needed") has no ok line after it
			if strings.HasPrefix(m[3], "packages:") {
				steps[cur].State = "done"
			}
			continue
		}
		if cur < 0 {
			continue
		}
		switch t := strings.TrimSpace(line); {
		case t == "ok":
			steps[cur].State = "done"
		case strings.HasPrefix(t, "failed:"):
			steps[cur].State = "fail"
			steps[cur].Note = strings.TrimSpace(strings.TrimPrefix(t, "failed:"))
		}
	}
	switch j.Status {
	case "done":
		for i := range steps {
			if steps[i].State != "fail" {
				steps[i].State = "done"
			}
		}
	case "failed":
		for i := range steps {
			if steps[i].State == "run" { // the job stopped while this one was going
				steps[i].State = "fail"
			}
		}
	}
	return steps
}

func firstStepLine(log []string) string {
	for _, l := range log {
		if stepLine.MatchString(l) {
			return l
		}
	}
	return ""
}

// jobProgress is the overall percentage: finished steps plus half of the one running.
func jobProgress(steps []jobStep, status string) int {
	if status == "done" {
		return 100
	}
	done, run := 0, 0
	for _, s := range steps {
		switch s.State {
		case "done":
			done++
		case "run":
			run++
		}
	}
	return (done*200 + run*100) / (2 * len(steps))
}

func elapsed(j job, now time.Time) string {
	end := now
	if !j.End.IsZero() {
		end = j.End
	}
	d := end.Sub(j.Start).Round(time.Second)
	if d < 0 {
		d = 0
	}
	if h := int(d.Hours()); h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, int(d.Minutes())%60, int(d.Seconds())%60)
	}
	return fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}

// jobFailure is the line to show in the failure banner: the failed step's
// error, or the last thing the log said.
func jobFailure(steps []jobStep, j job) string {
	for _, s := range steps {
		if s.State == "fail" && s.Note != "" {
			return s.Note
		}
	}
	for i := len(j.Log) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(j.Log[i]); t != "" {
			return t
		}
	}
	return ""
}
