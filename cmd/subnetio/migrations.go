package main

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func migrate(db *sql.DB) error {
	if err := ensureMigrationsTable(db); err != nil {
		return err
	}
	files, err := listMigrationFiles()
	if err != nil {
		return err
	}
	latest, err := latestMigrationVersion(files)
	if err != nil {
		return err
	}
	current, err := currentMigrationVersion(db)
	if err != nil {
		return err
	}
	if current > latest {
		return fmt.Errorf("database schema is newer (%d) than this binary supports (%d)", current, latest)
	}
	for _, file := range files {
		version, err := migrationVersion(file)
		if err != nil {
			return err
		}
		applied, err := migrationApplied(db, version)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		body, err := migFS.ReadFile(file)
		if err != nil {
			return err
		}
		if err := execMigrationSQL(db, string(body)); err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
		if err := markMigration(db, version); err != nil {
			return err
		}
	}
	return nil
}

func latestMigrationVersion(files []string) (int, error) {
	latest := 0
	for _, file := range files {
		version, err := migrationVersion(file)
		if err != nil {
			return 0, err
		}
		if version > latest {
			latest = version
		}
	}
	return latest, nil
}

func currentMigrationVersion(db *sql.DB) (int, error) {
	var value sql.NullInt64
	if err := db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&value); err != nil {
		return 0, err
	}
	if !value.Valid {
		return 0, nil
	}
	return int(value.Int64), nil
}

func ensureMigrationsTable(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		)
	`)
	return err
}

func listMigrationFiles() ([]string, error) {
	entries, err := migFS.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		files = append(files, "migrations/"+entry.Name())
	}
	sort.Strings(files)
	return files, nil
}

func migrationVersion(path string) (int, error) {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	var digits strings.Builder
	for _, r := range base {
		if r < '0' || r > '9' {
			break
		}
		digits.WriteRune(r)
	}
	if digits.Len() == 0 {
		return 0, fmt.Errorf("invalid migration name: %s", path)
	}
	version, err := strconv.Atoi(digits.String())
	if err != nil {
		return 0, fmt.Errorf("invalid migration version: %s", path)
	}
	return version, nil
}

func migrationApplied(db *sql.DB, version int) (bool, error) {
	var out int
	if err := db.QueryRow(`SELECT COUNT(1) FROM schema_migrations WHERE version=?`, version).Scan(&out); err != nil {
		return false, err
	}
	return out > 0, nil
}

func markMigration(db *sql.DB, version int) error {
	_, err := db.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES(?, ?)`, version, time.Now().UTC().Format(time.RFC3339))
	return err
}

func execMigrationSQL(db *sql.DB, body string) error {
	parts := strings.Split(body, ";")
	for _, part := range parts {
		stmt := strings.TrimSpace(part)
		if stmt == "" {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			if isDuplicateColumnError(err) {
				continue
			}
			return err
		}
	}
	return nil
}

func isDuplicateColumnError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate column name")
}
