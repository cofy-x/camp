package storage

import (
	"context"
	"database/sql"
	"errors"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

// Ownership controls management and execution. Room content remains team-visible.
func requireObjectOwner(ctx context.Context, q *sqlgen.Queries, owner string) error {
	if err := requireActor(ctx, q, false); err != nil {
		return err
	}
	actor, authenticated := UserFromContext(ctx)
	if !authenticated {
		return nil
	} // Trusted internal operations preserve their explicit caller checks.
	current, err := q.GetUser(ctx, actor.ID)
	if err != nil {
		return err
	}
	if current.Role == "admin" || owner != "" && owner == actor.ID {
		return nil
	}
	return problem("forbidden", "Only the owner or an administrator can change this object.")
}

// Read scopes also use current authority; a request context may predate demotion.
func currentActor(ctx context.Context, q *sqlgen.Queries) (User, bool, error) {
	if err := requireActor(ctx, q, false); err != nil {
		return User{}, false, err
	}
	actor, authenticated := UserFromContext(ctx)
	if !authenticated {
		return User{}, false, nil
	}
	row, err := q.GetUser(ctx, actor.ID)
	if err != nil {
		return User{}, true, err
	}
	return userRow(row), true, nil
}

func enabledOwner(ctx context.Context, q *sqlgen.Queries, owner string) error {
	if owner == "" {
		return nil
	}
	user, err := q.GetUser(ctx, owner)
	if err != nil || !user.Enabled {
		return problem("owner_disabled", "The owner is no longer enabled.")
	}
	return nil
}

func runnerAllowsAgent(ctx context.Context, q *sqlgen.Queries, runner sqlgen.Runner, agent Agent) error {
	if err := enabledOwner(ctx, q, runner.OwnerUserID.String); err != nil {
		return err
	}
	if err := enabledOwner(ctx, q, agent.OwnerUserID); err != nil {
		return err
	}
	if runner.TeamShared != 0 || agent.OwnerUserID != "" && agent.OwnerUserID == runner.OwnerUserID.String {
		return nil
	}
	_, err := q.GetRunnerAgentGrant(ctx, sqlgen.GetRunnerAgentGrantParams{RunnerID: runner.ID, AgentID: agent.ID})
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return problem("runner_forbidden", "The Runner owner has not authorized this Agent. Choose a permitted Runner or request an Agent grant.")
}

func resolveAgentRunner(ctx context.Context, q *sqlgen.Queries, room sqlgen.Room, agent Agent) (Runner, error) {
	target := agent.PreferredRunnerID
	if agent.OwnerUserID == "" && target == "" {
		target = room.RunnerID.String
		if target == "" {
			defaults, err := q.GetTeamRunnerDefault(ctx)
			if err != nil {
				return Runner{}, err
			}
			target = defaults.RunnerID.String
		}
	}
	if target == "" {
		return Runner{}, problem("runner_required", "Set an execution Runner for this Agent before assigning work.")
	}
	runner, err := availableRunner(ctx, q, target)
	if err != nil {
		return Runner{}, err
	}
	if err = runnerAllowsAgent(ctx, q, runner, agent); err != nil {
		return Runner{}, err
	}
	return runnerRow(runner)
}

func (s *Store) ResolveAgentRunner(ctx context.Context, roomID, agentID string) (Runner, error) {
	q := s.queries()
	room, err := q.GetRoom(ctx, roomID)
	if err != nil {
		return Runner{}, missing(err, "The room")
	}
	agent, err := q.GetAgent(ctx, agentID)
	if err != nil {
		return Runner{}, missing(err, "The agent")
	}
	return resolveAgentRunner(ctx, q, room, agentRow(agent))
}

// The frozen target is reauthorized, never re-resolved to another machine.
func authorizeRunExecution(ctx context.Context, q *sqlgen.Queries, run Run) error {
	if err := enabledOwner(ctx, q, run.RequestedByUserID); err != nil {
		return err
	}
	row, err := q.GetAgent(ctx, run.AgentID)
	if err != nil || !row.Enabled {
		return problem("invalid_agent", "The Agent is no longer available.")
	}
	agent := agentRow(row)
	if err := enabledOwner(ctx, q, agent.OwnerUserID); err != nil {
		return err
	}
	if _, err = q.GetMembership(ctx, sqlgen.GetMembershipParams{RoomID: run.RoomID, AgentID: run.AgentID}); err != nil {
		return problem("invalid_agent", "The Agent is no longer shared with this Room.")
	}
	if run.AgentSnapshot.Access == "write" && agent.Access != "write" {
		return problem("forbidden", "The Agent no longer has write access.")
	}
	if run.RunnerID == "" {
		return nil
	} // Historical local execution has no fabricated Runner.
	runner, err := availableRunner(ctx, q, run.RunnerID)
	if err != nil {
		return err
	}
	return runnerAllowsAgent(ctx, q, runner, agent)
}
func (s *Store) AuthorizeRunExecution(ctx context.Context, run Run) error {
	return authorizeRunExecution(ctx, s.queries(), run)
}

type ExecutionDefaults struct {
	PersonalRunnerID string `json:"personalRunnerId"`
	PersonalRevision int64  `json:"personalRevision"`
	TeamRunnerID     string `json:"teamRunnerId"`
	TeamRevision     int64  `json:"teamRevision"`
}
type ExecutionDefaultInput struct {
	Scope    string `json:"scope"`
	RunnerID string `json:"runnerId"`
	Revision int64  `json:"revision"`
}

func executionDefaults(ctx context.Context, q *sqlgen.Queries, userID string) (ExecutionDefaults, error) {
	var out ExecutionDefaults
	team, err := q.GetTeamRunnerDefault(ctx)
	if err != nil {
		return out, err
	}
	out.TeamRunnerID, out.TeamRevision = team.RunnerID.String, team.Revision
	personal, err := q.GetPersonalRunnerDefault(ctx, userID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if err == nil {
		out.PersonalRunnerID, out.PersonalRevision = personal.RunnerID.String, personal.Revision
	}
	return out, nil
}
func (s *Store) GetExecutionDefaults(ctx context.Context) (ExecutionDefaults, error) {
	if err := requireActor(ctx, s.queries(), false); err != nil {
		return ExecutionDefaults{}, err
	}
	return executionDefaults(ctx, s.queries(), actorID(ctx))
}
func (s *Store) SetExecutionDefault(ctx context.Context, in ExecutionDefaultInput) (out ExecutionDefaults, err error) {
	if in.Revision < 0 || in.Scope != "personal" && in.Scope != "team" {
		return out, problem("invalid_runner", "Choose personal or team defaults with the current revision.")
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := requireActor(ctx, q, in.Scope == "team"); e != nil {
			return e
		}
		actor := actorID(ctx)
		if in.Scope == "personal" && actor == "" {
			return problem("unauthenticated", "Sign in before changing personal defaults.")
		}
		if in.RunnerID != "" {
			runner, e := availableRunner(ctx, q, in.RunnerID)
			if e != nil {
				return e
			}
			if e = enabledOwner(ctx, q, runner.OwnerUserID.String); e != nil {
				return e
			}
			if runner.TeamShared == 0 && (in.Scope == "team" || runner.OwnerUserID.String != actor) {
				return problem("runner_forbidden", "This default requires your own Runner or a Runner explicitly shared with the team.")
			}
		}
		var changed int64
		var e error
		if in.Scope == "team" {
			changed, e = q.UpdateTeamRunnerDefault(ctx, sqlgen.UpdateTeamRunnerDefaultParams{RunnerID: nullableParent(in.RunnerID), Revision: in.Revision})
		} else {
			if e = q.InitializePersonalRunnerDefault(ctx, sqlgen.InitializePersonalRunnerDefaultParams{UserID: actor}); e != nil {
				return e
			}
			changed, e = q.UpdatePersonalRunnerDefault(ctx, sqlgen.UpdatePersonalRunnerDefaultParams{UserID: actor, RunnerID: nullableParent(in.RunnerID), Revision: in.Revision})
		}
		if e != nil {
			return e
		}
		if changed != 1 {
			return problem("revision_conflict", "The execution default changed. Refresh before saving.")
		}
		out, e = executionDefaults(ctx, q, actor)
		if e != nil {
			return e
		}
		return event(ctx, q, "execution_defaults.updated", "", actor)
	})
	return
}

func (s *Store) RunnerSources(ctx context.Context, id string) ([]RunnerSource, error) {
	runner, err := s.GetRunner(ctx, id)
	if err != nil {
		return nil, err
	}
	if err = requireObjectOwner(ctx, s.queries(), runner.OwnerUserID); err != nil {
		return nil, err
	}
	return runner.Sources, nil
}
func (s *Store) ListRunnerAgentGrants(ctx context.Context, id string) ([]string, error) {
	runner, err := s.queries().GetRunner(ctx, id)
	if err != nil {
		return nil, missing(err, "The runner")
	}
	if err = requireObjectOwner(ctx, s.queries(), runner.OwnerUserID.String); err != nil {
		return nil, err
	}
	return s.queries().ListRunnerAgentGrants(ctx, id)
}
func (s *Store) SetRunnerAgentGrant(ctx context.Context, runnerID, agentID string, allow bool) (out []string, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		runner, e := q.GetRunner(ctx, runnerID)
		if e != nil {
			return missing(e, "The runner")
		}
		if e = requireObjectOwner(ctx, q, runner.OwnerUserID.String); e != nil {
			return e
		}
		if _, e = q.GetAgent(ctx, agentID); e != nil {
			return missing(e, "The agent")
		}
		if allow {
			e = q.CreateRunnerAgentGrant(ctx, sqlgen.CreateRunnerAgentGrantParams{RunnerID: runnerID, AgentID: agentID, CreatedAt: now()})
		} else {
			e = q.DeleteRunnerAgentGrant(ctx, sqlgen.DeleteRunnerAgentGrantParams{RunnerID: runnerID, AgentID: agentID})
		}
		if e != nil {
			return e
		}
		if !allow {
			if e = revokeUnauthorizedRunnerRuns(ctx, q, runnerID); e != nil {
				return e
			}
		}
		out, e = q.ListRunnerAgentGrants(ctx, runnerID)
		if e != nil {
			return e
		}
		return event(ctx, q, "runner.updated", "", runnerID)
	})
	return
}
func revokeUnauthorizedRunnerRuns(ctx context.Context, q *sqlgen.Queries, runnerID string) error {
	return revokeRuns(ctx, q, "The Runner execution grant was revoked.", func(run Run) bool {
		return run.RunnerID == runnerID && authorizeRunExecution(ctx, q, run) != nil
	})
}

