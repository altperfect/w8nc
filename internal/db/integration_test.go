package db

import (
	"context"
	"os"
	"testing"
	"time"

	"bug-bounty-endpoint-pinger/internal/models"
)

func TestPostgresMigrationsAndDueSelection(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run Postgres integration tests")
	}
	ctx := context.Background()
	store, err := Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	endpoint, err := store.CreateEndpoint(ctx, EndpointRecord{
		URL:                  "https://example.com",
		HTTPMethod:           "GET",
		Headers:              []models.Header{},
		PingIntervalSeconds:  5,
		NotifyCondition:      models.Condition{Type: "status_code_changed"},
		NotificationTemplate: models.DefaultNotificationTemplate,
		Active:               true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.DeleteEndpoint(ctx, endpoint.ID) }()
	due, err := store.LockDueEndpoints(ctx, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) == 0 {
		t.Fatal("expected created active endpoint to be due immediately")
	}
}

func TestDeactivateExpiredEndpoints(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run Postgres integration tests")
	}
	ctx := context.Background()
	store, err := Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	deactivateAfter := 60
	endpoint, err := store.CreateEndpoint(ctx, EndpointRecord{
		URL:                    "https://example.com/expire",
		HTTPMethod:             "GET",
		Headers:                []models.Header{},
		PingIntervalSeconds:    5,
		DeactivateAfterSeconds: &deactivateAfter,
		NotifyCondition:        models.Condition{Type: "status_code_changed"},
		NotificationTemplate:   models.DefaultNotificationTemplate,
		Active:                 true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.DeleteEndpoint(ctx, endpoint.ID) }()
	if endpoint.DeactivateAt == nil {
		t.Fatal("created endpoint did not get deactivate_at")
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE endpoints SET deactivate_at=now()-interval '1 second' WHERE id=$1`, endpoint.ID); err != nil {
		t.Fatal(err)
	}
	affected, err := store.DeactivateExpiredEndpoints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if affected == 0 {
		t.Fatal("expected expired endpoint to be deactivated")
	}
	updated, err := store.GetEndpoint(ctx, endpoint.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Active || updated.State != "deactivated" || updated.DeactivatedReason == nil || *updated.DeactivatedReason != "time_limit_expired" {
		t.Fatalf("expired endpoint state: active=%v state=%s reason=%v", updated.Active, updated.State, updated.DeactivatedReason)
	}
}

func TestResumeAfterDowntimeExtendsActiveDeactivateDeadlines(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run Postgres integration tests")
	}
	ctx := context.Background()
	store, err := Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx, "../../migrations"); err != nil {
		t.Fatal(err)
	}

	deactivateAfter := 120
	endpoint, err := store.CreateEndpoint(ctx, EndpointRecord{
		URL:                    "https://example.com/resume",
		HTTPMethod:             "GET",
		Headers:                []models.Header{},
		PingIntervalSeconds:    5,
		DeactivateAfterSeconds: &deactivateAfter,
		NotifyCondition:        models.Condition{Type: "status_code_changed"},
		NotificationTemplate:   models.DefaultNotificationTemplate,
		Active:                 true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.DeleteEndpoint(ctx, endpoint.ID) }()

	now := time.Now().UTC()
	if _, err := store.Pool.Exec(ctx, `UPDATE endpoints SET deactivate_at=$2 WHERE id=$1`, endpoint.ID, now.Add(-30*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkRuntimeSeen(ctx, now.Add(-60*time.Second)); err != nil {
		t.Fatal(err)
	}

	resume, err := store.ResumeAfterDowntime(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if resume.ExtendedEndpoints == 0 {
		t.Fatal("expected active endpoint deadline to be extended")
	}
	if affected, err := store.DeactivateExpiredEndpoints(ctx); err != nil {
		t.Fatal(err)
	} else if affected != 0 {
		t.Fatalf("expired endpoints=%d, want 0 after downtime resume", affected)
	}

	updated, err := store.GetEndpoint(ctx, endpoint.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Active {
		t.Fatal("endpoint was deactivated after downtime resume")
	}
	if updated.DeactivateAt == nil || !updated.DeactivateAt.After(now) {
		t.Fatalf("deactivate_at=%v, want after %v", updated.DeactivateAt, now)
	}
}
