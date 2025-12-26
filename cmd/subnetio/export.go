package main

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
)

type ExportBundle struct {
	Project   ExportProject    `json:"project" yaml:"project"`
	Sites     []ExportSite     `json:"sites" yaml:"sites"`
	Pools     []ExportPool     `json:"pools" yaml:"pools"`
	Segments  []ExportSegment  `json:"segments" yaml:"segments"`
	DHCP      []ExportDHCP     `json:"dhcp" yaml:"dhcp"`
	Conflicts []ExportConflict `json:"conflicts" yaml:"conflicts"`
}

type ExportProject struct {
	ID   int64  `json:"id" yaml:"id"`
	Name string `json:"name" yaml:"name"`
}

type ExportSite struct {
	Project        string `json:"project" yaml:"project"`
	Name           string `json:"name" yaml:"name"`
	Region         string `json:"region" yaml:"region"`
	DNS            string `json:"dns" yaml:"dns"`
	NTP            string `json:"ntp" yaml:"ntp"`
	GatewayPolicy  string `json:"gateway_policy" yaml:"gateway_policy"`
	ReservedRanges string `json:"reserved_ranges" yaml:"reserved_ranges"`
}

type ExportPool struct {
	Site     string `json:"site" yaml:"site"`
	CIDR     string `json:"cidr" yaml:"cidr"`
	Family   string `json:"family" yaml:"family"`
	Tier     string `json:"tier" yaml:"tier"`
	Priority int    `json:"priority" yaml:"priority"`
}

type ExportSegment struct {
	Site          string `json:"site" yaml:"site"`
	VRF           string `json:"vrf" yaml:"vrf"`
	VLAN          int    `json:"vlan" yaml:"vlan"`
	Name          string `json:"name" yaml:"name"`
	Hosts         string `json:"hosts" yaml:"hosts"`
	Prefix        string `json:"prefix" yaml:"prefix"`
	CIDR          string `json:"cidr" yaml:"cidr"`
	PrefixV6      string `json:"prefix_v6" yaml:"prefix_v6"`
	CIDRV6        string `json:"cidr_v6" yaml:"cidr_v6"`
	Mask          string `json:"mask" yaml:"mask"`
	Network       string `json:"network" yaml:"network"`
	Broadcast     string `json:"broadcast" yaml:"broadcast"`
	Gateway       string `json:"gateway" yaml:"gateway"`
	GatewayV6     string `json:"gateway_v6" yaml:"gateway_v6"`
	DhcpEnabled   bool   `json:"dhcp_enabled" yaml:"dhcp_enabled"`
	DhcpRange     string `json:"dhcp_range" yaml:"dhcp_range"`
	Reservations  string `json:"dhcp_reservations" yaml:"dhcp_reservations"`
	Tags          string `json:"tags" yaml:"tags"`
	PoolTier      string `json:"pool_tier" yaml:"pool_tier"`
	Notes         string `json:"notes" yaml:"notes"`
	Locked        bool   `json:"locked" yaml:"locked"`
	Status        string `json:"status" yaml:"status"`
	StatusDetails string `json:"status_details" yaml:"status_details"`
}

type ExportDHCP struct {
	Site         string `json:"site" yaml:"site"`
	VRF          string `json:"vrf" yaml:"vrf"`
	VLAN         int    `json:"vlan" yaml:"vlan"`
	Name         string `json:"name" yaml:"name"`
	CIDR         string `json:"cidr" yaml:"cidr"`
	Gateway      string `json:"gateway" yaml:"gateway"`
	DhcpRange    string `json:"dhcp_range" yaml:"dhcp_range"`
	Reservations string `json:"dhcp_reservations" yaml:"dhcp_reservations"`
}

type ExportConflict struct {
	Level  string `json:"level" yaml:"level"`
	Kind   string `json:"kind" yaml:"kind"`
	Detail string `json:"detail" yaml:"detail"`
}

func exportCSV(c *gin.Context, db *sql.DB, projectID int64) error {
	return exportPlanCSV(c, db, projectID)
}

