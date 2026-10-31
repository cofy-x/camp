//go:build darwin

package nativeexecutor

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/workspace"
)

// This is the actual native-local production publication path. Its archive is
// deliberately distinct from the complete-tree capture/restore comparison.
func TestNativeLocalComparisonMeasurements(t *testing.T) {
	plan, output := os.Getenv("CAMP_MAC_COMPARISON_PLAN"), os.Getenv("CAMP_MAC_COMPARISON_OUTPUT")
	if plan == "" || output == "" {
		t.Skip("set explicit private Mac comparison plan and output")
	}
	type fixture struct {
		Workload  string `json:"workload"`
		Iteration int    `json:"iteration"`
		Phase     string `json:"phase"`
		Source    string `json:"source"`
		Digest    string `json:"digest"`
	}
	var input struct {
		Schema   int       `json:"schema"`
		Fixtures []fixture `json:"fixtures"`
	}
	data, err := os.ReadFile(plan)
	if err != nil || json.Unmarshal(data, &input) != nil || input.Schema != 1 || len(input.Fixtures) == 0 {
		t.Fatal("invalid retained comparison plan", err)
	}
	type observation struct {
		Fixture                    fixture `json:"fixture"`
		PrepareMS                  float64 `json:"prepareMs"`
		CaptureMS                  float64 `json:"captureMs"`
		ArchiveBytes               int64   `json:"archiveBytes"`
		ArchiveDigest              string  `json:"archiveDigest,omitempty"`
		CPUUserUsec                int64   `json:"cpuUserUsec"`
		CPUSystemUsec              int64   `json:"cpuSystemUsec"`
		RSSKiB                     int     `json:"rssKiB"`
		ProcessLifetimeMaxRSSBytes int64   `json:"processLifetimeMaxRSSBytes"`
		Error                      string  `json:"error,omitempty"`
		Unsupported                bool    `json:"unsupported,omitempty"`
	}
	report := struct {
		Schema       int           `json:"schema"`
		Passed       bool          `json:"passed"`
		Scope        string        `json:"scope"`
		Observations []observation `json:"observations"`
	}{Schema: 1, Passed: true, Scope: "actual native-local production Prepare and publication Capture on the identical retained inputs; excludes full-tree restore/fork, measured separately; per-process lifetime RSS peak and self CPU excluding children; existing VM/background load unchanged"}
	for _, item := range input.Fixtures {
		provider, ctx, root := nativeProvider(t)
		row := observation{Fixture: item}
		var before, after syscall.Rusage
		_ = syscall.Getrusage(syscall.RUSAGE_SELF, &before)
		started := time.Now()
		active, err := provider.Prepare(ctx, workspace.Spec{Source: item.Source, AttemptID: fmt.Sprintf("comparison-%d-%s", item.Iteration, item.Phase)})
		row.PrepareMS = float64(time.Since(started).Microseconds()) / 1000
		if err == nil {
			started = time.Now()
			archive, captureErr := active.Capture(ctx, filepath.Join(root, "publication.tar"), "controlled comparison", []string{"node_modules"})
			row.CaptureMS = float64(time.Since(started).Microseconds()) / 1000
			row.ArchiveBytes, row.ArchiveDigest = archive.Bytes, archive.SHA256
			err = captureErr
			if closeErr := active.Close(ctx); closeErr != nil {
				t.Error(closeErr)
			}
		}
		if err != nil {
			row.Error = err.Error()
			row.Unsupported = item.Workload == "dependencies-links"
			if !row.Unsupported {
				report.Passed = false
			}
		}
		_ = syscall.Getrusage(syscall.RUSAGE_SELF, &after)
		row.CPUUserUsec = (after.Utime.Sec-before.Utime.Sec)*1_000_000 + int64(after.Utime.Usec-before.Utime.Usec)
		row.CPUSystemUsec = (after.Stime.Sec-before.Stime.Sec)*1_000_000 + int64(after.Stime.Usec-before.Stime.Usec)
		row.ProcessLifetimeMaxRSSBytes = after.Maxrss
		rss, _ := exec.Command("/bin/ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid())).Output()
		row.RSSKiB, _ = strconv.Atoi(strings.TrimSpace(string(rss)))
		report.Observations = append(report.Observations, row)
		runtime.GC() // Outside each measured phase, as in the shipped baseline probe.
		data, _ := json.MarshalIndent(report, "", "  ")
		if err := os.WriteFile(output, append(data, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if !report.Passed {
		t.Fatal("native-local comparison failed; all samples retained", output)
	}
}
