package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// sensitiveSettingKey mirrors the panel API's filter (router.isSensitiveSettingKey):
// keys that may carry credentials are never shown, whichever path filled the
// map. Keeping a copy here avoids importing the router package for one switch.
func sensitiveSettingKey(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	if k == "" {
		return true
	}
	switch k {
	case "telegram", "panel_ssl":
		return true
	}
	for _, s := range []string{"token", "secret", "credential", "password", "private", "bot", "key", "auth"} {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// openPanelDB opens the panel's own SQLite file.
//
// The console runs as a separate process next to the panel service, which
// already holds the same database, so concurrent access is expected rather
// than exceptional:
//   - _busy_timeout=8000 makes both sides wait for a short write lock instead
//     of failing outright;
//   - a single connection keeps one process from deadlocking against itself;
//   - the read-only mode used for browsing cannot take a write lock at all,
//     and it also guarantees a slip in this package can never corrupt the
//     panel's data while someone is just looking at it.
//
// It deliberately does NOT call database.InitDB: that runs migration and
// de-duplication passes (and can rename listeners or rotate subscription
// tokens), which is the panel's job, not a viewer's.
func openPanelDB(path string, writable bool) (*gorm.DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("no database path in config")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	q := "_busy_timeout=8000"
	if !writable {
		q = "mode=ro&_busy_timeout=8000"
	}
	db, err := gorm.Open(sqlite.New(sqlite.Config{
		DriverName: sqliteDriverName,
		DSN:        "file:" + abs + "?" + q,
	}), &gorm.Config{
		// The console draws its own screen; SQL logs would smear over it.
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, err
	}
	if sqlDB, sqlErr := db.DB(); sqlErr == nil {
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
	}
	return db, nil
}

// withWritableDB runs fn against a short-lived writable connection. Actions
// (enable a user, toggle a listener) are the only reason the console ever
// opens the database for writing, and the handle is closed right after so the
// panel is never blocked longer than the action itself.
func withWritableDB(path string, fn func(*gorm.DB) error) error {
	db, err := openPanelDB(path, true)
	if err != nil {
		return err
	}
	defer func() {
		if sqlDB, e := db.DB(); e == nil {
			_ = sqlDB.Close()
		}
	}()
	return fn(db)
}
