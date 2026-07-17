package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/imprun/windforce-lite/internal/catalog"
	"github.com/imprun/windforce-lite/internal/contract"
	"github.com/imprun/windforce-lite/internal/event"
	"github.com/imprun/windforce-lite/internal/webhook"
)

func TestLocalReleaseCatalogPublishesAtomically(t *testing.T) {
	store := NewLocalStore(filepath.Join(t.TempDir(), "state.json"))
	ctx := context.Background()
	releasedAt := time.Date(2026, 7, 16, 9, 30, 0, 0, time.UTC)
	deployment := releaseCatalogDeployment("workspace-a", "source-a", "echo", "commit-a")

	published, err := store.PublishRelease(ctx, deployment, releasedAt)
	if err != nil {
		t.Fatal(err)
	}
	if published.UpdatedAt == nil || !published.UpdatedAt.Equal(releasedAt) {
		t.Fatalf("published updatedAt = %v, want %v", published.UpdatedAt, releasedAt)
	}
	snapshot, err := store.LoadCatalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.History) != 1 {
		t.Fatalf("history count = %d, want 1", len(snapshot.History))
	}
	if len(snapshot.Audit) != 1 {
		t.Fatalf("audit count = %d, want 1", len(snapshot.Audit))
	}
	if snapshot.Audit[0].ID != snapshot.History[0].ID || snapshot.Audit[0].Kind != "release_published" {
		t.Fatalf("release audit = %#v, history = %#v", snapshot.Audit[0], snapshot.History[0])
	}
	marker := snapshot.SourceMarkers[catalog.SourceReleaseKey("workspace-a", "source-a")]
	if marker.Commit != "commit-a" || !marker.ReleasedAt.Equal(releasedAt) {
		t.Fatalf("source marker = %#v", marker)
	}

	override := "priority"
	if _, err := store.SetAppTagOverride(ctx, "workspace-a", "echo", &override); err != nil {
		t.Fatal(err)
	}
	snapshot, err = store.LoadCatalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	active := snapshot.Deployments[catalog.DeploymentKey("workspace-a", "echo")]
	if active.TagOverride == nil || *active.TagOverride != override {
		t.Fatalf("active tag override = %v", active.TagOverride)
	}
	if snapshot.History[0].Deployment.TagOverride != nil {
		t.Fatalf("release history was mutated: %#v", snapshot.History[0].Deployment.TagOverride)
	}
}

func TestLocalReleaseCatalogImportIsIdempotent(t *testing.T) {
	ctx := context.Background()
	source := NewLocalStore(filepath.Join(t.TempDir(), "source-state.json"))
	if _, err := source.PublishRelease(ctx, releaseCatalogDeployment("workspace-a", "source-a", "echo", "commit-a"), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := source.AppendAudit(ctx, catalog.AuditRecord{
		Workspace:   "workspace-a",
		GitSourceID: "source-a",
		App:         "echo",
		Kind:        "source_updated",
		Actor:       "tester",
	}); err != nil {
		t.Fatal(err)
	}
	imported, err := source.LoadCatalog(ctx)
	if err != nil {
		t.Fatal(err)
	}

	target := NewLocalStore(filepath.Join(t.TempDir(), "target-state.json"))
	if err := target.ImportCatalog(ctx, imported); err != nil {
		t.Fatal(err)
	}
	if err := target.ImportCatalog(ctx, imported); err != nil {
		t.Fatal(err)
	}
	got, err := target.LoadCatalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Deployments) != 1 || len(got.History) != 1 || len(got.Audit) != 2 || len(got.SourceMarkers) != 1 {
		t.Fatalf("idempotent import counts = deployments:%d history:%d audit:%d markers:%d", len(got.Deployments), len(got.History), len(got.Audit), len(got.SourceMarkers))
	}
}

