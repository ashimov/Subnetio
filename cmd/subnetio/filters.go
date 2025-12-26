package main

import (
	"database/sql"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type FilterPreset struct {
	ID        int64
	ProjectID int64
	Page      string
	Name      string
	Query     string
	CreatedAt string
}

type SegmentFilters struct {
	SiteID int64
	VRF    string
	VLAN   int
	Tag    string
	Name   string
}

func listFilterPresets(db *sql.DB, projectID int64, page string) ([]FilterPreset, error) {
	if projectID <= 0 || strings.TrimSpace(page) == "" {
		return nil, nil
	}
	rows, err := db.Query(`
		SELECT id, project_id, page, name, query, created_at
		FROM filter_presets
		WHERE project_id=? AND page=?
		ORDER BY created_at DESC, id DESC
	`, projectID, page)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []FilterPreset
	for rows.Next() {
		var preset FilterPreset
		if err := rows.Scan(&preset.ID, &preset.ProjectID, &preset.Page, &preset.Name, &preset.Query, &preset.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, preset)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func saveFilterPreset(db *sql.DB, projectID int64, page, name, query string) error {
	if projectID <= 0 {
		return nil
	}
	_, err := db.Exec(`
		INSERT INTO filter_presets(project_id, page, name, query, created_at)
		VALUES(?, ?, ?, ?, ?)
	`, projectID, page, name, query, time.Now().UTC().Format(time.RFC3339))
	return err
}

func deleteFilterPreset(db *sql.DB, projectID int64, presetID int64, page string) error {
	if projectID <= 0 || presetID <= 0 || strings.TrimSpace(page) == "" {
		return nil
	}
	_, err := db.Exec(`DELETE FROM filter_presets WHERE id=? AND project_id=? AND page=?`, presetID, projectID, page)
	return err
}

func parseSegmentFilters(c *gin.Context) SegmentFilters {
	return segmentFiltersFromValues(c.Request.URL.Query())
}

func normalizeSegmentFilterQuery(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	raw = strings.TrimPrefix(raw, "?")
	values, err := url.ParseQuery(raw)
	if err != nil {
		return ""
	}
	return segmentFiltersQuery(segmentFiltersFromValues(values))
}

func segmentFiltersFromValues(values url.Values) SegmentFilters {
	var out SegmentFilters
	if raw := strings.TrimSpace(values.Get("filter_site")); raw != "" {
		if id, err := strconv.ParseInt(raw, 10, 64); err == nil && id > 0 {
			out.SiteID = id
		}
	}
	if raw := strings.TrimSpace(values.Get("filter_vrf")); raw != "" {
		out.VRF = raw
	}
	if raw := strings.TrimSpace(values.Get("filter_vlan")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			out.VLAN = v
		}
	}
	if raw := strings.TrimSpace(values.Get("filter_tag")); raw != "" {
		out.Tag = raw
	}
	if raw := strings.TrimSpace(values.Get("filter_name")); raw != "" {
		out.Name = raw
	}
	return out
}

func segmentFiltersQuery(filters SegmentFilters) string {
	values := url.Values{}
	if filters.SiteID > 0 {
		values.Set("filter_site", itoa64(filters.SiteID))
	}
	if filters.VRF != "" {
		values.Set("filter_vrf", strings.TrimSpace(filters.VRF))
	}
	if filters.VLAN > 0 {
		values.Set("filter_vlan", itoa(filters.VLAN))
	}
	if filters.Tag != "" {
		values.Set("filter_tag", strings.TrimSpace(filters.Tag))
	}
	if filters.Name != "" {
		values.Set("filter_name", strings.TrimSpace(filters.Name))
	}
	return values.Encode()
}

func filtersActive(filters SegmentFilters) bool {
	return filters.SiteID > 0 || filters.VRF != "" || filters.VLAN > 0 || filters.Tag != "" || filters.Name != ""
}

func applySegmentFilters(views []SegmentView, filters SegmentFilters) []SegmentView {
	if !filtersActive(filters) {
		return views
	}
	out := make([]SegmentView, 0, len(views))
	nameNeedle := strings.ToLower(filters.Name)
	vrfNeedle := strings.ToLower(filters.VRF)
	tagNeedle := strings.ToLower(filters.Tag)
	for _, view := range views {
		if filters.SiteID > 0 && view.SiteID != filters.SiteID {
			continue
		}
		if filters.VLAN > 0 && view.VLAN != filters.VLAN {
			continue
		}
		if vrfNeedle != "" && !strings.Contains(strings.ToLower(view.VRF), vrfNeedle) {
			continue
		}
		if nameNeedle != "" && !strings.Contains(strings.ToLower(view.Name), nameNeedle) {
			continue
		}
		if tagNeedle != "" {
			tags := ""
			if view.Tags.Valid {
				tags = view.Tags.String
			}
			if tags == "" || !strings.Contains(strings.ToLower(tags), tagNeedle) {
				continue
			}
		}
		out = append(out, view)
	}
	return out
}

func segmentsRedirectURL(projectID int64, filterQuery, key, value string) string {
	values := url.Values{}
	if projectID > 0 {
		values.Set("project_id", itoa64(projectID))
	}
	if filterQuery != "" {
		if parsed, err := url.ParseQuery(filterQuery); err == nil {
			for k, vs := range parsed {
				for _, v := range vs {
					values.Add(k, v)
				}
			}
		}
	}
	if key != "" && value != "" {
		values.Set(key, value)
	}
	if enc := values.Encode(); enc != "" {
		return "/segments?" + enc
	}
	return "/segments"
}
