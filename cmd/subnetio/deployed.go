// Copyright (c) 2025 Berik Ashimov

package main

import (
	"database/sql"
	"net/url"
	"strings"
	"time"
)

type DeployedConfig struct {
	ProjectID int64
	Template  string
	ScopeKey  string
	Content   string
	UpdatedAt string
}

func buildScopeKey(opts GenerateOptions) string {
	parts := []string{}
	if strings.TrimSpace(opts.SiteFilter) != "" {
		parts = append(parts, "site="+escapeScopeValue(opts.SiteFilter))
	}
	if strings.TrimSpace(opts.VRFFilter) != "" {
		parts = append(parts, "vrf="+escapeScopeValue(opts.VRFFilter))
	}
	if strings.TrimSpace(opts.SegmentFilter) != "" {
		parts = append(parts, "segment="+escapeScopeValue(opts.SegmentFilter))
	}
	if len(parts) == 0 {
		return "project"
	}
	return strings.Join(parts, "|")
}

func buildScopeKeyLegacy(opts GenerateOptions) string {
	parts := []string{}
	if strings.TrimSpace(opts.SiteFilter) != "" {
		parts = append(parts, "site="+strings.TrimSpace(opts.SiteFilter))
	}
	if strings.TrimSpace(opts.VRFFilter) != "" {
		parts = append(parts, "vrf="+strings.TrimSpace(opts.VRFFilter))
	}
	if strings.TrimSpace(opts.SegmentFilter) != "" {
		parts = append(parts, "segment="+strings.TrimSpace(opts.SegmentFilter))
	}
	if len(parts) == 0 {
		return "project"
	}
	return strings.Join(parts, "|")
}

func escapeScopeValue(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	return url.QueryEscape(raw)
}

func getDeployedConfig(db *sql.DB, projectID int64, template, scopeKey string) (DeployedConfig, bool, error) {
	if projectID <= 0 || template == "" || scopeKey == "" {
		return DeployedConfig{}, false, nil
	}
	var cfg DeployedConfig
	cfg.ProjectID = projectID
	cfg.Template = template
	cfg.ScopeKey = scopeKey
	row := db.QueryRow(`
		SELECT content, updated_at
		FROM deployed_configs
		WHERE project_id=? AND template=? AND scope_key=?`, projectID, template, scopeKey)
	if err := row.Scan(&cfg.Content, &cfg.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return DeployedConfig{}, false, nil
		}
		return DeployedConfig{}, false, err
	}
	return cfg, true, nil
}

func saveDeployedConfig(db *sql.DB, projectID int64, template, scopeKey, content string) error {
	if projectID <= 0 || template == "" || scopeKey == "" {
		return nil
	}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	updated := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(`
		INSERT INTO deployed_configs(project_id, template, scope_key, content, updated_at)
		VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(project_id, template, scope_key) DO UPDATE SET
			content=excluded.content,
			updated_at=excluded.updated_at`,
		projectID, template, scopeKey, content, updated)
	return err
}

func deleteDeployedConfig(db *sql.DB, projectID int64, template, scopeKey string) error {
	if projectID <= 0 || template == "" || scopeKey == "" {
		return nil
	}
	_, err := db.Exec(`DELETE FROM deployed_configs WHERE project_id=? AND template=? AND scope_key=?`, projectID, template, scopeKey)
	return err
}