func TestPostgresReleaseCatalogContract(t *testing.T) {
	dsn := os.Getenv("WINDFORCE_LITE_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("WINDFORCE_LITE_POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	store := openIsolatedPostgresCatalogStore(t, dsn)
	releasedAt := time.Date(2026, 7, 16, 10, 30, 0, 0, time.UTC)
	deployment := releaseCatalogDeployment("workspace-a", "source-a", "echo", "commit-a")
	actor := "operator@example.test"
	message := "Release contract test"
	deployment.CreatedBy = &actor
	deployment.Message = &message

	published, err := store.PublishRelease(ctx, deployment, releasedAt)
	if err != nil {
		t.Fatal(err)
	}
	if published.UpdatedAt == nil || !published.UpdatedAt.Equal(releasedAt) {
		t.Fatalf("published updatedAt = %v, want %v", published.UpdatedAt, releasedAt)
	}
	snapshot, err := store.LoadCatalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Deployments) != 1 || len(snapshot.History) != 1 || len(snapshot.Audit) != 1 || len(snapshot.SourceMarkers) != 1 {
		t.Fatalf("published counts = deployments:%d history:%d audit:%d markers:%d", len(snapshot.Deployments), len(snapshot.History), len(snapshot.Audit), len(snapshot.SourceMarkers))
	}
	if snapshot.Audit[0].ID != snapshot.History[0].ID || snapshot.Audit[0].Actor != actor || snapshot.Audit[0].Detail != message {
		t.Fatalf("release audit = %#v, history = %#v", snapshot.Audit[0], snapshot.History[0])
	}
	marker := snapshot.SourceMarkers[catalog.SourceReleaseKey("workspace-a", "source-a")]
	if marker.Commit != "commit-a" || !marker.ReleasedAt.Equal(releasedAt) {
		t.Fatalf("source marker = %#v", marker)
	}

	if err := store.ImportCatalog(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := store.ImportCatalog(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadCatalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Deployments) != 1 || len(got.History) != 1 || len(got.Audit) != 1 || len(got.SourceMarkers) != 1 {
		t.Fatalf("idempotent import counts = deployments:%d history:%d audit:%d markers:%d", len(got.Deployments), len(got.History), len(got.Audit), len(got.SourceMarkers))
	}
}

func TestPostgresReleaseCandidateAndSourceOperationLeaseContract(t *testing.T) {
	dsn := os.Getenv("WINDFORCE_LITE_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("WINDFORCE_LITE_POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	store := openIsolatedPostgresCatalogStore(t, dsn)
	first := releaseCatalogDeployment("workspace-a", "source-a", "echo", "commit-a")
	firstSyncedAt := time.Date(2026, 7, 17, 2, 0, 0, 0, time.UTC)
	if _, err := store.SaveReleaseCandidate(ctx, first, firstSyncedAt); err != nil {
		t.Fatal(err)
	}
	changed := first
	changed.Entrypoint = "changed.py"
	saved, err := store.SaveReleaseCandidate(ctx, changed, firstSyncedAt.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if saved.Deployment.Entrypoint != first.Entrypoint || !saved.SyncedAt.Equal(firstSyncedAt) {
		t.Fatalf("immutable candidate = %#v", saved)
	}

	second := releaseCatalogDeployment("workspace-a", "source-a", "echo", "commit-b")
	if _, err := store.SaveReleaseCandidate(ctx, second, firstSyncedAt.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	latest, err := store.GetLatestReleaseCandidate(ctx, "workspace-a", "source-a")
	if err != nil || latest.Deployment.Commit != "commit-b" {
		t.Fatalf("latest candidate = %#v, err=%v", latest, err)
	}

	acquired, err := store.AcquireSourceOperationLease(ctx, "workspace-a", "source-a", "holder-a", time.Minute)
	if err != nil || !acquired {
		t.Fatalf("holder-a acquire = %t, err=%v", acquired, err)
	}
	acquired, err = store.AcquireSourceOperationLease(ctx, "workspace-a", "source-a", "holder-b", time.Minute)
	if err != nil || acquired {
		t.Fatalf("holder-b competing acquire = %t, err=%v", acquired, err)
	}
	renewed, err := store.RenewSourceOperationLease(ctx, "workspace-a", "source-a", "holder-a", time.Minute)
	if err != nil || !renewed {
		t.Fatalf("holder-a renew = %t, err=%v", renewed, err)
	}
	if err := store.ReleaseSourceOperationLease(ctx, "workspace-a", "source-a", "holder-a"); err != nil {
		t.Fatal(err)
	}
	acquired, err = store.AcquireSourceOperationLease(ctx, "workspace-a", "source-a", "holder-b", time.Minute)
	if err != nil || !acquired {
		t.Fatalf("holder-b acquire after release = %t, err=%v", acquired, err)
	}
}

func TestPostgresMigrateSerializesConcurrentProcesses(t *testing.T) {
	dsn := os.Getenv("WINDFORCE_LITE_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("WINDFORCE_LITE_POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	admin, err := OpenPostgresStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "concurrent_migration_" + strings.ReplaceAll(time.Now().UTC().Format("20060102150405_000000000"), ".", "")
	if _, err := admin.pool.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	stores := make([]*PostgresStore, 3)
	for index := range stores {
		stores[index], err = OpenPostgresStore(ctx, dsn+separator+"search_path="+schema)
		if err != nil {
			for _, store := range stores {
				if store != nil {
					store.Close()
				}
			}
			_, _ = admin.pool.Exec(ctx, `DROP SCHEMA `+schema+` CASCADE`)
			admin.Close()
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, store := range stores {
			store.Close()
		}
		_, _ = admin.pool.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
	})

	errorsCh := make(chan error, len(stores))
	var group sync.WaitGroup
	for _, store := range stores {
		group.Add(1)
		go func(store *PostgresStore) {
			defer group.Done()
			errorsCh <- store.Migrate(ctx)
		}(store)
	}
	group.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatalf("concurrent migrate: %v", err)
		}
	}
}

func TestPostgresReleasePublicationRollsBackAllWrites(t *testing.T) {
	dsn := os.Getenv("WINDFORCE_LITE_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("WINDFORCE_LITE_POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()

	writeTables := []string{
		"control_release_history",
		"control_active_release",
		"control_source_release_marker",
		"control_audit",
		"control_plane_event",
		"webhook_delivery",
	}
	for _, failedTable := range writeTables {
		t.Run(failedTable, func(t *testing.T) {
			store := openIsolatedPostgresCatalogStore(t, dsn)
			store.ConfigureInputCrypto("postgres-test-secret-key", "")
			if _, err := store.CreateSubscription(ctx, webhook.Subscription{
				WorkspaceID:   "workspace-a",
				Name:          "Release rollback",
				Endpoint:      "https://hooks.example.test/releases",
				SigningSecret: "signing-secret-0123456789",
				EventTypes:    []string{event.ReleasePublishedType},
				Enabled:       true,
				CreatedBy:     "tester",
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := store.pool.Exec(ctx, `
CREATE FUNCTION reject_release_write() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'release write rejected by test';
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER reject_release_write
BEFORE INSERT ON `+failedTable+`
FOR EACH ROW EXECUTE FUNCTION reject_release_write();
`); err != nil {
				t.Fatal(err)
			}

			_, err := store.PublishRelease(ctx, releaseCatalogDeployment("workspace-a", "source-a", "echo", "commit-a"), time.Now().UTC())
			if err == nil {
				t.Fatalf("PublishRelease succeeded despite %s failure", failedTable)
			}
			if _, err := store.GetDeploymentForWorkspace(ctx, "workspace-a", "echo"); !errors.Is(err, catalog.ErrDeploymentNotFound) {
				t.Fatalf("active release error = %v, want ErrDeploymentNotFound", err)
			}
			for _, table := range writeTables {
				var count int
				if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Fatalf("%s count after %s rollback = %d", table, failedTable, count)
				}
			}
		})
	}
}

func openIsolatedPostgresCatalogStore(t *testing.T, dsn string) *PostgresStore {
	t.Helper()
	ctx := context.Background()
	admin, err := OpenPostgresStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "release_catalog_" + strings.ReplaceAll(time.Now().UTC().Format("20060102150405_000000000"), ".", "")
	if _, err := admin.pool.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	store, err := OpenPostgresStore(ctx, dsn+separator+"search_path="+schema)
	if err != nil {
		_, _ = admin.pool.Exec(ctx, `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
		t.Fatal(err)
	}
	if err := store.Migrate(ctx); err != nil {
		store.Close()
		_, _ = admin.pool.Exec(ctx, `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		store.Close()
		_, _ = admin.pool.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
	})
	return store
}

func releaseCatalogDeployment(workspace string, sourceID string, app string, commit string) contract.Deployment {
	return contract.Deployment{
		Workspace:   workspace,
		GitSourceID: sourceID,
		App:         app,
		Commit:      commit,
		Entrypoint:  "main.py",
		ObjectURI:   "bundle://" + workspace + "/" + sourceID + "/" + commit,
		Actions: map[string]contract.Action{
			"run": {Action: "run"},
		},
	}
}
