// Package testutil wires up an isolated Postgres test database and an
// in-process Fiber app (via app.Test, no real network listener) for the
// integration test suite under /tests. It deliberately never touches the
// shared dev "sales_system" database — everything here targets
// "sales_system_test", created on first use if it doesn't already exist.
package testutil

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/config"
	"github.com/igeargeek/sales-system-api/internal/database"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/routes"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

const testDBName = "sales_system_test"

// TestPassword is the plaintext password used for every user CreateUser seeds,
// so login tests can exercise the real bcrypt-check path.
const TestPassword = "password123!"

// tables lists every table AutoMigrate creates (internal/database/database.go),
// in FK-safe order doesn't matter because of CASCADE, but kept aligned for clarity.
var tables = []string{
	"audit_log_entries",
	"projects",
	"customer_products",
	"products",
	"contracts",
	"tasks",
	"payments",
	"quotes",
	"tags",
	"activities",
	"deals",
	"contacts",
	"companies",
	"leads",
	"users",
}

var (
	once    sync.Once
	testDB  *gorm.DB
	testCfg *config.Config
)

func getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func buildConfig() *config.Config {
	return &config.Config{
		AppEnv:      "test",
		Port:        "0",
		DBHost:      getenv("TEST_DB_HOST", "localhost"),
		DBPort:      getenv("TEST_DB_PORT", "5432"),
		DBUser:      getenv("TEST_DB_USER", "postgres"),
		DBPassword:  getenv("TEST_DB_PASSWORD", "postgres"),
		DBName:      testDBName,
		DBSSLMode:   "disable",
		JWTSecret:   "test-jwt-secret-not-for-prod",
		JWTExpiryHr: 720,
	}
}

// ensureDatabase creates the sales_system_test database if it doesn't already
// exist. Postgres has no CREATE DATABASE IF NOT EXISTS, so check pg_database
// first (and tolerate a benign "already exists" race on top of that).
func ensureDatabase(cfg *config.Config) error {
	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=postgres sslmode=%s",
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBSSLMode)
	sqlDB, err := sql.Open("postgres", dsn)
	if err != nil {
		return fmt.Errorf("open postgres admin conn: %w", err)
	}
	defer sqlDB.Close()

	var exists bool
	if err := sqlDB.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", cfg.DBName).Scan(&exists); err != nil {
		return fmt.Errorf("check pg_database: %w", err)
	}
	if exists {
		return nil
	}
	if _, err := sqlDB.Exec(fmt.Sprintf("CREATE DATABASE %q", cfg.DBName)); err != nil {
		if !strings.Contains(err.Error(), "already exists") {
			return fmt.Errorf("create database: %w", err)
		}
	}
	return nil
}

func setup() {
	testCfg = buildConfig()

	if err := ensureDatabase(testCfg); err != nil {
		panic(fmt.Sprintf("testutil: ensure test database: %v", err))
	}

	db, err := database.Connect(testCfg)
	if err != nil {
		panic(fmt.Sprintf("testutil: connect to test database: %v", err))
	}
	if err := database.AutoMigrate(db); err != nil {
		panic(fmt.Sprintf("testutil: automigrate: %v", err))
	}
	testDB = db
}

// TruncateAll wipes every table between tests, restarting identity sequences
// so IDs are predictable and cascading so FKs never block the truncate.
func TruncateAll(db *gorm.DB) error {
	stmt := fmt.Sprintf("TRUNCATE TABLE %s RESTART IDENTITY CASCADE", strings.Join(tables, ", "))
	return db.Exec(stmt).Error
}

// App returns a fresh Fiber app wired via routes.Setup against the shared
// test DB connection, with all tables truncated first so each test starts
// from a clean slate. Safe to call once per test (or subtest) — DB access
// is sequential given `go test -p 1`, matching the package-level requirement
// that these tests not run concurrently against the shared connection.
func App(t *testing.T) (*fiber.App, *gorm.DB) {
	t.Helper()
	once.Do(setup)
	require.NoError(t, TruncateAll(testDB), "truncate tables before test")

	app := fiber.New()
	routes.Setup(app, testDB, testCfg)
	return app, testDB
}

// Config exposes the test config (in particular JWTSecret) to tests that need
// to mint tokens directly, e.g. to forge an alg-confusion token.
func Config() *config.Config {
	once.Do(setup)
	return testCfg
}

var userSeq int64

var (
	testPasswordHashOnce sync.Once
	testPasswordHash     string
)

// testPasswordHash computes the bcrypt hash for TestPassword exactly once per
// test binary run — CreateUser is called dozens of times across the suite,
// and bcrypt is deliberately slow, so hashing the same constant repeatedly
// would add unnecessary wall-clock time for no behavioral benefit.
func hashedTestPassword(t *testing.T) string {
	t.Helper()
	testPasswordHashOnce.Do(func() {
		hash, err := utils.HashPassword(TestPassword)
		require.NoError(t, err)
		testPasswordHash = hash
	})
	return testPasswordHash
}

// CreateUser inserts a user with role `role` and password TestPassword,
// returning the persisted row. Username/email are unique per call so tests
// can run repeatedly against the truncated-between-tests shared DB without
// unique-constraint collisions.
func CreateUser(t *testing.T, db *gorm.DB, role models.Role) *models.User {
	t.Helper()
	n := atomic.AddInt64(&userSeq, 1)
	tag := fmt.Sprintf("%d_%d", time.Now().UnixNano(), n)

	hash := hashedTestPassword(t)

	u := &models.User{
		FirstName:    "Test",
		LastName:     "User" + tag,
		Username:     "user_" + tag,
		Email:        "user_" + tag + "@example.com",
		PasswordHash: hash,
		Role:         role,
		IsActive:     true,
	}
	require.NoError(t, db.Create(u).Error)
	return u
}

// Token mints a Bearer JWT for the given user id/role using the test config's
// secret, via the real utils.GenerateToken (same code path production uses).
func Token(t *testing.T, userID uint, role models.Role) string {
	t.Helper()
	cfg := Config()
	tok, err := utils.GenerateToken(cfg.JWTSecret, cfg.JWTExpiryHr, userID, role)
	require.NoError(t, err)
	return tok
}

// NewRequest builds an *http.Request suitable for app.Test(req), JSON-encoding
// body (if non-nil) and attaching a Bearer token (if non-empty).
func NewRequest(t *testing.T, method, target string, body interface{}, token string) *http.Request {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, target, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

// AuthRequest is NewRequest plus minting a token for userID/role in one call.
func AuthRequest(t *testing.T, method, target string, body interface{}, userID uint, role models.Role) *http.Request {
	t.Helper()
	return NewRequest(t, method, target, body, Token(t, userID, role))
}

// DecodeJSON decodes a Fiber test response body into v and closes it.
func DecodeJSON(t *testing.T, resp *http.Response, v interface{}) {
	t.Helper()
	defer resp.Body.Close()
	require.NoError(t, json.NewDecoder(resp.Body).Decode(v))
}