func exportXLSX(c *gin.Context, db *sql.DB, projectID int64) error {
	bundle, err := buildExportBundle(db, projectID)
	if err != nil {
		return err
	}
	f := excelize.NewFile()
	siteSheet := "Sites"
	f.SetSheetName("Sheet1", siteSheet)
	writeSheetRows(f, siteSheet, buildSitesSheet(bundle.Sites))

	segmentSheet := "Segments"
	f.NewSheet(segmentSheet)
	writeSheetRows(f, segmentSheet, buildSegmentsSheet(bundle.Segments))

	dhcpSheet := "DHCP"
	f.NewSheet(dhcpSheet)
	writeSheetRows(f, dhcpSheet, buildDhcpSheet(bundle.DHCP))

	conflictSheet := "Conflicts"
	f.NewSheet(conflictSheet)
	writeSheetRows(f, conflictSheet, buildConflictsSheet(bundle.Conflicts))

	buf, err := f.WriteToBuffer()
	if err != nil {
		return err
	}
	c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Header("Content-Disposition", "attachment; filename=subnetio_export.xlsx")
	c.Data(http.StatusOK, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", buf.Bytes())
	return nil
}

func exportYAML(c *gin.Context, db *sql.DB, projectID int64) error {
	return exportPlanYAML(c, db, projectID)
}

func exportJSON(c *gin.Context, db *sql.DB, projectID int64) error {
	return exportPlanJSON(c, db, projectID)
}

func exportAuditCSV(c *gin.Context, db *sql.DB, projectID int64) error {
	rows, err := listAuditEntries(db, projectID)
	if err != nil {
		return err
	}
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=subnetio_audit.csv")
	w := csv.NewWriter(c.Writer)
	if err := w.Write([]string{
		"created_at",
		"actor",
		"action",
		"entity_type",
		"entity_id",
		"entity_label",
		"reason",
		"before_json",
		"after_json",
	}); err != nil {
		return err
	}
	for _, row := range rows {
		entityID := ""
		if row.EntityID.Valid {
			entityID = itoa64(row.EntityID.Int64)
		}
		_ = w.Write([]string{
			row.CreatedAt,
			row.Actor,
			row.Action,
			row.EntityType,
			entityID,
			nullString(row.EntityLabel),
			nullString(row.Reason),
			nullString(row.BeforeJSON),
			nullString(row.AfterJSON),
		})
	}
	w.Flush()
	return w.Error()
}

func exportAuditJSON(c *gin.Context, db *sql.DB, projectID int64) error {
	rows, err := listAuditEntries(db, projectID)
	if err != nil {
		return err
	}
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=subnetio_audit.json")
	out, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return err
	}
	c.String(200, string(out))
	return nil
}

func buildExportBundle(db *sql.DB, projectID int64) (ExportBundle, error) {
	project := ExportProject{ID: projectID, Name: "Default"}
	if p, ok := projectByID(db, projectID); ok {
		project.Name = p.Name
	}
	sites, err := listSites(db, projectID)
	if err != nil {
		return ExportBundle{}, err
	}
	pools, err := listPools(db, projectID)
	if err != nil {
		return ExportBundle{}, err
	}
	segments, err := listSegments(db, projectID)
	if err != nil {
		return ExportBundle{}, err
	}
	rules, _ := getProjectRules(db, projectID)
	statuses, conflicts := analyzeAll(segments, pools, sites, rules)
	views := buildSegmentViews(segments, statuses, pools)

	bundle := ExportBundle{
		Project:   project,
		Sites:     exportSites(sites),
		Pools:     exportPools(pools),
		Segments:  exportSegments(views),
		DHCP:      exportDHCP(views),
		Conflicts: exportConflicts(conflicts),
	}
	return bundle, nil
}

func projectByID(db *sql.DB, id int64) (Project, bool) {
	if id <= 0 {
		return Project{}, false
	}
	var p Project
	if err := db.QueryRow(`SELECT id, name, description FROM projects WHERE id=?`, id).Scan(&p.ID, &p.Name, &p.Description); err != nil {
		return Project{}, false
	}
	return p, true
}

func exportSites(sites []Site) []ExportSite {
	out := make([]ExportSite, 0, len(sites))
	for _, s := range sites {
		out = append(out, ExportSite{
			Project:        nullString(s.Project),
			Name:           s.Name,
			Region:         nullString(s.Region),
			DNS:            nullString(s.DNS),
			NTP:            nullString(s.NTP),
			GatewayPolicy:  nullString(s.GatewayPolicy),
			ReservedRanges: nullString(s.ReservedRanges),
		})
	}
	return out
}

func exportPools(pools []Pool) []ExportPool {
	out := make([]ExportPool, 0, len(pools))
	for _, p := range pools {
		out = append(out, ExportPool{
			Site:     p.Site,
			CIDR:     p.CIDR,
			Family:   normalizePoolFamily(p.Family),
			Tier:     nullString(p.Tier),
			Priority: p.Priority,
		})
	}
	return out
}

