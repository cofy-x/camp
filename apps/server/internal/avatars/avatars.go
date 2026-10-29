// Package avatars owns Camp's bundled identity artwork and stable catalog IDs.
// The original pixel artwork is distributed under Camp's MIT license.
package avatars

import (
	"bytes"
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"fmt"
	"math/big"
)

const (
	Member = "member"
	Agent  = "agent"
)

type Entry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// IDs and their artwork are permanent. Add new IDs when changing a design so
// saved selections, backups and browser caches keep the same identity.
//
//go:embed catalog.json
var source []byte

var catalog, images = load()

func load() ([]Entry, map[string][]byte) {
	var data struct {
		Art     map[string][]string `json:"art"`
		Entries []struct {
			Entry
			Art    string            `json:"art"`
			Colors map[string]string `json:"colors"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(source, &data); err != nil {
		panic(err)
	}
	entries := make([]Entry, 0, len(data.Entries))
	images := make(map[string][]byte, len(data.Entries))
	for _, item := range data.Entries {
		var svg bytes.Buffer
		svg.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20" shape-rendering="crispEdges">`)
		fmt.Fprintf(&svg, `<path fill="%s" d="M0 0h20v20H0z"/>`, item.Colors["."])
		for y, row := range data.Art[item.Art] {
			for x := 0; x < len(row); {
				end := x + 1
				for end < len(row) && row[end] == row[x] {
					end++
				}
				if row[x] != '.' {
					fmt.Fprintf(&svg, `<path fill="%s" d="M%d %dh%dv1H%dz"/>`, item.Colors[string(row[x])], x, y, end-x, x)
				}
				x = end
			}
		}
		svg.WriteString(`</svg>`)
		entries = append(entries, item.Entry)
		images[item.ID] = svg.Bytes()
	}
	return entries, images
}

func Catalog() []Entry { return append([]Entry(nil), catalog...) }

func Valid(id, kind string) bool {
	for _, item := range catalog {
		if item.ID == id && item.Kind == kind {
			return true
		}
	}
	return false
}

func Image(id string) ([]byte, bool) { data, ok := images[id]; return data, ok }

// Choose balances defaults within the identity kind; explicit selections may
// repeat. The caller counts and assigns inside the same storage transaction.
func Choose(kind string, counts map[string]int64) (string, error) {
	var candidates []string
	var lowest int64 = -1
	for _, item := range catalog {
		if item.Kind != kind {
			continue
		}
		count := counts[item.ID]
		if lowest < 0 || count < lowest {
			lowest, candidates = count, nil
		}
		if count == lowest {
			candidates = append(candidates, item.ID)
		}
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("no avatars for %q", kind)
	}
	index, err := rand.Int(rand.Reader, big.NewInt(int64(len(candidates))))
	if err != nil {
		return "", err
	}
	return candidates[index.Int64()], nil
}
