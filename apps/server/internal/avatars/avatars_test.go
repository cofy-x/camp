package avatars

import (
	"encoding/json"
	"encoding/xml"
	"io"
	"regexp"
	"strings"
	"testing"
)

func TestBundledCatalogIsCompleteAndSelfContained(t *testing.T) {
	var data struct {
		Art     map[string][]string `json:"art"`
		Entries []struct {
			Entry
			Art    string            `json:"art"`
			Colors map[string]string `json:"colors"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(source, &data); err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	hex := regexp.MustCompile(`^#[0-9a-f]{6}$`)
	idPattern := regexp.MustCompile(`^(member|agent)-[a-z]+-[a-z]+-v[0-9]+$`)
	for _, entry := range data.Entries {
		if ids[entry.ID] || !idPattern.MatchString(entry.ID) || !strings.HasPrefix(entry.ID, entry.Kind+"-") || entry.Name == "" || !Valid(entry.ID, entry.Kind) {
			t.Fatalf("invalid catalog entry: %+v", entry.Entry)
		}
		ids[entry.ID] = true
		rows := data.Art[entry.Art]
		if len(rows) != 20 {
			t.Fatal("wrong artwork height", entry.ID)
		}
		for _, row := range rows {
			if len(row) != 20 {
				t.Fatal("wrong artwork width", entry.ID)
			}
			for _, pixel := range row {
				if !hex.MatchString(entry.Colors[string(pixel)]) {
					t.Fatal("undefined or unsafe color", entry.ID, pixel)
				}
			}
		}
		image, ok := Image(entry.ID)
		if !ok {
			t.Fatal("missing image", entry.ID)
		}
		decoder := xml.NewDecoder(strings.NewReader(string(image)))
		for {
			token, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if start, ok := token.(xml.StartElement); ok {
				if start.Name.Local != "svg" && start.Name.Local != "path" {
					t.Fatal("unexpected active image element", start.Name)
				}
				for _, attr := range start.Attr {
					if strings.Contains(attr.Name.Local, "href") || strings.HasPrefix(attr.Name.Local, "on") {
						t.Fatal("active image attribute", attr.Name)
					}
				}
			}
		}
	}
	if Valid("../unknown", Member) || Valid("", Member) {
		t.Fatal("accepted an unknown avatar")
	}
}

func TestDefaultSelectionBalancesOnlyItsIdentityKind(t *testing.T) {
	for _, kind := range []string{Member, Agent} {
		counts := map[string]int64{}
		for range 100 {
			id, err := Choose(kind, counts)
			if err != nil || !Valid(id, kind) {
				t.Fatal(id, err)
			}
			counts[id]++
			var lowest, highest int64 = 100, 0
			for _, entry := range Catalog() {
				if entry.Kind == kind {
					lowest = min(lowest, counts[entry.ID])
					highest = max(highest, counts[entry.ID])
				}
			}
			if highest-lowest > 1 {
				t.Fatal("default repeated before available choices were used", counts)
			}
		}
	}
}
