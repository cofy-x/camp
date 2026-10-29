package storage

import "encoding/json"

const MaxTextSize = 1 << 20

// Error describes a rejected storage operation without choosing an HTTP status.
type Error struct {
	Code, Message string
}

func (e *Error) Error() string { return e.Message }

func problem(code, message string) error { return &Error{Code: code, Message: message} }

type Preferences struct {
	Wallpaper    string `json:"wallpaper"`
	IconSnap     bool   `json:"iconSnap"`
	ControlStrip bool   `json:"controlStrip"`
}

type Bootstrap struct {
	Preferences Preferences     `json:"preferences"`
	Layout      json.RawMessage `json:"layout"`
}
