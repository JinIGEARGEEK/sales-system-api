package config

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	AppEnv      string
	Port        string
	DatabaseURL string // set → used as-is (Railway/Heroku/Render-style single connection string)
	DBHost      string
	DBPort      string
	DBUser      string
	DBPassword  string
	DBName      string
	DBSSLMode   string
	JWTSecret   string
	JWTExpiryHr int
	CORSOrigins string // comma-separated allow-list; "*" (default) allows any origin

	// SMTP settings for the Task due-date email reminder feature. SMTPHost empty
	// means email is not configured — utils.SendMail silently no-ops rather
	// than erroring (utils.LogMailStatus logs that once at startup), so the
	// app runs fine without these set; alerts still reach people in-app.
	SMTPHost     string
	SMTPPort     string
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string

	// AppURL is the frontend's public base URL (e.g. https://crm.example.com),
	// used to link from emails back into the app. Optional: emails just omit
	// the link when it's unset.
	AppURL string

	// Object storage backend for Quote/Contract/Attachment uploads — see
	// biz_spec/s3-migration-plan.md. "local" (default) writes to disk, fine
	// for dev/docker-compose but not durable on a stateless deploy platform;
	// "s3" requires the S3_* fields below.
	StorageBackend    string
	S3Bucket          string
	S3Region          string
	S3Endpoint        string
	S3AccessKeyID     string
	S3SecretAccessKey string
	S3ForcePathStyle  bool

	// Connection-pool limits for database.Connect. database/sql's own
	// defaults are unlimited open connections that live forever — a burst
	// can exhaust Postgres' max_connections (Railway's managed Postgres is
	// small), and a connection dropped server-side (failover, idle reaping)
	// isn't noticed until a request trips over it. Env-tunable since the
	// right ceiling depends on the plan and on how many replicas share it
	// (replicas × DB_MAX_OPEN_CONNS must stay under the server's limit).
	DBMaxOpenConns    int
	DBMaxIdleConns    int
	DBConnMaxLifetime time.Duration
	DBConnMaxIdleTime time.Duration

	// TrustedProxies are the peers (IPs or CIDRs) whose X-Forwarded-For the
	// login rate limiter believes — see internal/clientip. Empty means trust
	// nobody: every caller is keyed by its socket address, which is right
	// for a direct connection but, behind a proxy, puts everyone into the
	// proxy's single bucket. On Railway it defaults to the private ranges
	// instead — see defaultTrustedProxies.
	TrustedProxies []string

	// AdminInitialPassword, when set, is the password seedAdmin gives the
	// first Admin (cmd/api/main.go) instead of a generated one, so a real
	// deployment never has to print a credential into its logs. Only read
	// while the users table is empty; the Admin still has to change it at
	// first login.
	AdminInitialPassword string
}

// Connection-pool defaults, used when the DB_* pool vars are unset.
const (
	DefaultDBMaxOpenConns    = 25
	DefaultDBMaxIdleConns    = 10
	DefaultDBConnMaxLifetime = 30 * time.Minute
	DefaultDBConnMaxIdleTime = 5 * time.Minute
)

