package documenttext

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "document-worker" {
		if err := Worker(context.Background(), os.Stdin, os.Stdout); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

const contentTypes = `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`

func archive(t *testing.T, entries [][2]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	w := zip.NewWriter(&buffer)
	for _, entry := range entries {
		part, err := w.Create(entry[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(entry[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func docx(t *testing.T, body string, extras ...[2]string) []byte {
	t.Helper()
	entries := [][2]string{{"[Content_Types].xml", contentTypes}, {"word/document.xml", `<w:document xmlns:w="` + wordNamespace + `"><w:body>` + body + `</w:body></w:document>`}}
	return archive(t, append(entries, extras...))
}

func paragraph(text string) string { return `<w:p><w:r><w:t>` + text + `</w:t></w:r></w:p>` }

func requireFailure(t *testing.T, err error, code string) *Failure {
	t.Helper()
	var failure *Failure
	if !errors.As(err, &failure) || failure.Code != code {
		t.Fatalf("error = %v, want failure %s", err, code)
	}
	return failure
}

func TestDOCXBusinessFixtureAndStableParagraphSources(t *testing.T) {
	content, err := os.ReadFile("../../../../examples/documents/interview-notes.docx")
	if err != nil {
		t.Fatal(err)
	}
	result, err := extractDOCX(context.Background(), content)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sections) != 9 || result.Sections[2].ID != "p3" || result.Sections[2].Text != "Specialist: Setup questions repeat, and answers live in private notes." || result.Sections[7].Label != "Paragraph 8" || result.Sections[7].Text != "Morgan approves every published guide update." {
		t.Fatalf("unexpected source sections: %+v", result)
	}
	if err := result.validate("docx"); err != nil {
		t.Fatal(err)
	}
}

func TestDOCXVisibleBodyOnlyAndOmissionWarnings(t *testing.T) {
	body := `<w:p><w:r><w:t xml:space="preserve">Hello </w:t></w:r><w:r><w:t>world</w:t><w:tab/><w:t>value</w:t><w:br/><w:t>next</w:t><w:instrText>DO NOT EXECUTE</w:instrText></w:r><w:del><w:r><w:delText>deleted</w:delText></w:r></w:del><w:ins><w:r><w:t> inserted</w:t></w:r></w:ins><w:drawing><w:txbxContent>` + paragraph("hidden textbox") + `</w:txbxContent></w:drawing></w:p><w:p/><w:tbl><w:tr><w:tc>` + paragraph("cell &amp; text") + `</w:tc></w:tr></w:tbl>`
	result, err := extractDOCX(context.Background(), docx(t, body, [2]string{"word/header1.xml", "not parsed"}, [2]string{"word/media/image.png", "not parsed"}, [2]string{"word/_rels/document.xml.rels", `<Relationships><Relationship Target="http://127.0.0.1/private" TargetMode="External"/></Relationships>`}))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sections) != 2 || result.Sections[0].Text != "Hello world\tvalue\nnext inserted" || result.Sections[1].Text != "cell & text" {
		t.Fatalf("unexpected text: %+v", result.Sections)
	}
	if len(result.Warnings) != 6 {
		t.Fatalf("omission warnings = %v", result.Warnings)
	}
}

func TestDOCXSimpleFieldPreservesSavedValueAndReportsOmission(t *testing.T) {
	body := `<w:p><w:fldSimple w:instr="DATE"><w:r><w:t>October 27, 2026</w:t></w:r></w:fldSimple></w:p>`
	result, err := extractDOCX(context.Background(), docx(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sections) != 1 || result.Sections[0].Text != "October 27, 2026" {
		t.Fatalf("saved field value was changed: %+v", result.Sections)
	}
	if !strings.Contains(strings.Join(result.Warnings, " "), "Field instructions are omitted. Displayed field values are included as saved, without recalculation.") {
		t.Fatalf("missing field warning: %v", result.Warnings)
	}
}

func TestDOCXRejectsUnsafeMalformedAndOversizedContent(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		code string
	}{
		{"not zip", []byte("not a docx"), "malformed"},
		{"locked office", []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1}, "encrypted"},
		{"missing part", archive(t, [][2]string{{"[Content_Types].xml", contentTypes}}), "malformed"},
		{"duplicate part", docx(t, paragraph("one"), [2]string{"word/document.xml", "duplicate"}), "malformed"},
		{"unsafe part", docx(t, paragraph("one"), [2]string{"../escape", "no"}), "malformed"},
		{"empty body", docx(t, `<w:p/>`), "no_text"},
		{"broken xml", docx(t, `<w:p>`), "malformed"},
		{"external entity", docx(t, `<!DOCTYPE foo [<!ENTITY xxe SYSTEM "file:///etc/passwd">]>`+paragraph("&xxe;")), "malformed"},
		{"nesting", docx(t, strings.Repeat(`<w:sdt>`, 129)+paragraph("deep")+strings.Repeat(`</w:sdt>`, 129)), "limit_exceeded"},
		{"paragraphs", docx(t, strings.Repeat(paragraph("many"), MaxDOCXParagraphs+1)), "limit_exceeded"},
		{"text", docx(t, paragraph(strings.Repeat("a", MaxTextBytes+1))), "limit_exceeded"},
		{"expanded zip", docx(t, paragraph("ok"), [2]string{"word/media/bomb", strings.Repeat("x", (32<<20)+1)}), "limit_exceeded"},
		{"macro content type", archive(t, [][2]string{{"[Content_Types].xml", strings.ReplaceAll(contentTypes, "wordprocessingml.document.main+xml", "ms-word.document.macroEnabled.main+xml")}, {"word/document.xml", "ignored"}}), "unsupported_format"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := extractDOCX(context.Background(), test.data)
			requireFailure(t, err, test.code)
			if len(result.Sections) != 0 {
				t.Fatal("failure returned partial text")
			}
		})
	}
}

func TestDOCXStrictNamespaceAndWorkerEnvelope(t *testing.T) {
	data := archive(t, [][2]string{{"[Content_Types].xml", contentTypes}, {"word/document.xml", `<w:document xmlns:w="` + strictWordNamespace + `"><w:body>` + paragraph("Strict text") + `</w:body></w:document>`}})
	var output bytes.Buffer
	if err := Worker(context.Background(), bytes.NewReader(data), &output); err != nil {
		t.Fatal(err)
	}
	var message response
	if err := json.Unmarshal(output.Bytes(), &message); err != nil {
		t.Fatal(err)
	}
	if message.Result == nil || message.Result.Sections[0].Text != "Strict text" || message.Error != nil {
		t.Fatalf("worker result %+v", message)
	}
	output.Reset()
	if err := Worker(context.Background(), strings.NewReader("invalid"), &output); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(output.Bytes(), &message); err != nil {
		t.Fatal(err)
	}
	if message.Error == nil || message.Error.Code != "malformed" {
		t.Fatalf("worker error %+v", message)
	}
}

func TestDOCXCancelledBeforeParse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := extractDOCX(ctx, docx(t, paragraph("body")))
	requireFailure(t, err, "cancelled")
	if !errors.Is(err, context.Canceled) {
		t.Fatal("context cancellation is not preserved")
	}
}