func revokeOwnedExecution(ctx context.Context, q *sqlgen.Queries, userID string) error {
	if err := q.RevokeOwnerEnrollments(ctx, nullableParent(userID)); err != nil {
		return err
	}
	agents, err := q.ListAgents(ctx)
	if err != nil {
		return err
	}
	ownedAgents := map[string]bool{}
	for _, agent := range agents {
		if agent.OwnerUserID.String == userID {
			ownedAgents[agent.ID] = true
		}
	}
	runners, err := q.ListRunners(ctx)
	if err != nil {
		return err
	}
	ownedRunners := map[string]bool{}
	for _, runner := range runners {
		if runner.OwnerUserID.String == userID {
			ownedRunners[runner.ID] = true
			if err = q.UpdateRunner(ctx, sqlgen.UpdateRunnerParams{ID: runner.ID, Name: runner.Name, Enabled: 0, Revoked: 1, UpdatedAt: now()}); err != nil {
				return err
			}
			if err = event(ctx, q, "runner.updated", "", runner.ID); err != nil {
				return err
			}
		}
	}
	if err = revokeRuns(ctx, q, "The requester, Agent owner, or Runner owner was disabled.", func(run Run) bool {
		return run.RequestedByUserID == userID || ownedAgents[run.AgentID] || ownedRunners[run.RunnerID]
	}); err != nil {
		return err
	}
	return revokeSourceImports(ctx, q, "The source owner or importing member was disabled.", func(item SourceImport) bool { return item.RequestedByUserID == userID || ownedRunners[item.RunnerID] })
}
