package main

import "database/sql"

func deleteProject(db *sql.DB, projectID int64, defaultProjectID int64) error {
	if projectID <= 0 || projectID == defaultProjectID {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	siteIDs := []int64{}
	rows, err := tx.Query(`SELECT site_id FROM project_sites WHERE project_id=?`, projectID)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			_ = tx.Rollback()
			return err
		}
		siteIDs = append(siteIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		_ = tx.Rollback()
		return err
	}
	_ = rows.Close()

	for _, siteID := range siteIDs {
		if err := deleteSiteTx(tx, siteID); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM deployed_configs WHERE project_id=?`, projectID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`DELETE FROM project_rules WHERE project_id=?`, projectID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`DELETE FROM project_meta WHERE project_id=?`, projectID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`DELETE FROM projects WHERE id=?`, projectID); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func deleteSite(db *sql.DB, siteID int64) error {
	if siteID <= 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if err := deleteSiteTx(tx, siteID); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func deleteSiteTx(tx *sql.Tx, siteID int64) error {
	if _, err := tx.Exec(`DELETE FROM segment_meta WHERE segment_id IN (SELECT id FROM segments WHERE site_id=?)`, siteID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM segments WHERE site_id=?`, siteID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM pools WHERE site_id=?`, siteID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM site_meta WHERE site_id=?`, siteID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM project_sites WHERE site_id=?`, siteID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM sites WHERE id=?`, siteID); err != nil {
		return err
	}
	return nil
}

func deleteSegment(db *sql.DB, segmentID int64) error {
	if segmentID <= 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM segment_meta WHERE segment_id=?`, segmentID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`DELETE FROM segments WHERE id=?`, segmentID); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func deleteProjectRules(db *sql.DB, projectID int64) error {
	if projectID <= 0 {
		return nil
	}
	_, err := db.Exec(`DELETE FROM project_rules WHERE project_id=?`, projectID)
	return err
}
