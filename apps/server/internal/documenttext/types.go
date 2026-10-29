// Package documenttext prepares bounded, source-labelled text from immutable
// documents. Parsing runs in isolated subprocesses, independently of agents.
package documenttext

import (
	"fmt"
	"path/filepath"
	"strings"
)

const (
	Version           = "camp-document-text-v1-pdfjs-6.4.299"
	MaxInputBytes     = 8 << 20
	MaxTextBytes      = 1 << 20
	MaxPDFPages       = 200
	MaxDOCXParagraphs = 2000
)

type Section struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Text  string `json:"text"`
}

type Result struct {
	Format   string    `json:"format"`
	Sections []Section `json:"sections"`
	Warnings []string  `json:"warnings"`
}

type Failure struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	cause     error
}

func (f *Failure) Error() string { return f.Message }

func (f *Failure) Unwrap() error { return f.cause }

func failure(code, message string, retryable bool) *Failure {
	return &Failure{Code: code, Message: message, Retryable: retryable}
}

func Format(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pdf":
		return "pdf"
	case ".docx":
		return "docx"
	default:
		return ""
	}
}

type response struct {
	Result *Result  `json:"result,omitempty"`
	Error  *Failure `json:"error,omitempty"`
}

func (r Result) validate(format string) error {
	if r.Format != format || len(r.Sections) == 0 || len(r.Sections) > MaxDOCXParagraphs || (format == "pdf" && len(r.Sections) > MaxPDFPages) {
		return failure("invalid_result", "The document reader returned an invalid result.", true)
	}
	total := 0
	for i, section := range r.Sections {
		label := "Paragraph"
		if format == "pdf" {
			label = "Page"
		}
		if section.ID != fmt.Sprintf("p%d", i+1) || section.Label != fmt.Sprintf("%s %d", label, i+1) {
			return failure("invalid_result", "The document reader returned invalid source locations.", true)
		}
		total += len(section.Text)
	}
	if total == 0 || total > MaxTextBytes || len(r.Warnings) > 32 {
		return failure("invalid_result", "The document reader returned text outside its limits.", true)
	}
	return nil
}
