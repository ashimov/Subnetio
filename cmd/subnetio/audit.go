package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type AuditEntry struct {
	ID         int64
	ProjectID  sql.NullInt64
	Actor      string
	Action     string
	EntityType string
	EntityID   sql.NullInt64
	EntityLabel sql.NullString
	Reason     sql.NullString
	BeforeJSON sql.NullString
	AfterJSON  sql.NullString
	CreatedAt  string
}

type auditRecord struct {
	ProjectID  int64
	Actor      string
	Action     string
	EntityType string
	EntityID   sql.NullInt64
	EntityLabel sql.NullString
	Reason     sql.NullString
	Before     any
	After      any
}

type auditProjectSnapshot struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type auditProjectMetaSnapshot struct {
	DomainName     string   `json:"domain_name,omitempty"`
	DNS            string   `json:"dns,omitempty"`
	NTP            string   `json:"ntp,omitempty"`
	GatewayPolicy  string   `json:"gateway_policy,omitempty"`
	DhcpSearch     string   `json:"dhcp_search,omitempty"`
	DhcpLeaseTime  *int     `json:"dhcp_lease_time,omitempty"`
	DhcpRenewTime  *int     `json:"dhcp_renew_time,omitempty"`
	DhcpRebindTime *int     `json:"dhcp_rebind_time,omitempty"`
	DhcpBootFile   string   `json:"dhcp_boot_file,omitempty"`
	DhcpNextServer string   `json:"dhcp_next_server,omitempty"`
	DhcpVendorOpts []string `json:"dhcp_vendor_options,omitempty"`
	GrowthRate     *float64 `json:"growth_rate,omitempty"`
	GrowthMonths   *int     `json:"growth_months,omitempty"`
}

type auditRulesSnapshot struct {
	VLANScope            string `json:"vlan_scope"`
	RequireInPool        bool   `json:"require_in_pool"`
	AllowReservedOverlap bool   `json:"allow_reserved_overlap"`
	OversizeThreshold    int    `json:"oversize_threshold"`
	PoolStrategy         string `json:"pool_strategy"`
	PoolTierFallback     bool   `json:"pool_tier_fallback"`
}

type auditSiteSnapshot struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	Project        string `json:"project,omitempty"`
	Region         string `json:"region,omitempty"`
	DNS            string `json:"dns,omitempty"`
	NTP            string `json:"ntp,omitempty"`
	GatewayPolicy  string `json:"gateway_policy,omitempty"`
	ReservedRanges string `json:"reserved_ranges,omitempty"`
	DhcpSearch     string `json:"dhcp_search,omitempty"`
	DhcpLeaseTime  *int   `json:"dhcp_lease_time,omitempty"`
	DhcpRenewTime  *int   `json:"dhcp_renew_time,omitempty"`
	DhcpRebindTime *int   `json:"dhcp_rebind_time,omitempty"`
	DhcpBootFile   string `json:"dhcp_boot_file,omitempty"`
	DhcpNextServer string `json:"dhcp_next_server,omitempty"`
	DhcpVendorOpts []string `json:"dhcp_vendor_options,omitempty"`
}

type auditPoolSnapshot struct {
	ID       int64  `json:"id"`
	Site     string `json:"site"`
	CIDR     string `json:"cidr"`
	Family   string `json:"family"`
	Tier     string `json:"tier,omitempty"`
	Priority int    `json:"priority,omitempty"`
}

type auditSegmentSnapshot struct {
	ID               int64  `json:"id"`
	Site             string `json:"site"`
	VRF              string `json:"vrf"`
	VLAN             int    `json:"vlan"`
	Name             string `json:"name"`
	Hosts            *int   `json:"hosts,omitempty"`
	Prefix           *int   `json:"prefix,omitempty"`
	PrefixV6         *int   `json:"prefix_v6,omitempty"`
	CIDR             string `json:"cidr,omitempty"`
	CIDRV6           string `json:"cidr_v6,omitempty"`
	Locked           bool   `json:"locked"`
	DhcpEnabled      bool   `json:"dhcp_enabled"`
	DhcpRange        string `json:"dhcp_range,omitempty"`
	DhcpReservations string `json:"dhcp_reservations,omitempty"`
	Gateway          string `json:"gateway,omitempty"`
	GatewayV6        string `json:"gateway_v6,omitempty"`
	Tags             string `json:"tags,omitempty"`
	Notes            string `json:"notes,omitempty"`
	PoolTier         string `json:"pool_tier,omitempty"`
}

