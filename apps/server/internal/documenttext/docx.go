package documenttext

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"unicode"
)

const wordNamespace = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
const strictWordNamespace = "http://purl.oclc.org/ooxml/wordprocessingml/main"
const maxXMLBytes = 8 << 20
const maxXMLTokens = 500000

// Worker is the private Camp DOCX subprocess entry point. It reads only stdin;
// startup configuration, database access and environment loading are excluded.
func Worker(ctx context.Context, input io.Reader, output io.Writer) error {
	content, err := io.ReadAll(io.LimitReader(input, MaxInputBytes+1))
	var result Result
	if err != nil {
		err = failure("reader_failed", "The document reader could not receive the original file.", true)
	} else if len(content) > MaxInputBytes {
		err = failure("input_limit", "The document exceeds the 8 MiB preparation limit.", false)
	} else {
		result, err = extractDOCX(ctx, content)
	}
	if err != nil {
		var problem *Failure
		if !errors.As(err, &problem) {
			problem = failure("malformed", "The DOCX document could not be read.", false)
		}
		return json.NewEncoder(output).Encode(response{Error: problem})
	}
	return json.NewEncoder(output).Encode(response{Result: &result})
}

func extractDOCX(ctx context.Context, content []byte) (Result, error) {
	if bytes.HasPrefix(content, []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1}) {
		return Result{}, failure("encrypted", "This file uses an encrypted or legacy Office container. Upload an unencrypted DOCX copy.", false)
	}
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return Result{}, failure("malformed", "This file is not a readable DOCX archive.", false)
	}
	if len(reader.File) > 2048 {
		return Result{}, failure("limit_exceeded", "The DOCX archive contains more than 2,048 entries.", false)
	}
	entries := make(map[string]*zip.File, len(reader.File))
	var expanded uint64
	warnings := []string{"Extracted text includes the main document body and table paragraphs. Formatting and page layout are not reproduced; paragraph numbers identify nonempty extracted paragraphs."}
	hasOtherStories, hasMedia := false, false
	for _, file := range reader.File {
		if _, exists := entries[file.Name]; exists || strings.Contains(file.Name, "\\") || strings.HasPrefix(file.Name, "/") || path.Clean(file.Name) != strings.TrimSuffix(file.Name, "/") || strings.HasPrefix(file.Name, "../") {
			return Result{}, failure("malformed", "The DOCX archive has ambiguous or unsafe entry names.", false)
		}
		entries[file.Name] = file
		if file.UncompressedSize64 > 32<<20 || expanded > 32<<20-file.UncompressedSize64 {
			return Result{}, failure("limit_exceeded", "The DOCX archive exceeds the 32 MiB expanded-data limit.", false)
		}
		expanded += file.UncompressedSize64
		if strings.HasPrefix(file.Name, "word/header") || strings.HasPrefix(file.Name, "word/footer") || file.Name == "word/footnotes.xml" || file.Name == "word/endnotes.xml" || file.Name == "word/comments.xml" {
			hasOtherStories = true
		}
		if strings.HasPrefix(file.Name, "word/media/") || strings.HasPrefix(file.Name, "word/embeddings/") {
			hasMedia = true
		}
	}
	contentTypes, err := readPart(entries["[Content_Types].xml"], 64<<10)
	if err != nil {
		return Result{}, err
	}
	if err := validateContentType(contentTypes); err != nil {
		return Result{}, err
	}
	body, err := readPart(entries["word/document.xml"], maxXMLBytes)
	if err != nil {
		return Result{}, err
	}
	if hasOtherStories {
		warnings = append(warnings, "Headers, footers, footnotes, endnotes and comments are not included.")
	}
	if hasMedia {
		warnings = append(warnings, "Images and embedded objects are not read. No OCR is performed.")
	}
	result, err := parseBody(ctx, body)
	if err != nil {
		return Result{}, err
	}
	result.Warnings = append(warnings, result.Warnings...)
	return result, nil
}

func readPart(file *zip.File, limit int64) ([]byte, error) {
	if file == nil {
		return nil, failure("malformed", "The DOCX archive is missing a required document part.", false)
	}
	if file.UncompressedSize64 > uint64(limit) {
		return nil, failure("limit_exceeded", "A DOCX document part exceeds its preparation limit.", false)
	}
	stream, err := file.Open()
	if err != nil {
		return nil, failure("malformed", "A DOCX document part could not be opened.", false)
	}
	defer stream.Close()
	content, err := io.ReadAll(io.LimitReader(stream, limit+1))
	if err != nil {
		return nil, failure("malformed", "A DOCX document part is damaged or has an invalid checksum.", false)
	}
	if int64(len(content)) > limit {
		return nil, failure("limit_exceeded", "A DOCX document part exceeds its preparation limit.", false)
	}
	return content, nil
}

func validateContentType(content []byte) error {
	decoder := xml.NewDecoder(bytes.NewReader(content))
	found := false
	depth := 0
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return failure("malformed", "The DOCX content types could not be read.", false)
		}
		switch token := token.(type) {
		case xml.Directive:
			return failure("malformed", "DOCX XML declarations with directives are not supported.", false)
		case xml.StartElement:
			depth++
			if depth > 32 {
				return failure("limit_exceeded", "DOCX XML nesting exceeds its preparation limit.", false)
			}
			if token.Name.Space != "http://schemas.openxmlformats.org/package/2006/content-types" || token.Name.Local != "Override" {
				continue
			}
			var name, kind string
			for _, attr := range token.Attr {
				if attr.Name.Local == "PartName" {
					name = attr.Value
				}
				if attr.Name.Local == "ContentType" {
					kind = attr.Value
				}
			}
			if name == "/word/document.xml" {
				if found || kind != "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml" {
					return failure("unsupported_format", "Only standard, non-macro DOCX documents are supported.", false)
				}
				found = true
			}
		case xml.EndElement:
			depth--
		}
	}
	if !found {
		return failure("malformed", "The archive does not identify a standard DOCX main document.", false)
	}
	return nil
}

