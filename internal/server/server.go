// Package server builds the Fiber app: its config (error handler, body
// limit, timeouts, trusted proxies), the global middleware stack, /health,
// and every route. Shared by cmd/api/main.go and testutil.App so the test
// suite exercises the exact app production runs — the same error envelope,
// panic recovery and body limit.
package server

import (
	"context"
	"log"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/requestid"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/clientip"
	"github.com/igeargeek/sales-system-api/internal/config"
	"github.com/igeargeek/sales-system-api/internal/handlers"
	"github.com/igeargeek/sales-system-api/internal/routes"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// BodyLimit is the largest request body accepted. Fiber's default is 4MB,
// which made utils.MaxUploadSize (10MB) unreachable: a 5MB PDF was cut off
// with a bare 413 before the handler's own size check could run. Sized at
// MaxUploadSize plus 1MB for the multipart envelope (boundaries, part
// headers, the other form fields sent alongside the file), so the handler
// is what rejects an oversized file, with its own error message.
const BodyLimit = utils.MaxUploadSize + 1<<20

// Server timeouts. fasthttp's are unlimited by default, so a slow or stalled
// client could hold a connection (and its goroutine/buffer) forever.
//   - ReadTimeout bounds reading the whole request, body included — 2
//     minutes is a 10MB upload at under 1 Mbit/s, generous for a phone on a
//     poor connection.
//   - WriteTimeout bounds writing the response — the PDF/CSV exports are
//     rendered in the handler before the write starts, so this only has to
//     cover shipping the bytes to a slow client.
//   - IdleTimeout bounds a keep-alive connection between requests; kept
//     above typical proxy idle timeouts so the proxy, not us, closes an idle
//     upstream connection (closing it first races the proxy's reuse of it).
const (
	ReadTimeout  = 2 * time.Minute
	WriteTimeout = 2 * time.Minute
	IdleTimeout  = 2 * time.Minute
)

// healthDBTimeout bounds /health's DB ping — well under railway.toml's
// healthcheckTimeout, so a hung database reads as "unhealthy", not as a
// health check that never answers.
const healthDBTimeout = 2 * time.Second

// New builds the app for cfg/db/storage with every route registered.
// proxies is cfg.TrustedProxies parsed by clientip.New, which keys the login
// limiter. accessLog false drops the per-request access log (the test suite
// sets it; error logging stays on). Listening and shutdown are the caller's.
func New(cfg *config.Config, db *gorm.DB, storage utils.Storage, proxies *clientip.Resolver, accessLog bool) *fiber.App {
	app := fiber.New(fiber.Config{
		ErrorHandler: apiErrorHandler,
		BodyLimit:    BodyLimit,
		ReadTimeout:  ReadTimeout,
		WriteTimeout: WriteTimeout,
		IdleTimeout:  IdleTimeout,
		// Without EnableTrustedProxyCheck Fiber treats every peer as a
		// trusted proxy, honoring anyone's X-Forwarded-Proto/-Host in
		// c.Protocol()/c.Hostname(). Same list the login limiter uses
		// (internal/clientip). ProxyHeader is deliberately left unset, so
		// c.IP() stays the socket peer: Fiber would read X-Forwarded-For's
		// leftmost entry, which is client-writable — use clientip for a
		// real client address.
		EnableTrustedProxyCheck: true,
		TrustedProxies:          cfg.TrustedProxies,
	})
	// Stack traces on: a panic is by definition a bug, and the 500 the
	// client gets (apiErrorHandler) says nothing useful on its own.
	app.Use(recover.New(recover.Config{EnableStackTrace: true}))
	// requestid before logger, so an access-log line and apiErrorHandler's
	// error line for the same request share an ID. Echoes/generates
	// X-Request-ID, which a client can report back for support.
	app.Use(requestid.New())
	if accessLog {
		app.Use(logger.New(logger.Config{
			Format: "${time} ${status} - ${latency} ${method} ${path} reqid=${locals:requestid}\n",
		}))
	}
	app.Use(cors.New(cors.Config{
		AllowOrigins: cfg.CORSOrigins,
		// A cross-origin frontend can only read non-standard response
		// headers listed here.
		ExposeHeaders: handlers.LaneRebalancedHeader,
	}))

	// Unauthenticated — used by the hosting platform's health check (e.g.
	// Railway) to decide whether a deploy is ready to receive traffic. Pings
	// the database: a process that's up but can't reach Postgres would
	// 500 every real request, so it shouldn't be reported ready.
	app.Get("/health", func(c *fiber.Ctx) error {
		ctx, cancel := context.WithTimeout(c.UserContext(), healthDBTimeout)
		defer cancel()
		sqlDB, err := db.DB()
		if err == nil {
			err = sqlDB.PingContext(ctx)
		}
		if err != nil {
			log.Printf("health: database ping failed: %v", err)
			return utils.ErrorResponse(c, fiber.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "Database unreachable")
		}
		return c.SendStatus(fiber.StatusOK)
	})

	routes.Setup(app, db, cfg, storage, proxies)
	return app
}

// apiErrorHandler guarantees every error — including a panic caught by
// recover.New() or a handler that returns a bare error instead of routing
// through utils.* — still gets the §1.5 JSON envelope instead of Fiber's
// default plain-text response.
func apiErrorHandler(c *fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	message := err.Error()
	if fe, ok := err.(*fiber.Error); ok {
		code = fe.Code
		message = fe.Message
	}
	// 5xx messages can carry raw Go/GORM/driver error text (internal details
	// that shouldn't reach a client) — log the real error server-side and
	// return a generic message instead. 4xx messages (e.g. Fiber's own
	// "Cannot GET /x") are safe to pass through as-is.
	if code >= fiber.StatusInternalServerError {
		reqID, _ := c.Locals("requestid").(string)
		log.Printf("unhandled error on %s %s (reqid=%s): %v", c.Method(), c.Path(), reqID, err)
		message = "Internal server error"
	}
	return utils.ErrorResponse(c, code, "INTERNAL_ERROR", message)
}
