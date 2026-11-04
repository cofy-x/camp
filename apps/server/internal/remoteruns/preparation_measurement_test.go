package remoteruns

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/cofy-x/camp/apps/server/internal/configuration"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/resources"
	"github.com/cofy-x/camp/apps/server/internal/runinput"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

type preparationSample struct {
	Workload       string             `json:"workload"`
	Files          int                `json:"files"`
	ProjectBytes   int                `json:"projectBytes"`
	References     int                `json:"references"`
	TaskInputs     int                `json:"taskInputs"`
	PromptBytes    int                `json:"promptBytes"`
	ArchiveBytes   int64              `json:"archiveBytes"`
	ActiveAttempts int                `json:"activeAttempts"`
	PhasesMS       map[string]float64 `json:"phasesMs"`
}

// TestRemoteInputPreparationMeasurements is an opt-in, source-only probe. It
// uses synthetic data, actual Git/archive/fsync/storage paths and fixture Runner
// registrations. It starts no Runner or model request and changes no team data.
// A new report is required for every invocation; ordinary tests skip the probe.
func TestRemoteInputPreparationMeasurements(t *testing.T) {
	reportPath := os.Getenv("CAMP_TEST_PREPARATION_REPORT")
	if reportPath == "" {
		t.Skip("set CAMP_TEST_PREPARATION_REPORT to a new private report file")
	}
	reportFile, err := os.OpenFile(reportPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	report := struct {
		Version     int                 `json:"version"`
		Platform    string              `json:"platform"`
		Go          string              `json:"go"`
		Git         string              `json:"git"`
		GOMAXPROCS  int                 `json:"gomaxprocs"`
		Source      string              `json:"source"`
		ProbeSHA256 string              `json:"probeSha256"`
		Scope       string              `json:"scope"`
		Complete    bool                `json:"complete"`
		Samples     []preparationSample `json:"samples"`
	}{Version: 1, Platform: runtime.GOOS + "/" + runtime.GOARCH, Go: runtime.Version(), GOMAXPROCS: runtime.GOMAXPROCS(0), Scope: "Warm synthetic fixtures; independent component timings precede full prepare. Reconciliation excludes scratch collection and has an increasing retained-attempt count. No daemon, native runtime, provider call or WSS latency measurement."}
	gitHead, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	report.Source = strings.TrimSpace(string(gitHead))
	gitVersion, err := exec.Command("git", "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	report.Git = strings.TrimSpace(string(gitVersion))
	_, sourceFile, _, _ := runtime.Caller(0)
	probe, err := os.ReadFile(sourceFile)
	if err != nil {
		t.Fatal(err)
	}
	probeHash := sha256.Sum256(probe)
	report.ProbeSHA256 = hex.EncodeToString(probeHash[:])
	t.Cleanup(func() {
		defer reportFile.Close()
		report.Complete = report.Complete && !t.Failed()
		data, err := json.MarshalIndent(report, "", "  ")
		if err == nil {
			_, err = reportFile.Write(append(data, '\n'))
		}
		if err == nil {
			err = reportFile.Sync()
		}
		if err != nil {
			t.Error(err)
		}
	})

	h := newRemoteHarness(t, configuration.ModelInput{})
	m := h.manager.Load()
	// Admission below uses storage directly. Stop the background scheduler so
	// exactly one caller builds each input and no command can be dispatched.
	shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
	err = m.Shutdown(shutdown)
	stop()
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the synchronous methods without a background scheduler, peers or
	// shutdown state. All dependencies still belong to the temporary fixture.
	m = &Manager{store: h.store, files: m.files, models: m.models, config: m.config, logger: m.logger, ctx: h.ctx, lastGC: time.Now()}
	revision, model := m.models.Snapshot()
	budget := storage.Budget{RequireRunner: true, RuntimeKey: revision, Model: model.Model, MaxTokens: model.MaxTokens, MaxSteps: model.MaxSteps, TimeoutSeconds: 180}
	for _, workload := range []struct {
		name                            string
		files, size, references, inputs int
	}{
		{"empty", 0, 0, 0, 0},
		{"small-files", 1000, 1024, 0, 0},
		{"large-file", 1, resources.MaxFileBytes, 0, 0},
		{"references", 100, 4096, 8, 0},
		{"task-inputs", 0, 0, 0, 128},
	} {
		source := t.TempDir()
		for i := range workload.files {
			data := bytes.Repeat([]byte{'x'}, workload.size)
			copy(data, fmt.Appendf(nil, "%08d", i))
			if err := os.WriteFile(filepath.Join(source, fmt.Sprintf("file-%04d.txt", i)), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
		resourceID := newID()
		imported, err := m.files.Import(h.ctx, resourceID, source)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = h.store.CreateResource(h.actor, storage.Resource{ID: resourceID, Name: workload.name, CurrentCommit: imported.Commit}); err != nil {
			t.Fatal(err)
		}
		for sample := range 3 {
			_, enrollment, err := h.store.CreateRunnerEnrollment(h.actor, "Preparation fixture", time.Now().Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			runner, err := h.store.JoinRunner(h.ctx, storage.JoinRunnerInput{Token: enrollment, RegistrationID: newID(), Credential: secret(), StorageID: newID()})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = h.store.ConnectRunner(h.ctx, runner.ID, storage.RunnerConnection{StorageID: runner.StorageID, Runtime: "fixture", Slots: 1, Capabilities: json.RawMessage(`{"executor":"fixture","ready":true,"fingerprint":"preparation-probe"}`)}); err != nil {
				t.Fatal(err)
			}
			room, agent := h.room(t, fmt.Sprintf("%s-%d", workload.name, sample), runner.ID)
			agent.Access = "read"
			if agent, err = h.store.SaveAgent(h.actor, agent); err != nil {
				t.Fatal(err)
			}
			room.ResourceID = resourceID
			if room, err = h.store.UpdateRoom(h.actor, room); err != nil {
				t.Fatal(err)
			}
			var refs []storage.ReferenceInput
			for i := range workload.references {
				refs = append(refs, storage.ReferenceInput{Kind: "file", ResourceID: resourceID, Commit: imported.Commit, Path: fmt.Sprintf("file-%04d.txt", i)})
			}
			var run storage.Run
			if workload.inputs != 0 {
				task, err := h.store.CreateTask(h.actor, room.ID, storage.CreateTaskInput{RequestID: newID(), Goal: "Review the complete explicit task context.", ParticipantAgentIDs: []string{agent.ID}, FirstAgentID: agent.ID}, budget)
				if err != nil {
					t.Fatal(err)
				}
				for range workload.inputs {
					if _, err = h.store.OperateTask(h.actor, task.ID, "inputs", storage.TaskOperation{RequestID: newID(), Content: strings.Repeat("x", 512)}); err != nil {
						t.Fatal(err)
					}
				}
				run, err = h.store.GetRun(h.ctx, task.ActiveRunID)
			} else {
				var sent storage.SendResult
				sent, err = h.store.SendMessage(h.actor, room.ID, storage.SendMessageInput{RequestID: newID(), Content: "Inspect the immutable input.", RecipientAgentIDs: []string{agent.ID}, RunnerRevision: &room.RunnerRevision, References: refs}, budget)
				if err == nil {
					run = sent.Runs[0]
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			run, attempt, err := h.store.ClaimRemoteRun(h.ctx, run.ID, runner.ID, 2*time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			row := preparationSample{Workload: workload.name, Files: workload.files, ProjectBytes: workload.files * workload.size, References: workload.references, TaskInputs: workload.inputs, PhasesMS: map[string]float64{}}
			measure := func(name string, operation func() error) {
				t.Helper()
				start := time.Now()
				if err := operation(); err != nil {
					t.Fatal(name, err)
				}
				row.PhasesMS[name] = float64(time.Since(start)) / float64(time.Millisecond)
			}
			directory := t.TempDir()
			measure("context", func() error {
				prompt, err := runinput.Conversation(h.ctx, h.store, run, storage.Conversation{})
				if err == nil && run.TaskID != "" {
					var task storage.CollaborationTask
					task, err = h.store.GetTask(h.ctx, run.TaskID)
					prompt = runinput.TaskBrief(task, run) + "\nRoom context:\n" + prompt
				}
				row.PromptBytes = len(prompt)
				return err
			})
			measure("projectArchive", func() error {
				artifact, err := writeArtifact(filepath.Join(directory, "project.tar"), func(w io.Writer) error { return m.files.ExportArchive(h.ctx, resourceID, run.BaselineCommit, w) })
				row.ArchiveBytes = artifact.Bytes
				return err
			})
			var references string
			measure("references", func() error {
				var err error
				references, err = runinput.PrepareReferences(h.ctx, m.files, run, directory)
				return err
			})
			measure("referenceArchive", func() error {
				if references == "" {
					return nil
				}
				_, err := writeArtifact(filepath.Join(directory, "references.tar"), func(w io.Writer) error { return resources.WriteArchive(h.ctx, references, w) })
				return err
			})
			measure("prepare", func() error { return m.prepare(h.ctx, run, attempt) })
			retained, err := h.store.GetAttempt(h.ctx, attempt.ID)
			if err != nil || retained.ModelInFlight || len(retained.Command) == 0 || retained.InputDigest == "" {
				t.Fatal("input was not retained without model execution", err)
			}
			active, err := h.store.ListActiveAttempts(h.ctx)
			if err != nil {
				t.Fatal(err)
			}
			row.ActiveAttempts = len(active)
			m.lastGC = time.Now()
			measure("reconcileWithoutPreparation", func() error { return m.tick(h.ctx) })
			report.Samples = append(report.Samples, row)
			t.Logf("%s sample %d: prepare %.3f ms", workload.name, sample+1, row.PhasesMS["prepare"])
		}
	}
	report.Complete = true
}
