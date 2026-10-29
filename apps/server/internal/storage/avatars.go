package storage

import (
	"context"
	"database/sql"

	"github.com/cofy-x/camp/apps/server/internal/avatars"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func chooseMemberAvatar(ctx context.Context, q *sqlgen.Queries) (string, error) {
	rows, err := q.ListUserAvatarCounts(ctx)
	if err != nil {
		return "", err
	}
	counts := map[string]int64{}
	for _, row := range rows {
		counts[row.Avatar] = row.Uses
	}
	return avatars.Choose(avatars.Member, counts)
}

func chooseAgentAvatar(ctx context.Context, q *sqlgen.Queries) (string, error) {
	rows, err := q.ListAgentAvatarCounts(ctx)
	if err != nil {
		return "", err
	}
	counts := map[string]int64{}
	for _, row := range rows {
		counts[row.Avatar] = row.Uses
	}
	return avatars.Choose(avatars.Agent, counts)
}

// Backfill only missing selections, without changing authorship, Agent
// revisions, timestamps or frozen Run snapshots. A failed transaction retries
// at startup under the existing initialization lock.
func (s *Store) ensureAvatars(ctx context.Context) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		userCounts, err := q.ListUserAvatarCounts(ctx)
		if err != nil {
			return err
		}
		counts := map[string]int64{}
		for _, row := range userCounts {
			counts[row.Avatar] = row.Uses
		}
		users, err := q.ListMissingUserAvatars(ctx)
		if err != nil {
			return err
		}
		for _, user := range users {
			id, err := avatars.Choose(avatars.Member, counts)
			if err != nil {
				return err
			}
			if err := q.BackfillUserAvatar(ctx, sqlgen.BackfillUserAvatarParams{ID: user, Avatar: id}); err != nil {
				return err
			}
			counts[id]++
		}
		agentCounts, err := q.ListAgentAvatarCounts(ctx)
		if err != nil {
			return err
		}
		counts = map[string]int64{}
		for _, row := range agentCounts {
			counts[row.Avatar] = row.Uses
		}
		agents, err := q.ListMissingAgentAvatars(ctx)
		if err != nil {
			return err
		}
		for _, agent := range agents {
			id, err := avatars.Choose(avatars.Agent, counts)
			if err != nil {
				return err
			}
			if err := q.BackfillAgentAvatar(ctx, sqlgen.BackfillAgentAvatarParams{ID: agent, Avatar: id}); err != nil {
				return err
			}
			counts[id]++
		}
		return nil
	})
}

// UpdateOwnAvatar cannot target another member, including for administrators.
// Comparing the prior selection prevents an older editor overwriting a newer
// choice. An identical retry is safe after an uncertain response.
func (s *Store) UpdateOwnAvatar(ctx context.Context, avatar, expected string) (User, error) {
	var saved User
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		actor, ok := UserFromContext(ctx)
		if !ok {
			return problem("unauthenticated", "Sign in to Camp.")
		}
		if err := requireActor(ctx, q, false); err != nil {
			return err
		}
		if !avatars.Valid(avatar, avatars.Member) {
			return problem("invalid_avatar", "Choose a member avatar from the catalog.")
		}
		current, err := q.GetUser(ctx, actor.ID)
		if err != nil {
			return err
		}
		saved = userRow(current)
		if current.Avatar == avatar {
			return nil
		}
		if current.Avatar != expected {
			return problem("profile_conflict", "Your avatar changed in another session. Reload your current avatar before saving.")
		}
		saved.Avatar, saved.UpdatedAt = avatar, now()
		n, err := q.UpdateUserAvatar(ctx, sqlgen.UpdateUserAvatarParams{ID: actor.ID, Avatar: avatar, ExpectedAvatar: expected, UpdatedAt: saved.UpdatedAt})
		if err != nil {
			return err
		}
		if n != 1 {
			return problem("profile_conflict", "Your avatar changed. Reload before saving.")
		}
		return event(ctx, q, "team.changed", "", actor.ID)
	})
	return saved, err
}
