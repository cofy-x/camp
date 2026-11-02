package serverstorage

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
)

func randomID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(value[:])
}
func decode(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var tail any
	if decoder.Decode(&tail) != io.EOF {
		return errors.New("A maintenance record has trailing data")
	}
	return nil
}
func hashRegular(ctx context.Context, root *os.Root, name string, expected os.FileInfo) (string, error) {
	file, err := root.Open(name)
	if err != nil {
		return "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(expected, opened) {
		return "", errors.New("An inspected file changed identity")
	}
	hash := sha256.New()
	n, err := copyContext(ctx, hash, file, expected.Size())
	if err != nil {
		return "", err
	}
	after, err := file.Stat()
	if err != nil || n != expected.Size() || after.Size() != expected.Size() || !after.ModTime().Equal(expected.ModTime()) {
		return "", errors.New("An inspected file changed content")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
