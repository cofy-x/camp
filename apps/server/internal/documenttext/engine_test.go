package documenttext

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rc4"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestEngineAdmission(t *testing.T) {
	engine := New(Config{})
	_, err := engine.Extract(context.Background(), "notes.txt", []byte("text"))
	requireFailure(t, err, "unsupported_format")
	_, err = engine.Extract(context.Background(), "empty.pdf", nil)
	requireFailure(t, err, "malformed")
	_, err = engine.Extract(context.Background(), "big.docx", make([]byte, MaxInputBytes+1))
	requireFailure(t, err, "input_limit")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = engine.Extract(ctx, "notes.pdf", []byte("header"))
	requireFailure(t, err, "cancelled")
	if Format("FILE.PDF") != "pdf" || Format("FILE.Docx") != "docx" || Format("old.doc") != "" {
		t.Fatal("incorrect supported extensions")
	}
}

func integrationEngine(t *testing.T) *Engine {
	t.Helper()
	root := os.Getenv("CAMP_TEST_DOCUMENT_RUNTIME")
	if root == "" {
		t.Skip("set CAMP_TEST_DOCUMENT_RUNTIME to test isolated native document readers")
	}
	if runtime.GOOS != "linux" {
		t.Skip("Linux Bubblewrap is required")
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Fatal(err)
	}
	return New(Config{RuntimeDir: root})
}

func TestIntegrationPDFAndDOCXImmutableSources(t *testing.T) {
	engine := integrationEngine(t)
	for _, test := range []struct {
		name   string
		count  int
		source string
	}{{"customer-brief.pdf", 2, "No customer-satisfaction baseline"}, {"interview-notes.docx", 9, "Morgan approves every"}} {
		t.Run(test.name, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join("../../../../examples/documents", test.name))
			if err != nil {
				t.Fatal(err)
			}
			original := bytes.Clone(content)
			result, err := engine.Extract(context.Background(), test.name, content)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Sections) != test.count {
				t.Fatalf("sections=%d want=%d", len(result.Sections), test.count)
			}
			all := ""
			for _, section := range result.Sections {
				all += section.Text
			}
			if !strings.Contains(all, test.source) {
				t.Fatalf("missing evidence: %s", all)
			}
			if !bytes.Equal(content, original) {
				t.Fatal("reader changed original bytes")
			}
		})
	}
}

func TestIntegrationPDFTerminalFailuresAndEmptyPageProvenance(t *testing.T) {
	engine := integrationEngine(t)
	noText, err := os.ReadFile("testdata/no-text.pdf")
	if err != nil {
		t.Fatal(err)
	}
	textPages := make([]string, 150)
	for i := range textPages {
		textPages[i] = strings.Repeat(strings.Repeat("word ", 20)+"\n", 80)
	}
	tests := []struct {
		name string
		data []byte
		code string
	}{
		{"malformed", []byte("%PDF-1.7\ninvalid"), "malformed"},
		{"no text", noText, "no_text"},
		{"locked", pdfFixture([]string{"secret"}, true), "encrypted"},
		{"page limit", pdfFixture(make([]string, MaxPDFPages+1), false), "limit_exceeded"},
		{"text limit", pdfFixture(textPages, false), "limit_exceeded"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := engine.Extract(context.Background(), "fixture.pdf", test.data)
			requireFailure(t, err, test.code)
			if len(result.Sections) != 0 {
				t.Fatal("failure returned partial text")
			}
		})
	}
	result, err := engine.Extract(context.Background(), "mixed.pdf", pdfFixture([]string{"first", "", "third"}, false))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sections) != 3 || result.Sections[1].ID != "p2" || result.Sections[1].Text != "" || result.Sections[2].Label != "Page 3" || !strings.Contains(strings.Join(result.Warnings, " "), "physical pages: 2") {
		t.Fatalf("empty page changed provenance: %+v", result)
	}
}

