package gormlog

import (
	"context"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm/logger"
)

// New returns a GORM logger that only emits slow queries (Warn) and compact errors.
// Set LLMD_GORM_SLOW_MS to override the slow threshold (default 200ms).
// Set LLMD_GORM_SQL=1 to restore verbose SQL logging (Info level).
func New() logger.Interface {
	slowMS := 200
	if v := strings.TrimSpace(os.Getenv("LLMD_GORM_SLOW_MS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			slowMS = n
		}
	}
	level := logger.Warn
	if os.Getenv("LLMD_GORM_SQL") == "1" {
		level = logger.Info
	}
	base := logger.New(
		log.New(os.Stdout, "", log.LstdFlags),
		logger.Config{
			SlowThreshold:             time.Duration(slowMS) * time.Millisecond,
			LogLevel:                  level,
			IgnoreRecordNotFoundError: true,
			Colorful:                  false,
		},
	)
	return &compactErrors{Interface: base}
}

type compactErrors struct {
	logger.Interface
}

func (l *compactErrors) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	if err != nil && isNoisyConstraintErr(err.Error()) {
		// Idempotent re-index races are handled via delete-before-insert; skip log spam.
		return
	}
	l.Interface.Trace(ctx, begin, fc, err)
}

func isNoisyConstraintErr(msg string) bool {
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "constraint failed")
}

func summarizeSQL(sql string) string {
	sql = strings.Join(strings.Fields(sql), " ")
	const max = 120
	if len(sql) <= max {
		return sql
	}
	return sql[:max] + "…"
}
