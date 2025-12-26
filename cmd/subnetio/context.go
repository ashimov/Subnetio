package main

import (
	"database/sql"
	"strconv"

	"github.com/gin-gonic/gin"
)

func baseData(c *gin.Context, db *sql.DB, defaultProjectID int64) (gin.H, int64) {
	activeProjectID := resolveActiveProjectID(c, db, defaultProjectID)
	projects, _ := listProjects(db)
	activeName := "Default"
	for _, p := range projects {
		if p.ID == activeProjectID {
			activeName = p.Name
			break
		}
	}
	data := gin.H{
		"Projects":          projects,
		"ActiveProjectID":   activeProjectID,
		"ActiveProjectName": activeName,
		"CurrentPath":       c.Request.URL.Path,
	}
	return data, activeProjectID
}

func resolveActiveProjectID(c *gin.Context, db *sql.DB, defaultProjectID int64) int64 {
	if id := parseProjectID(c.Query("project_id")); id > 0 {
		if projectExists(db, id) {
			c.SetCookie("active_project_id", itoa64(id), 3600*24*365, "/", "", false, true)
			return id
		}
	}
	if raw, err := c.Cookie("active_project_id"); err == nil {
		if id := parseProjectID(raw); id > 0 {
			if projectExists(db, id) {
				return id
			}
		}
	}
	if projectExists(db, defaultProjectID) {
		return defaultProjectID
	}
	return 0
}

func parseProjectID(raw string) int64 {
	if raw == "" {
		return 0
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}
	return id
}

func projectExists(db *sql.DB, id int64) bool {
	if id <= 0 {
		return false
	}
	var out int64
	if err := db.QueryRow(`SELECT id FROM projects WHERE id=?`, id).Scan(&out); err != nil {
		return false
	}
	return true
}
