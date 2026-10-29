package resources

import (
	"archive/tar"
	"bytes"
	"context"
	"testing"
)

func TestArchivePreservesBinaryAndRejectsUnsafeEntries(t *testing.T) {
	binary := []byte{0, 255, 1, 10, 128}
	archive := func(headers []*tar.Header) []byte {
		var out bytes.Buffer
		writer := tar.NewWriter(&out)
		for _, header := range headers {
			if err := writer.WriteHeader(header); err != nil {
				t.Fatal(err)
			}
			if header.Size > 0 {
				if _, err := writer.Write(binary); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		return out.Bytes()
	}
	regular := &tar.Header{Name: "./coverage/evidence.bin", Mode: 0644, Size: int64(len(binary)), Typeflag: tar.TypeReg}
	files, err := readArchive(context.Background(), bytes.NewReader(archive([]*tar.Header{regular})), Policy{})
	if err != nil || !bytes.Equal(files["coverage/evidence.bin"].content, binary) {
		t.Fatalf("Binary or generic directory lost: %v", err)
	}
	for _, header := range []*tar.Header{
		{Name: "../escape", Typeflag: tar.TypeReg},
		{Name: "/absolute", Typeflag: tar.TypeReg},
		{Name: "a/.env", Typeflag: tar.TypeReg},
		{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
		{Name: "hard", Typeflag: tar.TypeLink, Linkname: "coverage/evidence.bin"},
		{Name: "pipe", Typeflag: tar.TypeFifo},
		{Name: "setuid", Typeflag: tar.TypeReg, Mode: 04755},
		regular,
	} {
		if _, err := readArchive(context.Background(), bytes.NewReader(archive([]*tar.Header{regular, header})), Policy{}); err == nil {
			t.Fatalf("Accepted unsafe entry: %+v", header)
		}
	}
}
