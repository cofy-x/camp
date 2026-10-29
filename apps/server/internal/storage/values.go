package storage

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"time"
	"unicode/utf8"
)

func nullableParent(id string) sql.NullString { return sql.NullString{String: id, Valid: id != ""} }

func id() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func truncateUTF8(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
