package scheduler_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"w8nc/internal/db"
	"w8nc/internal/models"
	"w8nc/internal/pinger"
	"w8nc/internal/scheduler"
)

func TestSchedulerNotifyOnceDeactivatesAndCreatesOneEvent(t *testing.T) {
	store := integrationStore(t)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer target.Close()

	endpoint, err := store.CreateEndpoint(context.Background(), db.EndpointRecord{
		URL:                  target.URL,
		HTTPMethod:           "GET",
		Headers:              []models.Header{},
		PingIntervalSeconds:  5,
		NotifyCondition:      condition("status_code_equals", 200),
		NotificationTemplate: "{{status_code}} {{url}}",
		Active:               true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.DeleteEndpoint(context.Background(), endpoint.ID) }()

	service := &scheduler.Service{
		Store:       store,
		Pinger:      pinger.New(2*time.Second, 1024, true, nil),
		Concurrency: 2,
		LockFor:     5 * time.Second,
		Logger:      slog.Default(),
	}
	if err := service.RunDueOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	updated, err := store.GetEndpoint(context.Background(), endpoint.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Active || updated.State != "deactivated" || updated.NotifiedAt == nil {
		t.Fatalf("endpoint was not notify-once deactivated: active=%v state=%s notified=%v", updated.Active, updated.State, updated.NotifiedAt)
	}
	if count := notificationEventCount(t, store, endpoint.ID); count != 1 {
		t.Fatalf("notification events=%d, want 1", count)
	}
	if err := service.RunDueOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if count := notificationEventCount(t, store, endpoint.ID); count != 1 {
		t.Fatalf("notification event duplicated: %d", count)
	}
}

func TestEndpointIntervalEditRecomputesNextRun(t *testing.T) {
	store := integrationStore(t)
	endpoint, err := store.CreateEndpoint(context.Background(), db.EndpointRecord{
		URL:                  "https://example.com",
		HTTPMethod:           "GET",
		Headers:              []models.Header{},
		PingIntervalSeconds:  15,
		NotifyCondition:      condition("status_code_changed", nil),
		NotificationTemplate: models.DefaultNotificationTemplate,
		Active:               true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.DeleteEndpoint(context.Background(), endpoint.ID) }()
	if endpoint.NextRunAt == nil {
		t.Fatal("created active endpoint did not get next_run_at")
	}

	updated, err := store.UpdateEndpoint(context.Background(), endpoint.ID, db.EndpointRecord{
		URL:                  endpoint.URL,
		HTTPMethod:           endpoint.HTTPMethod,
		Headers:              endpoint.Headers,
		PingIntervalSeconds:  60,
		NotifyCondition:      endpoint.NotifyCondition,
		NotificationTemplate: endpoint.NotificationTemplate,
		Active:               true,
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if updated.PingIntervalSeconds != 60 || updated.NextRunAt == nil {
		t.Fatalf("interval update failed: seconds=%d next=%v", updated.PingIntervalSeconds, updated.NextRunAt)
	}
}

func TestManualPingInactiveEndpointRecordsLastResult(t *testing.T) {
	store := integrationStore(t)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer target.Close()

	endpoint, err := store.CreateEndpoint(context.Background(), db.EndpointRecord{
		URL:                  target.URL,
		HTTPMethod:           "GET",
		Headers:              []models.Header{},
		PingIntervalSeconds:  30,
		NotifyCondition:      condition("status_code_changed", nil),
		NotificationTemplate: models.DefaultNotificationTemplate,
		Active:               false,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.DeleteEndpoint(context.Background(), endpoint.ID) }()

	service := &scheduler.Service{
		Store:       store,
		Pinger:      pinger.New(2*time.Second, 1024, true, nil),
		Concurrency: 1,
		LockFor:     5 * time.Second,
		Logger:      slog.Default(),
	}
	if err := service.PingNow(context.Background(), endpoint); err != nil {
		t.Fatal(err)
	}

	updated, err := store.GetEndpoint(context.Background(), endpoint.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Active {
		t.Fatal("manual ping reactivated the endpoint")
	}
	if updated.State != "live" {
		t.Fatalf("state=%s, want live", updated.State)
	}
	if updated.NextRunAt != nil {
		t.Fatalf("next_run_at=%v, want nil", updated.NextRunAt)
	}
	if updated.LastStatusCode == nil || *updated.LastStatusCode != http.StatusOK {
		t.Fatalf("last_status_code=%v, want %d", updated.LastStatusCode, http.StatusOK)
	}
}

func notificationEventCount(t *testing.T, store *db.Store, endpointID string) int {
	t.Helper()
	var count int
	if err := store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM notification_events WHERE endpoint_id=$1`, endpointID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func integrationStore(t *testing.T) *db.Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run Postgres integration tests")
	}
	store, err := db.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	if err := store.Migrate(context.Background(), "../../migrations"); err != nil {
		t.Fatal(err)
	}
	return store
}

func condition(kind string, value any) models.Condition {
	raw, _ := json.Marshal(value)
	if value == nil {
		raw = nil
	}
	return models.Condition{Type: kind, Value: raw}
}
