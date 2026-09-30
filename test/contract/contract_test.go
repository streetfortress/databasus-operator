// Package contract verifies the internal/client API surface against a real
// databasus instance — the one pinned in docker-compose.yaml. Run via
// `make test-contract`; without DATABASUS_URL set the package is a no-op so
// that plain `make test` stays hermetic.
package contract

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	dbclient "github.com/streetfortress/databasus-operator/internal/client"
)

const (
	adminEmail    = "admin"
	adminPassword = "contract-test-password"
)

var c *dbclient.DatabasusClient

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}

	return fallback
}

// Target database connection details, as seen FROM the databasus container
// (docker-compose service name, not localhost).
var (
	targetPgHost     = envOr("TARGET_PG_HOST", "target-postgres")
	targetPgDatabase = envOr("TARGET_PG_DATABASE", "appdb")
	targetPgUser     = envOr("TARGET_PG_USER", "app")
	targetPgPassword = envOr("TARGET_PG_PASSWORD", "app_password_123")
)

func TestMain(m *testing.M) {
	baseURL := os.Getenv("DATABASUS_URL")
	if baseURL == "" {
		fmt.Println("DATABASUS_URL not set; skipping contract tests")
		return
	}

	if err := bootstrap(baseURL); err != nil {
		fmt.Fprintf(os.Stderr, "contract test bootstrap failed: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

// bootstrap waits for the instance, claims the seeded passwordless "admin"
// user, signs in, and ensures a workspace exists.
func bootstrap(baseURL string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if err := waitForHealth(ctx, baseURL); err != nil {
		return err
	}

	// First boot only; on an already-claimed instance this returns 400
	// ("admin password is already set"), which is fine for local re-runs.
	if err := postJSON(ctx, baseURL+"/api/v1/users/admin/set-password", "",
		map[string]string{"password": adminPassword}); err != nil {
		fmt.Printf("set-password: %v (continuing, assuming already set)\n", err)
	}

	token, err := dbclient.SignIn(ctx, baseURL, adminEmail, adminPassword)
	if err != nil {
		return fmt.Errorf("sign in: %w", err)
	}

	// A fresh instance has no workspaces; create one, tolerating failure if
	// a previous run already did.
	if err := postJSON(ctx, baseURL+"/api/v1/workspaces", token,
		map[string]string{"name": "contract-tests"}); err != nil {
		fmt.Printf("create workspace: %v (continuing, assuming it exists)\n", err)
	}

	bootstrapClient := dbclient.New(dbclient.Config{BaseURL: baseURL, Token: token})

	workspaceID, err := bootstrapClient.ResolveWorkspace(ctx, "")
	if err != nil {
		return fmt.Errorf("resolve workspace: %w", err)
	}

	c = dbclient.New(dbclient.Config{
		BaseURL:     baseURL,
		Token:       token,
		WorkspaceID: workspaceID,
	})

	return nil
}

func waitForHealth(ctx context.Context, baseURL string) error {
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v1/system/health", nil)

		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("databasus did not become healthy at %s: %w", baseURL, ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
}

// postJSON is a bootstrap-only helper for the two endpoints the operator's
// client intentionally does not wrap (admin claim, workspace creation).
func postJSON(ctx context.Context, url, token string, body any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("POST %s returned status %d", url, resp.StatusCode)
	}

	return nil
}

func TestHealthCheck(t *testing.T) {
	if err := c.HealthCheck(t.Context()); err != nil {
		t.Fatalf("health check failed: %v", err)
	}
}

func TestWorkspaceResolved(t *testing.T) {
	if c.WorkspaceID() == "" {
		t.Fatal("bootstrap resolved an empty workspace ID")
	}
}

func TestNotifierLifecycle(t *testing.T) {
	ctx := t.Context()

	created, err := c.SaveNotifier(ctx, &dbclient.NotifierRequest{
		Name:         "contract-webhook",
		NotifierType: "WEBHOOK",
		WebhookNotifier: &dbclient.WebhookRequest{
			WebhookURL:    "http://example.invalid/hook",
			WebhookMethod: "POST",
		},
	})
	if err != nil {
		t.Fatalf("SaveNotifier: %v", err)
	}

	if created.ID == "" {
		t.Fatal("SaveNotifier returned an empty ID")
	}

	fetched, err := c.GetNotifier(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetNotifier: %v", err)
	}

	if fetched == nil || fetched.Name != "contract-webhook" || fetched.NotifierType != "WEBHOOK" {
		t.Fatalf("GetNotifier returned unexpected notifier: %+v", fetched)
	}

	if err := c.DeleteNotifier(ctx, created.ID); err != nil {
		t.Fatalf("DeleteNotifier: %v", err)
	}

	gone, err := c.GetNotifier(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetNotifier after delete: %v", err)
	}

	if gone != nil {
		t.Fatalf("notifier still present after delete: %+v", gone)
	}
}

func TestStorageAndDatabaseBackupLifecycle(t *testing.T) {
	ctx := t.Context()

	// Storage save validates fields but does not test connectivity
	// (that is a separate endpoint), so dummy S3 credentials are fine.
	storage, err := c.SaveStorage(ctx, &dbclient.StorageRequest{
		Type: "S3",
		Name: "contract-s3",
		S3Storage: &dbclient.S3Request{
			S3Bucket:    "contract-bucket",
			S3Region:    "us-east-1",
			S3AccessKey: "contract-access-key",
			S3SecretKey: "contract-secret-key",
			S3Endpoint:  "http://s3.example.invalid",
		},
	})
	if err != nil {
		t.Fatalf("SaveStorage: %v", err)
	}

	if storage.ID == "" {
		t.Fatal("SaveStorage returned an empty ID")
	}

	t.Cleanup(func() {
		_ = c.DeleteStorage(context.Background(), storage.ID)
	})

	// Database creation connects to the target to detect the version, so
	// this exercises the full POSTGRES_LOGICAL request shape end to end.
	db, err := c.CreateDatabase(ctx, &dbclient.DatabaseRequest{
		Name: "contract-db",
		Type: dbclient.DatabaseTypePostgresLogical,
		PostgresqlLogical: &dbclient.PostgresqlLogicalRequest{
			Host:     targetPgHost,
			Port:     5432,
			Username: targetPgUser,
			Password: targetPgPassword,
			Database: &targetPgDatabase,
			SslMode:  "disable",
			CpuCount: 1,
		},
	})
	if err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	t.Cleanup(func() {
		_ = c.DeleteDatabase(context.Background(), db.ID)
	})

	if db.Type != dbclient.DatabaseTypePostgresLogical {
		t.Fatalf("CreateDatabase returned type %q, want %q", db.Type, dbclient.DatabaseTypePostgresLogical)
	}

	fetched, err := c.GetDatabase(ctx, db.ID)
	if err != nil {
		t.Fatalf("GetDatabase: %v", err)
	}

	if fetched == nil || fetched.Name != "contract-db" {
		t.Fatalf("GetDatabase returned unexpected database: %+v", fetched)
	}

	timeOfDay := "03:00"

	if _, err := c.SaveBackupConfig(ctx, &dbclient.BackupConfigRequest{
		DatabaseID:          db.ID,
		IsBackupsEnabled:    true,
		RetentionPolicyType: "COUNT",
		RetentionCount:      3,
		Storage:             &dbclient.StorageRef{ID: storage.ID},
		BackupInterval: &dbclient.IntervalRequest{
			Type:      "DAILY",
			TimeOfDay: &timeOfDay,
		},
		SendNotificationsOn: []string{},
		MaxFailedTriesCount: 1,
		Encryption:          "NONE",
	}); err != nil {
		t.Fatalf("SaveBackupConfig: %v", err)
	}

	backupCfg, err := c.GetBackupConfig(ctx, db.ID)
	if err != nil {
		t.Fatalf("GetBackupConfig: %v", err)
	}

	if backupCfg == nil || !backupCfg.IsBackupsEnabled {
		t.Fatalf("GetBackupConfig returned unexpected config: %+v", backupCfg)
	}

	if backupCfg.StorageID == nil || *backupCfg.StorageID != storage.ID {
		t.Fatalf("GetBackupConfig storageId = %v, want %s", backupCfg.StorageID, storage.ID)
	}

	if _, err := c.SaveHealthcheckConfig(ctx, &dbclient.HealthcheckConfigRequest{
		DatabaseID:                        db.ID,
		IsHealthcheckEnabled:              true,
		IntervalMinutes:                   1,
		AttemptsBeforeConcideredAsDown:    3,
		StoreAttemptsDays:                 7,
		IsSentNotificationWhenUnavailable: false,
	}); err != nil {
		t.Fatalf("SaveHealthcheckConfig: %v", err)
	}

	hc, err := c.GetHealthcheckConfig(ctx, db.ID)
	if err != nil {
		t.Fatalf("GetHealthcheckConfig: %v", err)
	}

	if hc == nil || !hc.IsHealthcheckEnabled {
		t.Fatalf("GetHealthcheckConfig returned unexpected config: %+v", hc)
	}

	// Update pass: rename and confirm the change round-trips.
	updated, err := c.UpdateDatabase(ctx, &dbclient.DatabaseRequest{
		ID:   db.ID,
		Name: "contract-db-renamed",
		Type: dbclient.DatabaseTypePostgresLogical,
		PostgresqlLogical: &dbclient.PostgresqlLogicalRequest{
			Host:     targetPgHost,
			Port:     5432,
			Username: targetPgUser,
			Password: targetPgPassword,
			Database: &targetPgDatabase,
			SslMode:  "disable",
			CpuCount: 1,
		},
	})
	if err != nil {
		t.Fatalf("UpdateDatabase: %v", err)
	}

	if updated.Name != "contract-db-renamed" {
		t.Fatalf("UpdateDatabase returned name %q, want contract-db-renamed", updated.Name)
	}

	if err := c.DeleteDatabase(ctx, db.ID); err != nil {
		t.Fatalf("DeleteDatabase: %v", err)
	}

	goneDB, err := c.GetDatabase(ctx, db.ID)
	if err != nil {
		t.Fatalf("GetDatabase after delete: %v", err)
	}

	if goneDB != nil {
		t.Fatalf("database still present after delete: %+v", goneDB)
	}

	if err := c.DeleteStorage(ctx, storage.ID); err != nil {
		t.Fatalf("DeleteStorage: %v", err)
	}

	goneStorage, err := c.GetStorage(ctx, storage.ID)
	if err != nil {
		t.Fatalf("GetStorage after delete: %v", err)
	}

	if goneStorage != nil {
		t.Fatalf("storage still present after delete: %+v", goneStorage)
	}
}
