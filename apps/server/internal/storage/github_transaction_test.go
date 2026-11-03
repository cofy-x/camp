package storage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestGitHubPayloadValidationDoesNotWaitForDatabaseConnection(t *testing.T) {
	s, actor, _, room, _, _ := taskFixture(t)
	source := testGitHubSource(t, s, actor, room)
	connection, err := s.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	request, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_, _, err = s.ReceiveGitHubDelivery(request, source.ID, source.SecretRef, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "issues", []byte("{"))
	var invalid *Error
	if !errors.As(err, &invalid) || invalid.Code != "invalid_source" {
		t.Fatal("payload validation waited for the occupied connection instead of rejecting its bytes", err)
	}
}

func TestPreparedGitHubDeliveryRechecksSourceAfterWaitingForConnection(t *testing.T) {
	for _, test := range []struct {
		name string
		sql  string
		code string
	}{
		{"disabled", "UPDATE github_sources SET enabled=0,revision=revision+1", "source_unavailable"},
		{"rotated", "UPDATE github_sources SET secret_ref='" + strings.Repeat("b", 48) + "',revision=revision+1", "source_unavailable"},
		{"repository pinned", "UPDATE github_sources SET repository_id=999", "source_conflict"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, actor, _, room, _, _ := taskFixture(t)
			source := testGitHubSource(t, s, actor, room)
			connection, err := s.db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			request, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			waits := s.db.Stats().WaitCount
			result := make(chan error, 1)
			go func() {
				_, _, err := s.ReceiveGitHubDelivery(request, source.ID, source.SecretRef, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "issues", testGitHubPayload("Original", "Prepared before source changes."))
				result <- err
			}()
			ticker := time.NewTicker(time.Millisecond)
			defer ticker.Stop()
			for s.db.Stats().WaitCount == waits {
				select {
				case err := <-result:
					t.Fatal("delivery did not wait for the exclusively held database connection", err)
				case <-request.Done():
					t.Fatal(request.Err())
				case <-ticker.C:
				}
			}
			// Preparation has completed and the write is waiting for its connection.
			// Change authority through that connection before allowing admission.
			if _, err = connection.ExecContext(request, test.sql); err != nil {
				t.Fatal(err)
			}
			if err = connection.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case err = <-result:
			case <-request.Done():
				t.Fatal(request.Err())
			}
			var rejected *Error
			if !errors.As(err, &rejected) || rejected.Code != test.code {
				t.Fatal("prepared payload bypassed the current source authority", err)
			}
			assertScalar(t, s, "SELECT count(*) FROM github_events", 0)
			assertScalar(t, s, "SELECT count(*) FROM github_deliveries", 0)
			assertScalar(t, s, "SELECT count(*) FROM automation_events", 0)
		})
	}
}
