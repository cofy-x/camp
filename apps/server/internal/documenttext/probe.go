package documenttext

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
)

// Probe exercises the actual isolated reader using a generated, bounded fixture.
// It never reads a user's document or contacts a model provider.
func (e *Engine) Probe(ctx context.Context, format string) error {
	var content []byte
	switch format {
	case "pdf":
		content = probePDF()
	case "docx":
		var buffer bytes.Buffer
		archive := zip.NewWriter(&buffer)
		for _, part := range [][2]string{{"[Content_Types].xml", `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`}, {"word/document.xml", `<w:document xmlns:w="` + wordNamespace + `"><w:body><w:p><w:r><w:t>Camp document readiness</w:t></w:r></w:p></w:body></w:document>`}} {
			writer, err := archive.Create(part[0])
			if err != nil {
				return err
			}
			if _, err := writer.Write([]byte(part[1])); err != nil {
				return err
			}
		}
		if err := archive.Close(); err != nil {
			return err
		}
		content = buffer.Bytes()
	default:
		return errors.New("Unsupported document readiness format")
	}
	result, err := e.Extract(ctx, "readiness."+format, content)
	if err != nil {
		return err
	}
	if len(result.Sections) != 1 || !strings.Contains(result.Sections[0].Text, "Camp document readiness") {
		return errors.New("The document reader did not return the expected readiness text")
	}
	return nil
}
func probePDF() []byte {
	stream := "BT /F1 12 Tf 50 740 Td (Camp document readiness) Tj ET"
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Kids [4 0 R] /Count 1 >>", "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>", "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents 5 0 R >>", fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream)}
	var output strings.Builder
	output.WriteString("%PDF-1.7\n")
	offsets := []int{}
	for i, object := range objects {
		offsets = append(offsets, output.Len())
		fmt.Fprintf(&output, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := output.Len()
	fmt.Fprintf(&output, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&output, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&output, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return []byte(output.String())
}