func TestIntegrationReaderHasNoSecretsHostFilesOrNetwork(t *testing.T) {
	engine := integrationEngine(t)
	runtimeDir := t.TempDir()
	private := filepath.Join(t.TempDir(), "private.txt")
	if err := os.WriteFile(private, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAMP_DOCUMENT_TEST_SECRET", "never inherited")
	script := fmt.Sprintf(`import { existsSync } from 'node:fs';
import { connect } from 'node:net';
if (process.env.CAMP_DOCUMENT_TEST_SECRET || existsSync(%q)) process.exit(3);
const socket=connect({host:'1.1.1.1',port:53});
socket.on('connect',()=>process.exit(4));
socket.on('error',()=>{process.stdout.write(JSON.stringify({result:{format:'pdf',sections:[{id:'p1',label:'Page 1',text:'isolated'}],warnings:[]}}));});
setTimeout(()=>process.exit(5),2000).unref();`, private)
	if err := os.WriteFile(filepath.Join(runtimeDir, "extract.mjs"), []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	engine.config.RuntimeDir = runtimeDir
	result, err := engine.Extract(context.Background(), "fixture.pdf", []byte("%PDF-1.7"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Sections[0].Text != "isolated" {
		t.Fatal("sandbox probe did not complete")
	}
}

func TestIntegrationCancellationReapsDetachedReaderChildren(t *testing.T) {
	engine := integrationEngine(t)
	runtimeDir := t.TempDir()
	marker := "camp-document-cleanup-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	script := fmt.Sprintf(`import { spawn } from 'node:child_process';
spawn('/node',['-e','setInterval(()=>{},1000)',%q],{detached:true,stdio:'ignore'}).unref();
setInterval(()=>{},1000);`, marker)
	if err := os.WriteFile(filepath.Join(runtimeDir, "extract.mjs"), []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	engine.config.RuntimeDir = runtimeDir
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := engine.Extract(ctx, "fixture.pdf", []byte("%PDF-1.7")); done <- err }()
	deadline := time.Now().Add(5 * time.Second)
	for !processMarkerExists(marker) {
		if time.Now().After(deadline) {
			t.Fatal("detached child did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		requireFailure(t, err, "cancelled")
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not finish")
	}
	deadline = time.Now().Add(3 * time.Second)
	for processMarkerExists(marker) {
		if time.Now().After(deadline) {
			t.Fatal("detached reader child survived cancellation")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestIntegrationOutputLimitStopsReader(t *testing.T) {
	engine := integrationEngine(t)
	runtimeDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(runtimeDir, "extract.mjs"), []byte(`process.stdout.write('x'.repeat(9*1024*1024)); setInterval(()=>{},1000);`), 0600); err != nil {
		t.Fatal(err)
	}
	engine.config.RuntimeDir = runtimeDir
	_, err := engine.Extract(context.Background(), "fixture.pdf", []byte("%PDF-1.7"))
	requireFailure(t, err, "limit_exceeded")
}

func TestIntegrationDeadlineAndProtocolFailures(t *testing.T) {
	engine := integrationEngine(t)
	runtimeDir := t.TempDir()
	engine.config.RuntimeDir = runtimeDir
	entry := filepath.Join(runtimeDir, "extract.mjs")
	if err := os.WriteFile(entry, []byte(`setInterval(()=>{},1000);`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := engine.Extract(ctx, "fixture.pdf", []byte("%PDF-1.7"))
	requireFailure(t, err, "timeout")
	for _, output := range []string{`{}`, `{"result":{"format":"pdf","sections":[{"id":"p9","label":"Page 9","text":"wrong location"}],"warnings":[]}}`, `{"error":{"code":"malformed","message":"failed","retryable":false}} {}`} {
		script := "process.stdout.write(" + strconv.Quote(output) + ");"
		if err := os.WriteFile(entry, []byte(script), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := engine.Extract(context.Background(), "fixture.pdf", []byte("%PDF-1.7"))
		requireFailure(t, err, "invalid_result")
	}
}

func processMarkerExists(marker string) bool {
	entries, _ := os.ReadDir("/proc")
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		data, _ := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if bytes.Contains(data, []byte(marker)) {
			return true
		}
	}
	return false
}

func pdfFixture(pages []string, locked bool) []byte {
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "", "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"}
	kids := []string{}
	for _, text := range pages {
		id := len(objects) + 1
		kids = append(kids, fmt.Sprintf("%d 0 R", id))
		objects = append(objects, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", id+1))
		stream := "BT /F1 6 Tf 50 740 Td 8 TL\n"
		for _, line := range strings.Split(text, "\n") {
			stream += "(" + line + ") Tj T*\n"
		}
		stream += "ET"
		objects = append(objects, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	}
	objects[1] = fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(pages))
	trailer := ""
	if locked {
		padding := []byte{0x28, 0xbf, 0x4e, 0x5e, 0x4e, 0x75, 0x8a, 0x41, 0x64, 0x00, 0x4e, 0x56, 0xff, 0xfa, 0x01, 0x08, 0x2e, 0x2e, 0x00, 0xb6, 0xd0, 0x68, 0x3e, 0x80, 0x2f, 0x0c, 0xa9, 0xfe, 0x64, 0x53, 0x69, 0x7a}
		user := append([]byte("secret"), padding...)
		user = user[:32]
		owner := md5.Sum(padding)
		cipher, _ := rc4.NewCipher(owner[:5])
		o := make([]byte, 32)
		cipher.XORKeyStream(o, user)
		id := []byte("document-fixture")
		permissions := make([]byte, 4)
		binary.LittleEndian.PutUint32(permissions, uint32(0xfffffffc))
		keyInput := append(bytes.Clone(user), o...)
		keyInput = append(keyInput, permissions...)
		keyInput = append(keyInput, id...)
		key := md5.Sum(keyInput)
		cipher, _ = rc4.NewCipher(key[:5])
		u := make([]byte, 32)
		cipher.XORKeyStream(u, padding)
		objects = append(objects, fmt.Sprintf("<< /Filter /Standard /V 1 /R 2 /Length 40 /O <%x> /U <%x> /P -4 >>", o, u))
		trailer = fmt.Sprintf(" /Encrypt %d 0 R /ID [<%x> <%x>]", len(objects), id, id)
	}
	var output strings.Builder
	output.WriteString("%PDF-1.7\n")
	offsets := []int{}
	for i, obj := range objects {
		offsets = append(offsets, output.Len())
		fmt.Fprintf(&output, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := output.Len()
	fmt.Fprintf(&output, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&output, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&output, "trailer\n<< /Size %d /Root 1 0 R%s >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, trailer, xref)
	return []byte(output.String())
}