func Load() *Config {
	_ = godotenv.Load()

	return &Config{
		AppEnv:      getEnv("APP_ENV", "development"),
		Port:        getEnv("PORT", "8080"),
		DatabaseURL: getEnv("DATABASE_URL", ""),
		DBHost:      getEnv("DB_HOST", "localhost"),
		DBPort:      getEnv("DB_PORT", "5432"),
		DBUser:      getEnv("DB_USER", "postgres"),
		DBPassword:  getEnv("DB_PASSWORD", "postgres"),
		DBName:      getEnv("DB_NAME", "sales_system"),
		DBSSLMode:   getEnv("DB_SSLMODE", "disable"),
		JWTSecret:   getEnv("JWT_SECRET", "change-me-in-production"),
		JWTExpiryHr: getEnvInt("JWT_EXPIRY_HOURS", 720),
		CORSOrigins: getEnv("CORS_ORIGINS", "*"),

		SMTPHost:     getEnv("SMTP_HOST", ""),
		SMTPPort:     getEnv("SMTP_PORT", "587"),
		SMTPUsername: getEnv("SMTP_USERNAME", ""),
		SMTPPassword: getEnv("SMTP_PASSWORD", ""),
		SMTPFrom:     getEnv("SMTP_FROM", ""),
		AppURL:       getEnv("APP_URL", ""),

		StorageBackend:    getEnv("STORAGE_BACKEND", "local"),
		S3Bucket:          getEnv("S3_BUCKET", ""),
		S3Region:          getEnv("S3_REGION", ""),
		S3Endpoint:        getEnv("S3_ENDPOINT", ""),
		S3AccessKeyID:     getEnv("S3_ACCESS_KEY_ID", ""),
		S3SecretAccessKey: getEnv("S3_SECRET_ACCESS_KEY", ""),
		S3ForcePathStyle:  getEnv("S3_FORCE_PATH_STYLE", "false") == "true",

		DBMaxOpenConns:    getEnvInt("DB_MAX_OPEN_CONNS", DefaultDBMaxOpenConns),
		DBMaxIdleConns:    getEnvInt("DB_MAX_IDLE_CONNS", DefaultDBMaxIdleConns),
		DBConnMaxLifetime: getEnvDuration("DB_CONN_MAX_LIFETIME", DefaultDBConnMaxLifetime),
		DBConnMaxIdleTime: getEnvDuration("DB_CONN_MAX_IDLE_TIME", DefaultDBConnMaxIdleTime),

		TrustedProxies: loadTrustedProxies(),

		AdminInitialPassword: getEnv("ADMIN_INITIAL_PASSWORD", ""),
	}
}

// railwayPrivateRanges is the TRUSTED_PROXIES default on Railway: the
// RFC 1918, CGNAT and IPv6 ULA ranges. Only Railway's edge proxy (or the
// project's own private network) can reach the container from these, and
// Railway doesn't document which range the edge uses, hence all of them.
// If it's none, clientip logs the untrusted peer to put in TRUSTED_PROXIES.
var railwayPrivateRanges = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "fc00::/7"}

// loadTrustedProxies parses TRUSTED_PROXIES (comma-separated IPs/CIDRs, or
// "none" to explicitly trust nobody), falling back to defaultTrustedProxies
// when it's unset.
func loadTrustedProxies() []string {
	raw := strings.TrimSpace(getEnv("TRUSTED_PROXIES", ""))
	if raw == "" {
		return defaultTrustedProxies()
	}
	if strings.EqualFold(raw, "none") {
		return nil
	}
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// defaultTrustedProxies trusts railwayPrivateRanges when running on Railway
// (which injects RAILWAY_ENVIRONMENT/RAILWAY_ENVIRONMENT_NAME into every
// service), and nobody anywhere else — local dev and docker-compose connect
// directly, where honoring X-Forwarded-For would let any caller pick their
// own rate-limit key.
func defaultTrustedProxies() []string {
	for _, k := range []string{"RAILWAY_ENVIRONMENT", "RAILWAY_ENVIRONMENT_NAME"} {
		if _, ok := os.LookupEnv(k); ok {
			return append([]string(nil), railwayPrivateRanges...)
		}
	}
	return nil
}

// getEnvInt reads a non-negative integer env var, falling back (with a log
// line, so a typo isn't silently ignored) when it's unset or malformed.
func getEnvInt(key string, fallback int) int {
	v := getEnv(key, "")
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		log.Printf("config: invalid %s=%q, using default %d", key, v, fallback)
		return fallback
	}
	return n
}

// getEnvDuration reads a Go duration env var (e.g. "30m"), with the same
// fallback behavior as getEnvInt.
func getEnvDuration(key string, fallback time.Duration) time.Duration {
	v := getEnv(key, "")
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		log.Printf("config: invalid %s=%q, using default %s", key, v, fallback)
		return fallback
	}
	return d
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