func isWord(name xml.Name) bool {
	return name.Space == wordNamespace || name.Space == strictWordNamespace
}

func parseBody(ctx context.Context, body []byte) (Result, error) {
	result := Result{Format: "docx", Sections: []Section{}, Warnings: []string{}}
	decoder := xml.NewDecoder(bytes.NewReader(body))
	depth, bodyDepth, paragraphDepth, textDepth, skipDepth, tokens, total := 0, 0, 0, 0, 0, 0, 0
	rootSeen, bodySeen, otherContent, revisions, fields := false, false, false, false, false
	var paragraph strings.Builder
	appendText := func(text string) error {
		if total+paragraph.Len()+len(text) > MaxTextBytes {
			return failure("limit_exceeded", "The document exceeds the 1 MiB extracted-text limit.", false)
		}
		paragraph.WriteString(text)
		return nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return Result{}, contextFailure(err)
		}
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Result{}, failure("malformed", "The DOCX document XML is damaged or unsupported.", false)
		}
		tokens++
		if tokens > maxXMLTokens {
			return Result{}, failure("limit_exceeded", "The DOCX document exceeds its XML complexity limit.", false)
		}
		switch token := token.(type) {
		case xml.Directive:
			return Result{}, failure("malformed", "DOCX XML declarations with directives are not supported.", false)
		case xml.StartElement:
			depth++
			if depth > 128 {
				return Result{}, failure("limit_exceeded", "The DOCX document exceeds its nesting limit.", false)
			}
			if depth == 1 {
				if rootSeen || !isWord(token.Name) || token.Name.Local != "document" {
					return Result{}, failure("malformed", "The DOCX main document has an invalid root.", false)
				}
				rootSeen = true
			}
			if !isWord(token.Name) {
				if bodyDepth > 0 && (token.Name.Local == "AlternateContent" || token.Name.Local == "oMath" || token.Name.Local == "oMathPara") && skipDepth == 0 {
					skipDepth = depth
					otherContent = true
				}
				continue
			}
			if token.Name.Local == "body" {
				if depth != 2 || bodySeen {
					return Result{}, failure("malformed", "The DOCX main document has an invalid body.", false)
				}
				bodyDepth = depth
				bodySeen = true
				continue
			}
			if bodyDepth == 0 || skipDepth > 0 {
				continue
			}
			switch token.Name.Local {
			case "del", "moveFrom":
				skipDepth = depth
				revisions = true
			case "ins", "moveTo":
				revisions = true
			case "drawing", "pict", "object", "txbxContent", "altChunk":
				skipDepth = depth
				otherContent = true
			case "instrText", "fldData":
				skipDepth = depth
				fields = true
			case "fldSimple":
				fields = true
			case "p":
				if paragraphDepth > 0 {
					return Result{}, failure("malformed", "The DOCX document contains nested body paragraphs.", false)
				}
				paragraphDepth = depth
				paragraph.Reset()
			case "t":
				if paragraphDepth > 0 {
					textDepth = depth
				}
			case "tab":
				if paragraphDepth > 0 {
					if err := appendText("\t"); err != nil {
						return Result{}, err
					}
				}
			case "br", "cr":
				if paragraphDepth > 0 {
					if err := appendText("\n"); err != nil {
						return Result{}, err
					}
				}
			}
		case xml.CharData:
			if skipDepth == 0 && textDepth > 0 && paragraphDepth > 0 {
				if err := appendText(string(token)); err != nil {
					return Result{}, err
				}
			}
		case xml.EndElement:
			if skipDepth == depth {
				skipDepth = 0
			}
			if textDepth == depth {
				textDepth = 0
			}
			if paragraphDepth == depth {
				text := strings.TrimFunc(paragraph.String(), unicode.IsSpace)
				if text != "" {
					if len(result.Sections) >= MaxDOCXParagraphs {
						return Result{}, failure("limit_exceeded", "The DOCX document exceeds the 2,000-paragraph limit.", false)
					}
					n := len(result.Sections) + 1
					result.Sections = append(result.Sections, Section{ID: fmt.Sprintf("p%d", n), Label: fmt.Sprintf("Paragraph %d", n), Text: text})
					total += len(text)
				}
				paragraphDepth = 0
				paragraph.Reset()
			}
			if bodyDepth == depth {
				bodyDepth = 0
			}
			depth--
		}
	}
	if !rootSeen || !bodySeen {
		return Result{}, failure("malformed", "The DOCX main document body is missing.", false)
	}
	if total == 0 {
		return Result{}, failure("no_text", "No readable body text was found. Images and scanned content require OCR, which is not available.", false)
	}
	if otherContent {
		result.Warnings = append(result.Warnings, "Drawings, text boxes, equations and embedded alternative content are omitted.")
	}
	if revisions {
		result.Warnings = append(result.Warnings, "Tracked deletions and moved-away text are omitted; inserted text is included.")
	}
	if fields {
		result.Warnings = append(result.Warnings, "Field instructions are omitted. Displayed field values are included as saved, without recalculation.")
	}
	return result, nil
}