type auditAllocationChange struct {
	SegmentID   int64  `json:"segment_id"`
	Site        string `json:"site"`
	VRF         string `json:"vrf"`
	VLAN        int    `json:"vlan"`
	Name        string `json:"name"`
	CIDRBefore  string `json:"cidr_before,omitempty"`
	CIDRAfter   string `json:"cidr_after,omitempty"`
	CIDRV6Before string `json:"cidr_v6_before,omitempty"`
	CIDRV6After  string `json:"cidr_v6_after,omitempty"`
}

type auditAllocationSummary struct {
	TotalSegments int                    `json:"total_segments"`
	Changes       []auditAllocationChange `json:"changes"`
}

type auditTemplateSnapshot struct {
	Name     string `json:"name"`
	Source   string `json:"source"`
	Checksum string `json:"checksum"`
	Size     int    `json:"size"`
}

type auditImportSummary struct {
	Source        string   `json:"source"`
	ProjectsAdded int      `json:"projects_added,omitempty"`
	SitesAdded    int      `json:"sites_added,omitempty"`
	PoolsAdded    int      `json:"pools_added,omitempty"`
	SegmentsAdded int      `json:"segments_added,omitempty"`
	Warnings      []string `json:"warnings,omitempty"`
	Errors        []string `json:"errors,omitempty"`
}

type auditDefaultsImportSummary struct {
	Source         string   `json:"source"`
	ProjectUpdated bool     `json:"project_updated"`
	SitesUpdated   int      `json:"sites_updated,omitempty"`
	Warnings       []string `json:"warnings,omitempty"`
	Errors         []string `json:"errors,omitempty"`
}

func auditActor(c *gin.Context) string {
	actor := strings.TrimSpace(c.GetHeader("X-Actor"))
	if actor == "" {
		actor = strings.TrimSpace(c.GetHeader("X-User"))
	}
	if actor == "" {
		actor = strings.TrimSpace(c.PostForm("actor"))
	}
	if actor == "" {
		actor = strings.TrimSpace(c.Query("actor"))
	}
	if actor == "" {
		actor = c.ClientIP()
	}
	if actor == "" {
		actor = "unknown"
	}
	return actor
}

func auditReason(c *gin.Context) string {
	reason := strings.TrimSpace(c.PostForm("reason"))
	if reason == "" {
		reason = strings.TrimSpace(c.Query("reason"))
	}
	return reason
}

func writeAudit(db *sql.DB, c *gin.Context, record auditRecord) {
	if strings.TrimSpace(record.Actor) == "" {
		record.Actor = auditActor(c)
	}
	if !record.Reason.Valid {
		reason := auditReason(c)
		if reason != "" {
			record.Reason = sql.NullString{String: reason, Valid: true}
		}
	}
	if err := insertAuditRecord(db, record); err != nil {
		log.Printf("audit log error: %v", err)
	}
}

