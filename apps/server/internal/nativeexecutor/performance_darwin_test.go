//go:build darwin

package nativeexecutor

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/workspace"
)

func TestNativeLocalCheckpointMeasurements(t *testing.T) {
	output := os.Getenv("CAMP_MAC_BENCHMARK_OUTPUT")
	if output == "" {
		t.Skip("set CAMP_MAC_BENCHMARK_OUTPUT for retained native measurements")
	}
	type observation struct {
		Workload     string  `json:"workload"`
		Iteration    int     `json:"iteration"`
		Files        int     `json:"files"`
		PrepareMS    float64 `json:"prepareMs"`
		CaptureMS    float64 `json:"captureMs"`
		ArchiveBytes int64   `json:"archiveBytes"`
		RSSKiB       int     `json:"rssKiB"`
		HeapBytes    uint64  `json:"heapBytes"`
	}
	report := struct {
		Schema        int           `json:"schema"`
		Platform      string        `json:"platform"`
		ModelCalled   bool          `json:"modelCalled"`
		LinuxRequired bool          `json:"linuxRequired"`
		Observations  []observation `json:"observations"`
	}{Schema: 1, Platform: runtime.GOOS + "/" + runtime.GOARCH}
	for _, spec := range []struct {
		name        string
		files, size int
	}{{"text", 6, 128}, {"10000-files", 10000, 128}, {"128-MiB-binary", 16, 8 << 20}} {
		provider, ctx, root := nativeProvider(t)
		source := filepath.Join(root, "source")
		if err := os.Mkdir(source, 0700); err != nil {
			t.Fatal(err)
		}
		block, err := aes.NewCipher(bytes.Repeat([]byte{0x43}, 32))
		if err != nil {
			t.Fatal(err)
		}
		for index := 0; index < spec.files; index++ {
			data := make([]byte, spec.size)
			if spec.name == "text" {
				for i := range data {
					data[i] = byte(65 + index)
				}
			} else {
				iv := make([]byte, aes.BlockSize)
				binary.BigEndian.PutUint32(iv[12:], uint32(index))
				cipher.NewCTR(block, iv).XORKeyStream(data, data)
			}
			if len(data) > 8 {
				binary.LittleEndian.PutUint32(data, uint32(index))
			}
			mode := os.FileMode(0644)
			if index == 3 {
				mode = 0755
			}
			if err := os.WriteFile(filepath.Join(source, fmt.Sprintf("file-%05d.bin", index)), data, mode); err != nil {
				t.Fatal(err)
			}
		}
		for iteration := 0; iteration < 3; iteration++ {
			runtime.GC()
			start := time.Now()
			active, err := provider.Prepare(ctx, workspace.Spec{Source: source})
			prepare := time.Since(start)
			if err != nil {
				t.Fatal(err)
			}
			work := active.Directory()
			if err := os.WriteFile(filepath.Join(work, "file-00000.bin"), []byte("changed binary\x00\xff"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(work, "file-00001.bin")); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(filepath.Join(work, "file-00002.bin"), filepath.Join(work, "renamed.bin")); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(filepath.Join(work, "file-00003.bin"), 0644); err != nil {
				t.Fatal(err)
			}
			start = time.Now()
			archive, err := active.Capture(ctx, filepath.Join(root, fmt.Sprintf("capture-%d.tar", iteration)), "native measurement", nil)
			capture := time.Since(start)
			if err != nil {
				t.Fatal(err)
			}
			var memory runtime.MemStats
			runtime.ReadMemStats(&memory)
			rss, _ := exec.Command("/bin/ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid())).Output()
			rssKiB, _ := strconv.Atoi(strings.TrimSpace(string(rss)))
			report.Observations = append(report.Observations, observation{Workload: spec.name, Iteration: iteration, Files: spec.files - 1, PrepareMS: float64(prepare.Microseconds()) / 1000, CaptureMS: float64(capture.Microseconds()) / 1000, ArchiveBytes: archive.Bytes, RSSKiB: rssKiB, HeapBytes: memory.HeapAlloc})
			if err := active.Close(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(output), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}
