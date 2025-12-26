package main

import (
	"database/sql"
	"fmt"
	"net/netip"
	"strings"

	"github.com/gin-gonic/gin"
)

type ImportReport struct {
	ProjectsAdded int
	SitesAdded    int
	PoolsAdded    int
	SegmentsAdded int
	Warnings      []string
	Errors        []string
}

type csvColumns struct {
	Project          int
	Site             int
	VRF              int
	VLAN             int
	Name             int
	Hosts            int
	Prefix           int
	CIDR             int
	Locked           int
	Dhcp             int
	DhcpRange        int
	DhcpReservations int
	Gateway          int
	Tags             int
	Notes            int
	Pool             int
	ReservedRanges   int
	Region           int
	DNS              int
	NTP              int
	GatewayPolicy    int
}

func importCSVPlan(c *gin.Context, db *sql.DB, activeProjectID int64) *ImportReport {
	return importPlanCSV(c, db, activeProjectID)
}

func defaultColumns() csvColumns {
	return csvColumns{
		Project:          -1,
		Site:             0,
		VRF:              1,
		VLAN:             2,
		Name:             3,
		Hosts:            4,
		Prefix:           5,
		CIDR:             6,
		Locked:           7,
		Dhcp:             -1,
		DhcpRange:        -1,
		DhcpReservations: -1,
		Gateway:          -1,
		Tags:             -1,
		Notes:            -1,
		Pool:             -1,
		ReservedRanges:   -1,
		Region:           -1,
		DNS:              -1,
		NTP:              -1,
		GatewayPolicy:    -1,
	}
}

func mapColumns(header []string) csvColumns {
	cols := blankColumns()
	for i, raw := range header {
		name := normalizeHeader(raw)
		switch name {
		case "project", "org", "organization":
			cols.Project = i
		case "site", "sitename", "siteid":
			cols.Site = i
		case "vrf", "vrfname":
			cols.VRF = i
		case "vlan", "vlanid":
			cols.VLAN = i
		case "name", "segment", "segmentname":
			cols.Name = i
		case "hosts", "hostcount", "size":
			cols.Hosts = i
		case "prefix", "mask", "prefixlen":
			cols.Prefix = i
		case "cidr", "subnet", "network":
			cols.CIDR = i
		case "locked", "lock":
			cols.Locked = i
		case "dhcp", "dhcpenabled":
			cols.Dhcp = i
		case "dhcprange", "dhcppool":
			cols.DhcpRange = i
		case "dhcpreservations", "reservations":
			cols.DhcpReservations = i
		case "gateway", "gw":
			cols.Gateway = i
		case "tags", "tag":
			cols.Tags = i
		case "notes", "note":
			cols.Notes = i
		case "pool", "basepool":
			cols.Pool = i
		case "reserved", "reservedranges":
			cols.ReservedRanges = i
		case "region":
			cols.Region = i
		case "dns":
			cols.DNS = i
		case "ntp":
			cols.NTP = i
		case "gatewaypolicy":
			cols.GatewayPolicy = i
		}
	}
	return cols
}

func blankColumns() csvColumns {
	return csvColumns{
		Project:          -1,
		Site:             -1,
		VRF:              -1,
		VLAN:             -1,
		Name:             -1,
		Hosts:            -1,
		Prefix:           -1,
		CIDR:             -1,
		Locked:           -1,
		Dhcp:             -1,
		DhcpRange:        -1,
		DhcpReservations: -1,
		Gateway:          -1,
		Tags:             -1,
		Notes:            -1,
		Pool:             -1,
		ReservedRanges:   -1,
		Region:           -1,
		DNS:              -1,
		NTP:              -1,
		GatewayPolicy:    -1,
	}
}

func looksLikeHeader(row []string) bool {
	for _, cell := range row {
		cell = strings.TrimSpace(cell)
		if cell == "" {
			continue
		}
		for _, r := range cell {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				return true
			}
		}
	}
	return false
}

func normalizeHeader(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "")
	value = strings.ReplaceAll(value, "_", "")
	value = strings.ReplaceAll(value, "-", "")
	return value
}