func insertAuditRecord(db *sql.DB, record auditRecord) error {
	before, err := marshalAuditPayload(record.Before)
	if err != nil {
		return err
	}
	after, err := marshalAuditPayload(record.After)
	if err != nil {
		return err
	}
	createdAt := time.Now().UTC().Format(time.RFC3339)
	_, err = db.Exec(`
		INSERT INTO audit_log(
			project_id, actor, action, entity_type, entity_id, entity_label, reason, before_json, after_json, created_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		nullInt64ToAny(record.ProjectID),
		record.Actor,
		record.Action,
		record.EntityType,
		nullInt64ToAny(record.EntityID),
		nullStringToAny(record.EntityLabel.String),
		nullStringToAny(record.Reason.String),
		nullStringToAny(before),
		nullStringToAny(after),
		createdAt,
	)
	return err
}

func listAuditEntries(db *sql.DB, projectID int64) ([]AuditEntry, error) {
	query := `
		SELECT id, project_id, actor, action, entity_type, entity_id, entity_label, reason, before_json, after_json, created_at
		FROM audit_log
	`
	var args []any
	if projectID > 0 {
		query += " WHERE project_id=?"
		args = append(args, projectID)
	}
	query += " ORDER BY created_at DESC, id DESC"
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var entry AuditEntry
		if err := rows.Scan(
			&entry.ID,
			&entry.ProjectID,
			&entry.Actor,
			&entry.Action,
			&entry.EntityType,
			&entry.EntityID,
			&entry.EntityLabel,
			&entry.Reason,
			&entry.BeforeJSON,
			&entry.AfterJSON,
			&entry.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}

func marshalAuditPayload(value any) (string, error) {
	if value == nil {
		return "", nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func snapshotProject(p Project) auditProjectSnapshot {
	out := auditProjectSnapshot{
		ID:   p.ID,
		Name: strings.TrimSpace(p.Name),
	}
	if p.Description.Valid {
		out.Description = strings.TrimSpace(p.Description.String)
	}
	return out
}

func snapshotProjectMeta(meta ProjectMeta) auditProjectMetaSnapshot {
	out := auditProjectMetaSnapshot{
		DomainName:    strings.TrimSpace(nullString(meta.DomainName)),
		DNS:           strings.TrimSpace(nullString(meta.DNS)),
		NTP:           strings.TrimSpace(nullString(meta.NTP)),
		GatewayPolicy: strings.TrimSpace(nullString(meta.GatewayPolicy)),
		DhcpSearch:    strings.TrimSpace(nullString(meta.DhcpSearch)),
		DhcpBootFile:  strings.TrimSpace(nullString(meta.DhcpBootFile)),
		DhcpNextServer: strings.TrimSpace(nullString(meta.DhcpNextServer)),
	}
	if meta.DhcpVendorOpts.Valid {
		out.DhcpVendorOpts = splitCSV(meta.DhcpVendorOpts.String)
	}
	out.DhcpLeaseTime = nullIntPtr(meta.DhcpLeaseTime)
	out.DhcpRenewTime = nullIntPtr(meta.DhcpRenewTime)
	out.DhcpRebindTime = nullIntPtr(meta.DhcpRebindTime)
	out.GrowthMonths = nullIntPtr(meta.GrowthMonths)
	out.GrowthRate = nullFloatPtr(meta.GrowthRate)
	return out
}

func snapshotRules(rules ProjectRules) auditRulesSnapshot {
	return auditRulesSnapshot{
		VLANScope:            rules.VLANScope,
		RequireInPool:        rules.RequireInPool,
		AllowReservedOverlap: rules.AllowReservedOverlap,
		OversizeThreshold:    rules.OversizeThreshold,
		PoolStrategy:         rules.PoolStrategy,
		PoolTierFallback:     rules.PoolTierFallback,
	}
}

func snapshotSite(site Site) auditSiteSnapshot {
	out := auditSiteSnapshot{
		ID:             site.ID,
		Name:           strings.TrimSpace(site.Name),
		Project:        strings.TrimSpace(nullString(site.Project)),
		Region:         strings.TrimSpace(nullString(site.Region)),
		DNS:            strings.TrimSpace(nullString(site.DNS)),
		NTP:            strings.TrimSpace(nullString(site.NTP)),
		GatewayPolicy:  strings.TrimSpace(nullString(site.GatewayPolicy)),
		ReservedRanges: strings.TrimSpace(nullString(site.ReservedRanges)),
		DhcpSearch:     strings.TrimSpace(nullString(site.DhcpSearch)),
		DhcpBootFile:   strings.TrimSpace(nullString(site.DhcpBootFile)),
		DhcpNextServer: strings.TrimSpace(nullString(site.DhcpNextServer)),
	}
	if site.DhcpVendorOpts.Valid {
		out.DhcpVendorOpts = splitCSV(site.DhcpVendorOpts.String)
	}
	out.DhcpLeaseTime = nullIntPtr(site.DhcpLeaseTime)
	out.DhcpRenewTime = nullIntPtr(site.DhcpRenewTime)
	out.DhcpRebindTime = nullIntPtr(site.DhcpRebindTime)
	return out
}

func snapshotPool(pool Pool) auditPoolSnapshot {
	out := auditPoolSnapshot{
		ID:       pool.ID,
		Site:     strings.TrimSpace(pool.Site),
		CIDR:     strings.TrimSpace(pool.CIDR),
		Family:   strings.TrimSpace(normalizePoolFamily(pool.Family)),
		Priority: pool.Priority,
	}
	if pool.Tier.Valid {
		out.Tier = strings.TrimSpace(pool.Tier.String)
	}
	return out
}

func snapshotSegment(seg Segment) auditSegmentSnapshot {
	out := auditSegmentSnapshot{
		ID:               seg.ID,
		Site:             strings.TrimSpace(seg.Site),
		VRF:              strings.TrimSpace(seg.VRF),
		VLAN:             seg.VLAN,
		Name:             strings.TrimSpace(seg.Name),
		Hosts:            nullIntPtr(seg.Hosts),
		Prefix:           nullIntPtr(seg.Prefix),
		PrefixV6:         nullIntPtr(seg.PrefixV6),
		CIDR:             strings.TrimSpace(nullString(seg.CIDR)),
		CIDRV6:           strings.TrimSpace(nullString(seg.CIDRV6)),
		Locked:           seg.Locked,
		DhcpEnabled:      seg.DhcpEnabled,
		DhcpRange:        strings.TrimSpace(nullString(seg.DhcpRange)),
		DhcpReservations: strings.TrimSpace(nullString(seg.DhcpReservations)),
		Gateway:          strings.TrimSpace(nullString(seg.Gateway)),
		GatewayV6:        strings.TrimSpace(nullString(seg.GatewayV6)),
		Tags:             strings.TrimSpace(nullString(seg.Tags)),
		Notes:            strings.TrimSpace(nullString(seg.Notes)),
		PoolTier:         strings.TrimSpace(nullString(seg.PoolTier)),
	}
	return out
}

func splitCSV(raw string) []string {
	parts := []string{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		parts = append(parts, part)
	}
	return parts
}

func snapshotTemplate(name, source string, content []byte) auditTemplateSnapshot {
	return auditTemplateSnapshot{
		Name:     name,
		Source:   source,
		Checksum: checksumSHA256(string(content)),
		Size:     len(content),
	}
}

func nullFloatPtr(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	val := v.Float64
	return &val
}

func nullInt64ToAny(v any) any {
	switch cast := v.(type) {
	case sql.NullInt64:
		if cast.Valid {
			return cast.Int64
		}
		return nil
	case int64:
		if cast > 0 {
			return cast
		}
		return nil
	default:
		return nil
	}
}

func buildAllocationSummary(before, after []Segment) auditAllocationSummary {
	beforeByID := make(map[int64]Segment, len(before))
	for _, s := range before {
		beforeByID[s.ID] = s
	}
	summary := auditAllocationSummary{TotalSegments: len(after)}
	for _, s := range after {
		prev, ok := beforeByID[s.ID]
		if !ok {
			continue
		}
		prevCIDR := cidrString(prev.CIDR)
		prevCIDRV6 := cidrString(prev.CIDRV6)
		nextCIDR := cidrString(s.CIDR)
		nextCIDRV6 := cidrString(s.CIDRV6)
		if prevCIDR == nextCIDR && prevCIDRV6 == nextCIDRV6 {
			continue
		}
		summary.Changes = append(summary.Changes, auditAllocationChange{
			SegmentID:    s.ID,
			Site:         s.Site,
			VRF:          s.VRF,
			VLAN:         s.VLAN,
			Name:         s.Name,
			CIDRBefore:   prevCIDR,
			CIDRAfter:    nextCIDR,
			CIDRV6Before: prevCIDRV6,
			CIDRV6After:  nextCIDRV6,
		})
	}
	return summary
}

func siteByID(db *sql.DB, siteID int64) (Site, bool) {
	if siteID <= 0 {
		return Site{}, false
	}
	var site Site
	row := db.QueryRow(`
		SELECT s.id, s.name, p.name,
			m.region, m.dns, m.ntp, m.gateway_policy, m.reserved_ranges,
			m.dhcp_search, m.dhcp_lease_time, m.dhcp_renew_time, m.dhcp_rebind_time,
			m.dhcp_boot_file, m.dhcp_next_server, m.dhcp_vendor_options
		FROM sites s
		LEFT JOIN project_sites ps ON ps.site_id = s.id
		LEFT JOIN projects p ON p.id = ps.project_id
		LEFT JOIN site_meta m ON m.site_id = s.id
		WHERE s.id=?`, siteID)
	if err := row.Scan(
		&site.ID, &site.Name, &site.Project,
		&site.Region, &site.DNS, &site.NTP, &site.GatewayPolicy, &site.ReservedRanges,
		&site.DhcpSearch, &site.DhcpLeaseTime, &site.DhcpRenewTime, &site.DhcpRebindTime,
		&site.DhcpBootFile, &site.DhcpNextServer, &site.DhcpVendorOpts,
	); err != nil {
		return Site{}, false
	}
	return site, true
}

func poolByID(db *sql.DB, poolID int64) (Pool, bool) {
	if poolID <= 0 {
		return Pool{}, false
	}
	var pool Pool
	row := db.QueryRow(`
		SELECT p.id, p.site_id, s.name, p.cidr,
			COALESCE(p.family, 'ipv4'), p.tier, COALESCE(p.priority, 0)
		FROM pools p
		JOIN sites s ON s.id = p.site_id
		WHERE p.id=?`, poolID)
	if err := row.Scan(&pool.ID, &pool.SiteID, &pool.Site, &pool.CIDR, &pool.Family, &pool.Tier, &pool.Priority); err != nil {
		return Pool{}, false
	}
	return pool, true
}

func segmentByID(db *sql.DB, segmentID int64) (Segment, bool) {
	if segmentID <= 0 {
		return Segment{}, false
	}
	var seg Segment
	var locked int
	row := db.QueryRow(`
		SELECT s.id, s.site_id, si.name, s.vrf, s.vlan, s.name, s.hosts, s.prefix, s.cidr,
			s.prefix_v6, s.cidr_v6, s.locked,
			sm.dhcp_enabled, sm.dhcp_range, sm.dhcp_reservations, sm.gateway, sm.gateway_v6,
			sm.notes, sm.tags, sm.pool_tier
		FROM segments s
		JOIN sites si ON si.id = s.site_id
		LEFT JOIN segment_meta sm ON sm.segment_id = s.id
		WHERE s.id=?`, segmentID)
	if err := row.Scan(
		&seg.ID, &seg.SiteID, &seg.Site, &seg.VRF, &seg.VLAN, &seg.Name,
		&seg.Hosts, &seg.Prefix, &seg.CIDR, &seg.PrefixV6, &seg.CIDRV6, &locked,
		&seg.DhcpEnabled, &seg.DhcpRange, &seg.DhcpReservations, &seg.Gateway, &seg.GatewayV6,
		&seg.Notes, &seg.Tags, &seg.PoolTier,
	); err != nil {
		return Segment{}, false
	}
	seg.Locked = locked != 0
	return seg, true
}

func projectIDBySite(db *sql.DB, siteID int64) int64 {
	if siteID <= 0 {
		return 0
	}
	var projectID int64
	if err := db.QueryRow(`SELECT project_id FROM project_sites WHERE site_id=?`, siteID).Scan(&projectID); err != nil {
		return 0
	}
	return projectID
}