func exportSegments(views []SegmentView) []ExportSegment {
	out := make([]ExportSegment, 0, len(views))
	for _, v := range views {
		out = append(out, ExportSegment{
			Site:          v.Site,
			VRF:           v.VRF,
			VLAN:          v.VLAN,
			Name:          v.Name,
			Hosts:         nullIntString(v.Hosts),
			Prefix:        nullIntString(v.Prefix),
			CIDR:          v.CIDR,
			PrefixV6:      nullIntString(v.PrefixV6),
			CIDRV6:        v.CIDRV6,
			Mask:          v.Mask,
			Network:       v.Network,
			Broadcast:     v.Broadcast,
			Gateway:       v.Gateway,
			GatewayV6:     v.GatewayV6,
			DhcpEnabled:   v.DhcpEnabled,
			DhcpRange:     v.DhcpRange,
			Reservations:  v.Reservations,
			Tags:          nullString(v.Tags),
			PoolTier:      nullString(v.PoolTier),
			Notes:         nullString(v.Notes),
			Locked:        v.Locked,
			Status:        v.StatusLabel,
			StatusDetails: v.StatusDetail,
		})
	}
	return out
}

func exportDHCP(views []SegmentView) []ExportDHCP {
	out := make([]ExportDHCP, 0, len(views))
	for _, v := range views {
		if !v.DhcpEnabled {
			continue
		}
		out = append(out, ExportDHCP{
			Site:         v.Site,
			VRF:          v.VRF,
			VLAN:         v.VLAN,
			Name:         v.Name,
			CIDR:         v.CIDR,
			Gateway:      v.Gateway,
			DhcpRange:    v.DhcpRange,
			Reservations: v.Reservations,
		})
	}
	return out
}

func exportConflicts(conflicts []Conflict) []ExportConflict {
	out := make([]ExportConflict, 0, len(conflicts))
	for _, c := range conflicts {
		out = append(out, ExportConflict{Level: c.Level, Kind: c.Kind, Detail: c.Detail})
	}
	return out
}

func buildSitesSheet(rows []ExportSite) [][]interface{} {
	out := [][]interface{}{{"project", "site", "region", "dns", "ntp", "gateway_policy", "reserved_ranges"}}
	for _, r := range rows {
		out = append(out, []interface{}{r.Project, r.Name, r.Region, r.DNS, r.NTP, r.GatewayPolicy, r.ReservedRanges})
	}
	return out
}

func buildSegmentsSheet(rows []ExportSegment) [][]interface{} {
	out := [][]interface{}{{"site", "vrf", "vlan", "name", "hosts", "prefix", "cidr", "prefix_v6", "cidr_v6", "mask", "network", "broadcast", "gateway", "gateway_v6", "dhcp_enabled", "dhcp_range", "reservations", "tags", "pool_tier", "notes", "locked", "status", "status_details"}}
	for _, r := range rows {
		out = append(out, []interface{}{r.Site, r.VRF, r.VLAN, r.Name, r.Hosts, r.Prefix, r.CIDR, r.PrefixV6, r.CIDRV6, r.Mask, r.Network, r.Broadcast, r.Gateway, r.GatewayV6, r.DhcpEnabled, r.DhcpRange, r.Reservations, r.Tags, r.PoolTier, r.Notes, r.Locked, r.Status, r.StatusDetails})
	}
	return out
}

func buildDhcpSheet(rows []ExportDHCP) [][]interface{} {
	out := [][]interface{}{{"site", "vrf", "vlan", "name", "cidr", "gateway", "dhcp_range", "reservations"}}
	for _, r := range rows {
		out = append(out, []interface{}{r.Site, r.VRF, r.VLAN, r.Name, r.CIDR, r.Gateway, r.DhcpRange, r.Reservations})
	}
	return out
}

func buildConflictsSheet(rows []ExportConflict) [][]interface{} {
	out := [][]interface{}{{"severity", "kind", "detail"}}
	for _, r := range rows {
		out = append(out, []interface{}{r.Level, r.Kind, r.Detail})
	}
	return out
}

func writeSheetRows(f *excelize.File, sheet string, rows [][]interface{}) {
	for i, row := range rows {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		_ = f.SetSheetRow(sheet, cell, &row)
	}
}

func nullString(v sql.NullString) string {
	if v.Valid {
		return v.String
	}
	return ""
}

func nullIntString(v sql.NullInt64) string {
	if v.Valid {
		return itoa64(v.Int64)
	}
	return ""
}