func processImportRow(db *sql.DB, report *ImportReport, cols csvColumns, row []string, rowIndex int, activeProjectID int64) {
	get := func(idx int) string {
		if idx < 0 || idx >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[idx])
	}

	projectName := get(cols.Project)
	siteName := get(cols.Site)
	vrf := get(cols.VRF)
	vlanStr := get(cols.VLAN)
	segName := get(cols.Name)
	hostsStr := get(cols.Hosts)
	prefixStr := get(cols.Prefix)
	cidrStr := get(cols.CIDR)
	lockedStr := get(cols.Locked)
	dhcpStr := get(cols.Dhcp)
	dhcpRange := get(cols.DhcpRange)
	dhcpReservations := get(cols.DhcpReservations)
	gateway := get(cols.Gateway)
	tags := get(cols.Tags)
	notes := get(cols.Notes)
	poolStr := get(cols.Pool)
	reservedRanges := get(cols.ReservedRanges)
	region := get(cols.Region)
	dns := get(cols.DNS)
	ntp := get(cols.NTP)
	gatewayPolicy := get(cols.GatewayPolicy)

	if siteName == "" {
		report.Errors = append(report.Errors, fmt.Sprintf("row %d: site is required", rowIndex))
		return
	}
	if vrf == "" {
		vrf = "DEFAULT"
		report.Warnings = append(report.Warnings, fmt.Sprintf("row %d: VRF missing, defaulted to DEFAULT", rowIndex))
	}
	vlan := parseInt(vlanStr)
	if vlan <= 0 {
		report.Errors = append(report.Errors, fmt.Sprintf("row %d: invalid VLAN", rowIndex))
		return
	}
	if segName == "" {
		report.Errors = append(report.Errors, fmt.Sprintf("row %d: segment name required", rowIndex))
		return
	}

	projectID := activeProjectID
	if projectName != "" {
		id, created, err := getOrCreateProjectID(db, projectName)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("row %d: project error: %v", rowIndex, err))
			return
		}
		projectID = id
		if created {
			report.ProjectsAdded++
		}
	}
	if projectID == 0 {
		projectID = activeProjectID
	}

	siteID, created, err := getOrCreateSiteID(db, siteName)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("row %d: site error: %v", rowIndex, err))
		return
	}
	if created {
		report.SitesAdded++
	}
	if existingProjectID := projectIDBySite(db, siteID); existingProjectID > 0 && existingProjectID != projectID {
		existingLabel := itoa64(existingProjectID)
		if project, ok := projectByID(db, existingProjectID); ok {
			existingLabel = project.Name
		}
		report.Errors = append(report.Errors, fmt.Sprintf("row %d: site already belongs to project %s", rowIndex, existingLabel))
		return
	}
	_, _ = db.Exec(`INSERT INTO project_sites(project_id, site_id) VALUES(?, ?) ON CONFLICT(site_id) DO UPDATE SET project_id=excluded.project_id`, projectID, siteID)

	if region != "" || dns != "" || ntp != "" || gatewayPolicy != "" || reservedRanges != "" {
		_, _ = db.Exec(`
			INSERT INTO site_meta(site_id, region, dns, ntp, gateway_policy, reserved_ranges)
			VALUES(?, ?, ?, ?, ?, ?)
			ON CONFLICT(site_id) DO UPDATE SET
				region=COALESCE(excluded.region, site_meta.region),
				dns=COALESCE(excluded.dns, site_meta.dns),
				ntp=COALESCE(excluded.ntp, site_meta.ntp),
				gateway_policy=COALESCE(excluded.gateway_policy, site_meta.gateway_policy),
				reserved_ranges=COALESCE(excluded.reserved_ranges, site_meta.reserved_ranges)`,
			siteID,
			nullStringToAny(region),
			nullStringToAny(dns),
			nullStringToAny(ntp),
			nullStringToAny(gatewayPolicy),
			nullStringToAny(reservedRanges),
		)
	}

	if poolStr != "" {
		for _, part := range strings.Split(poolStr, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			prefix, err := netip.ParsePrefix(part)
			if err != nil {
				report.Warnings = append(report.Warnings, fmt.Sprintf("row %d: invalid pool %s", rowIndex, part))
				continue
			}
			family := "ipv4"
			if prefix.Addr().Is6() {
				family = "ipv6"
			}
			cidr := prefix.String()
			if !poolExists(db, siteID, cidr) {
				_, _ = db.Exec(`INSERT INTO pools(site_id, cidr, family) VALUES(?, ?, ?)`, siteID, cidr, family)
				report.PoolsAdded++
			}
		}
	}

	var hosts sql.NullInt64
	if hostsStr != "" {
		if v := parseInt(hostsStr); v > 0 {
			hosts = sql.NullInt64{Int64: int64(v), Valid: true}
		}
	}
	var prefix sql.NullInt64
	if prefixStr != "" {
		if v := parseInt(prefixStr); v > 0 && v <= 32 {
			prefix = sql.NullInt64{Int64: int64(v), Valid: true}
		}
	}

	cidr := strings.TrimSpace(cidrStr)
	if cidr != "" {
		p, err := netip.ParsePrefix(cidr)
		if err != nil {
			report.Warnings = append(report.Warnings, fmt.Sprintf("row %d: invalid CIDR %s", rowIndex, cidr))
			cidr = ""
		} else if !prefix.Valid {
			prefix = sql.NullInt64{Int64: int64(p.Bits()), Valid: true}
		}
	}

	lockedProvided := lockedStr != ""
	locked := parseBool(lockedStr)
	if cidr != "" && !lockedProvided {
		locked = true
		lockedProvided = true
	}

	segID, exists, err := findSegmentID(db, siteID, vrf, vlan, segName)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("row %d: segment lookup error: %v", rowIndex, err))
		return
	}
	if !exists {
		res, err := db.Exec(`
			INSERT INTO segments(site_id, vrf, vlan, name, hosts, prefix, locked, cidr)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
			siteID, vrf, vlan, segName,
			nullIntToAny(hosts), nullIntToAny(prefix),
			boolToInt(locked), nullStringToAny(cidr),
		)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("row %d: insert segment failed: %v", rowIndex, err))
			return
		}
		segID, _ = res.LastInsertId()
		report.SegmentsAdded++
	} else {
		_, _ = db.Exec(`
			UPDATE segments SET
				hosts=COALESCE(?, hosts),
				prefix=COALESCE(?, prefix),
				cidr=COALESCE(?, cidr),
				locked=COALESCE(?, locked)
			WHERE id=?`,
			nullIntToAny(hosts),
			nullIntToAny(prefix),
			nullStringToAny(cidr),
			lockedAny(lockedProvided, locked),
			segID,
		)
	}

	dhcpProvided := dhcpStr != ""
	dhcpEnabled := parseBool(dhcpStr)
	if !dhcpProvided && (dhcpRange != "" || dhcpReservations != "") {
		dhcpProvided = true
		dhcpEnabled = true
		report.Warnings = append(report.Warnings, fmt.Sprintf("row %d: DHCP enabled because range/reservations provided", rowIndex))
	}
	if dhcpProvided || dhcpRange != "" || dhcpReservations != "" || gateway != "" || tags != "" || notes != "" {
		_, _ = db.Exec(`
			INSERT INTO segment_meta(segment_id, dhcp_enabled, dhcp_range, dhcp_reservations, gateway, notes, tags)
			VALUES(?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(segment_id) DO UPDATE SET
				dhcp_enabled=COALESCE(excluded.dhcp_enabled, segment_meta.dhcp_enabled),
				dhcp_range=COALESCE(excluded.dhcp_range, segment_meta.dhcp_range),
				dhcp_reservations=COALESCE(excluded.dhcp_reservations, segment_meta.dhcp_reservations),
				gateway=COALESCE(excluded.gateway, segment_meta.gateway),
				notes=COALESCE(excluded.notes, segment_meta.notes),
				tags=COALESCE(excluded.tags, segment_meta.tags)`,
			segID,
			boolAny(dhcpProvided, dhcpEnabled),
			nullStringToAny(dhcpRange),
			nullStringToAny(dhcpReservations),
			nullStringToAny(gateway),
			nullStringToAny(notes),
			nullStringToAny(tags),
		)
	}
}

func parseInt(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if strings.HasPrefix(value, "/") {
		value = strings.TrimPrefix(value, "/")
	}
	var out int
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0
		}
		out = out*10 + int(r-'0')
	}
	return out
}

func parseBool(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return false
	}
}

func lockedAny(provided bool, value bool) any {
	if !provided {
		return nil
	}
	return boolToInt(value)
}

func boolAny(provided bool, value bool) any {
	if !provided {
		return nil
	}
	return boolToInt(value)
}

func getOrCreateProjectID(db *sql.DB, name string) (int64, bool, error) {
	var id int64
	err := db.QueryRow(`SELECT id FROM projects WHERE name=?`, name).Scan(&id)
	if err == nil {
		return id, false, nil
	}
	if err != sql.ErrNoRows {
		return 0, false, err
	}
	res, err := db.Exec(`INSERT INTO projects(name) VALUES(?)`, name)
	if err != nil {
		return 0, false, err
	}
	id, err = res.LastInsertId()
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

func getOrCreateSiteID(db *sql.DB, name string) (int64, bool, error) {
	var id int64
	err := db.QueryRow(`SELECT id FROM sites WHERE name=?`, name).Scan(&id)
	if err == nil {
		return id, false, nil
	}
	if err != sql.ErrNoRows {
		return 0, false, err
	}
	res, err := db.Exec(`INSERT INTO sites(name) VALUES(?)`, name)
	if err != nil {
		return 0, false, err
	}
	id, err = res.LastInsertId()
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

func poolExists(db *sql.DB, siteID int64, cidr string) bool {
	var id int64
	if err := db.QueryRow(`SELECT id FROM pools WHERE site_id=? AND cidr=?`, siteID, cidr).Scan(&id); err != nil {
		return false
	}
	return true
}

func findSegmentID(db *sql.DB, siteID int64, vrf string, vlan int, name string) (int64, bool, error) {
	var id int64
	err := db.QueryRow(`SELECT id FROM segments WHERE site_id=? AND vrf=? AND vlan=? AND name=?`, siteID, vrf, vlan, name).Scan(&id)
	if err == nil {
		return id, true, nil
	}
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	return 0, false, err
}
