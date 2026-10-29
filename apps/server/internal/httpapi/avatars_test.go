package httpapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/avatars"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestAvatarCatalogAndProfileAreAuthenticatedAndValidated(t *testing.T) {
	f := newTeamFixture(t)
	requireHTTP(t, teamRequest(t, f, nil, "GET", "/api/avatars", nil), 401)
	catalog := teamRequest(t, f, f.memberCookie, "GET", "/api/avatars", nil)
	requireHTTP(t, catalog, 200)
	var body struct {
		Avatars []avatars.Entry `json:"avatars"`
	}
	if err := json.Unmarshal(catalog.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	var memberAvatar, agentAvatar string
	for _, entry := range body.Avatars {
		if entry.Kind == avatars.Agent {
			agentAvatar = entry.ID
		} else if entry.ID != f.member.Avatar {
			memberAvatar = entry.ID
		}
		image := teamRequest(t, f, f.memberCookie, "GET", "/api/avatars/"+entry.ID, nil)
		requireHTTP(t, image, 200)
		if image.Header().Get("Content-Type") != "image/svg+xml" || !strings.Contains(image.Header().Get("Cache-Control"), "immutable") {
			t.Fatal(image.Header())
		}
	}
	requireHTTP(t, teamRequest(t, f, nil, "GET", "/api/avatars/"+memberAvatar, nil), 401)
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", "/api/avatars/unknown", nil), 404)
	change := map[string]any{"avatar": memberAvatar, "expectedAvatar": f.member.Avatar}
	reply := teamRequest(t, f, f.memberCookie, "PATCH", "/api/me/avatar", change)
	requireHTTP(t, reply, 200)
	var result struct {
		User storage.User `json:"user"`
	}
	if err := json.Unmarshal(reply.Body.Bytes(), &result); err != nil || result.User.ID != f.member.ID || result.User.Avatar != memberAvatar {
		t.Fatal(result, err)
	}
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "PATCH", "/api/me/avatar", change), 200)
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "PATCH", "/api/me/avatar", map[string]any{"avatar": f.member.Avatar, "expectedAvatar": f.member.Avatar}), 409)
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "PATCH", "/api/me/avatar", map[string]any{"avatar": agentAvatar, "expectedAvatar": memberAvatar}), 400)
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "PATCH", "/api/me/avatar", map[string]any{"avatar": memberAvatar, "expectedAvatar": memberAvatar, "userId": f.admin.ID}), 400)
	owner, err := f.store.GetUser(context.Background(), f.admin.ID)
	if err != nil || owner.Avatar != f.admin.Avatar {
		t.Fatal("profile change escaped the signed-in account", owner, err)
	}
	invalidAgent := teamRequest(t, f, f.memberCookie, "POST", "/api/agents", map[string]any{"name": "Wrong avatar", "handle": "wrong-avatar", "avatar": memberAvatar})
	requireHTTP(t, invalidAgent, 400)
}
