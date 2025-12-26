// Copyright (c) 2025 Berik Ashimov

package main

import (
	"crypto/sha1"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
)

const planSchemaVersion = "2"

const (
	planRowMeta    = "meta"
	planRowRules   = "rules"
	planRowSite    = "site"
	planRowPool    = "pool"
	planRowSegment = "segment"
)

type PlanBundle struct {
	SchemaVersion string    `json:"schema_version" yaml:"schema_version"`
	Rows          []PlanRow `json:"rows" yaml:"rows"`
}

type PlanRow struct {
	RowType       string `json:"row_type" yaml:"row_type"`
	UID           string `json:"uid,omitempty" yaml:"uid,omitempty"`
	Project       string `json:"project,omitempty" yaml:"project,omitempty"`
	SchemaVersion string `json:"schema_version,omitempty" yaml:"schema_version,omitempty"`

	Site           string `json:"site,omitempty" yaml:"site,omitempty"`
	Region         string `json:"region,omitempty" yaml:"region,omitempty"`
	DNS            string `json:"dns,omitempty" yaml:"dns,omitempty"`
	NTP            string `json:"ntp,omitempty" yaml:"ntp,omitempty"`
	GatewayPolicy  string `json:"gateway_policy,omitempty" yaml:"gateway_policy,omitempty"`
	ReservedRanges string `json:"reserved_ranges,omitempty" yaml:"reserved_ranges,omitempty"`
	Pool           string `json:"pool,omitempty" yaml:"pool,omitempty"`
	PoolFamily     string `json:"pool_family,omitempty" yaml:"pool_family,omitempty"`
	PoolTier       string `json:"pool_tier,omitempty" yaml:"pool_tier,omitempty"`
	PoolPriority   *int   `json:"pool_priority,omitempty" yaml:"pool_priority,omitempty"`

	VRF              string `json:"vrf,omitempty" yaml:"vrf,omitempty"`
	VLAN             *int   `json:"vlan,omitempty" yaml:"vlan,omitempty"`
	Name             string `json:"name,omitempty" yaml:"name,omitempty"`
	Hosts            *int   `json:"hosts,omitempty" yaml:"hosts,omitempty"`
	Prefix           *int   `json:"prefix,omitempty" yaml:"prefix,omitempty"`
	CIDR             string `json:"cidr,omitempty" yaml:"cidr,omitempty"`
	PrefixV6         *int   `json:"prefix_v6,omitempty" yaml:"prefix_v6,omitempty"`
	CIDRV6           string `json:"cidr_v6,omitempty" yaml:"cidr_v6,omitempty"`
	Locked           *bool  `json:"locked,omitempty" yaml:"locked,omitempty"`
	DHCP             *bool  `json:"dhcp,omitempty" yaml:"dhcp,omitempty"`
	DHCPRange        string `json:"dhcp_range,omitempty" yaml:"dhcp_range,omitempty"`
	DHCPReservations string `json:"dhcp_reservations,omitempty" yaml:"dhcp_reservations,omitempty"`
	Gateway          string `json:"gateway,omitempty" yaml:"gateway,omitempty"`
	GatewayV6        string `json:"gateway_v6,omitempty" yaml:"gateway_v6,omitempty"`
	Tags             string `json:"tags,omitempty" yaml:"tags,omitempty"`
	Notes            string `json:"notes,omitempty" yaml:"notes,omitempty"`

	DomainName           string   `json:"domain_name,omitempty" yaml:"domain_name,omitempty"`
	ProjectDNS           string   `json:"project_dns,omitempty" yaml:"project_dns,omitempty"`
	ProjectNTP           string   `json:"project_ntp,omitempty" yaml:"project_ntp,omitempty"`
	ProjectGatewayPolicy string   `json:"project_gateway_policy,omitempty" yaml:"project_gateway_policy,omitempty"`
	DHCPSearch           string   `json:"dhcp_search,omitempty" yaml:"dhcp_search,omitempty"`
	DHCPLeaseTime        *int     `json:"dhcp_lease_time,omitempty" yaml:"dhcp_lease_time,omitempty"`
	DHCPRenewTime        *int     `json:"dhcp_renew_time,omitempty" yaml:"dhcp_renew_time,omitempty"`
	DHCPRebindTime       *int     `json:"dhcp_rebind_time,omitempty" yaml:"dhcp_rebind_time,omitempty"`
	DHCPBootFile         string   `json:"dhcp_boot_file,omitempty" yaml:"dhcp_boot_file,omitempty"`
	DHCPNextServer       string   `json:"dhcp_next_server,omitempty" yaml:"dhcp_next_server,omitempty"`
	DHCPVendorOptions    string   `json:"dhcp_vendor_options,omitempty" yaml:"dhcp_vendor_options,omitempty"`
	GrowthRate           *float64 `json:"growth_rate,omitempty" yaml:"growth_rate,omitempty"`
	GrowthMonths         *int     `json:"growth_months,omitempty" yaml:"growth_months,omitempty"`

	VLANScope            string `json:"vlan_scope,omitempty" yaml:"vlan_scope,omitempty"`
	RequireInPool        *bool  `json:"require_in_pool,omitempty" yaml:"require_in_pool,omitempty"`
	AllowReservedOverlap *bool  `json:"allow_reserved_overlap,omitempty" yaml:"allow_reserved_overlap,omitempty"`
	OversizeThreshold    *int   `json:"oversize_threshold,omitempty" yaml:"oversize_threshold,omitempty"`
	PoolStrategy         string `json:"pool_strategy,omitempty" yaml:"pool_strategy,omitempty"`
	PoolTierFallback     *bool  `json:"pool_tier_fallback,omitempty" yaml:"pool_tier_fallback,omitempty"`
}

func exportPlanCSV(c *gin.Context, db *sql.DB, projectID int64) error {
	bundle, err := buildPlanBundle(db, projectID)
	if err != nil {
		return err
	}
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=subnetio_plan.csv")
	w := csv.NewWriter(c.Writer)
	if err := w.Write(planCSVHeaders()); err != nil {
		return err
	}
	for _, row := range bundle.Rows {
		if err := w.Write(planRowToCSV(row)); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

func exportPlanYAML(c *gin.Context, db *sql.DB, projectID int64) error {
	bundle, err := buildPlanBundle(db, projectID)
	if err != nil {
		return err
	}
	out, err := yaml.Marshal(bundle)
	if err != nil {
		return err
	}
	c.Header("Content-Type", "application/x-yaml; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=subnetio_plan.yaml")
	c.String(200, string(out))
	return nil
}

func exportPlanJSON(c *gin.Context, db *sql.DB, projectID int64) error {
	bundle, err := buildPlanBundle(db, projectID)
	if err != nil {
		return err
	}
	out, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return err
	}
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=subnetio_plan.json")
	c.String(200, string(out))
	return nil
}

func buildPlanBundle(db *sql.DB, projectID int64) (PlanBundle, error) {
	project := Project{ID: projectID, Name: "Default"}
	if p, ok := projectByID(db, projectID); ok {
		project = p
	}
	meta, err := getProjectMeta(db, projectID)
	if err != nil {
		return PlanBundle{}, err
	}
	rules, _ := getProjectRules(db, projectID)
	sites, err := listSites(db, projectID)
	if err != nil {
		return PlanBundle{}, err
	}
	pools, err := listPools(db, projectID)
	if err != nil {
		return PlanBundle{}, err
	}
	segments, err := listSegments(db, projectID)
	if err != nil {
		return PlanBundle{}, err
	}
	projectName := strings.TrimSpace(project.Name)
	if projectName == "" {
		projectName = "Default"
	}

	siteProject := map[int64]string{}
	for _, s := range sites {
		name := projectName
		if s.Project.Valid && strings.TrimSpace(s.Project.String) != "" {
			name = strings.TrimSpace(s.Project.String)
		}
		siteProject[s.ID] = name
	}

	var rows []PlanRow
	rows = append(rows, buildPlanMetaRow(projectName, meta))
	rows = append(rows, buildPlanRulesRow(projectName, rules))
	rows = append(rows, buildPlanSiteRows(projectName, sites)...)
	rows = append(rows, buildPlanPoolRows(siteProject, pools)...)
	rows = append(rows, buildPlanSegmentRows(siteProject, segments)...)

	sortPlanRows(rows)

	return PlanBundle{
		SchemaVersion: planSchemaVersion,
		Rows:          rows,
	}, nil
}

func buildPlanMetaRow(projectName string, meta ProjectMeta) PlanRow {
	row := PlanRow{
		RowType:       planRowMeta,
		UID:           stableID(planRowMeta, projectName),
		Project:       projectName,
		SchemaVersion: planSchemaVersion,
	}
	row.DomainName = nullString(meta.DomainName)
	row.ProjectDNS = nullString(meta.DNS)
	row.ProjectNTP = nullString(meta.NTP)
	row.ProjectGatewayPolicy = nullString(meta.GatewayPolicy)
	row.DHCPSearch = nullString(meta.DhcpSearch)
	row.DHCPLeaseTime = nullIntPtr(meta.DhcpLeaseTime)
	row.DHCPRenewTime = nullIntPtr(meta.DhcpRenewTime)
	row.DHCPRebindTime = nullIntPtr(meta.DhcpRebindTime)
	row.DHCPBootFile = nullString(meta.DhcpBootFile)
	row.DHCPNextServer = nullString(meta.DhcpNextServer)
	row.DHCPVendorOptions = nullString(meta.DhcpVendorOpts)
	if meta.GrowthRate.Valid {
		val := meta.GrowthRate.Float64
		row.GrowthRate = &val
	}
	if meta.GrowthMonths.Valid {
		val := int(meta.GrowthMonths.Int64)
		row.GrowthMonths = &val
	}
	return row
}

func buildPlanRulesRow(projectName string, rules ProjectRules) PlanRow {
	requireInPool := rules.RequireInPool
	allowReserved := rules.AllowReservedOverlap
	oversize := rules.OversizeThreshold
	poolFallback := rules.PoolTierFallback
	return PlanRow{
		RowType:              planRowRules,
		UID:                  stableID(planRowRules, projectName),
		Project:              projectName,
		VLANScope:            rules.VLANScope,
		RequireInPool:        &requireInPool,
		AllowReservedOverlap: &allowReserved,
		OversizeThreshold:    &oversize,
		PoolStrategy:         rules.PoolStrategy,
		PoolTierFallback:     &poolFallback,
	}
}

func buildPlanSiteRows(defaultProject string, sites []Site) []PlanRow {
	out := make([]PlanRow, 0, len(sites))
	for _, s := range sites {
		projectName := defaultProject
		if s.Project.Valid && strings.TrimSpace(s.Project.String) != "" {
			projectName = strings.TrimSpace(s.Project.String)
		}
		row := PlanRow{
			RowType:        planRowSite,
			UID:            stableID(planRowSite, projectName, s.Name),
			Project:        projectName,
			Site:           s.Name,
			Region:         nullString(s.Region),
			DNS:            nullString(s.DNS),
			NTP:            nullString(s.NTP),
			GatewayPolicy:  nullString(s.GatewayPolicy),
			ReservedRanges: nullString(s.ReservedRanges),
		}
		out = append(out, row)
	}
	return out
}

func buildPlanPoolRows(siteProject map[int64]string, pools []Pool) []PlanRow {
	out := make([]PlanRow, 0, len(pools))
	for _, p := range pools {
		projectName := siteProject[p.SiteID]
		if projectName == "" {
			projectName = "Default"
		}
		priority := p.Priority
		out = append(out, PlanRow{
			RowType:      planRowPool,
			UID:          stableID(planRowPool, projectName, p.Site, p.CIDR),
			Project:      projectName,
			Site:         p.Site,
			Pool:         p.CIDR,
			PoolFamily:   normalizePoolFamily(p.Family),
			PoolTier:     nullString(p.Tier),
			PoolPriority: &priority,
		})
	}
	return out
}

func buildPlanSegmentRows(siteProject map[int64]string, segments []Segment) []PlanRow {
	out := make([]PlanRow, 0, len(segments))
	for _, s := range segments {
		projectName := siteProject[s.SiteID]
		if projectName == "" {
			projectName = "Default"
		}
		vlan := s.VLAN
		locked := s.Locked
		row := PlanRow{
			RowType:   planRowSegment,
			UID:       stableID(planRowSegment, projectName, s.Site, s.VRF, itoa(s.VLAN), s.Name),
			Project:   projectName,
			Site:      s.Site,
			VRF:       s.VRF,
			VLAN:      &vlan,
			Name:      s.Name,
			Locked:    &locked,
			CIDR:      nullString(s.CIDR),
			CIDRV6:    nullString(s.CIDRV6),
			Gateway:   nullString(s.Gateway),
			GatewayV6: nullString(s.GatewayV6),
			Tags:      nullString(s.Tags),
			Notes:     nullString(s.Notes),
			PoolTier:  nullString(s.PoolTier),
		}
		if s.Hosts.Valid {
			val := int(s.Hosts.Int64)
			row.Hosts = &val
		}
		if s.Prefix.Valid {
			val := int(s.Prefix.Int64)
			row.Prefix = &val
		}
		if s.PrefixV6.Valid {
			val := int(s.PrefixV6.Int64)
			row.PrefixV6 = &val
		}
		if s.DhcpRange.Valid {
			row.DHCPRange = strings.TrimSpace(s.DhcpRange.String)
		}
		if s.DhcpReservations.Valid {
			row.DHCPReservations = strings.TrimSpace(s.DhcpReservations.String)
		}
		hasMeta := s.DhcpEnabled || s.DhcpRange.Valid || s.DhcpReservations.Valid || s.Gateway.Valid || s.GatewayV6.Valid || s.Notes.Valid || s.Tags.Valid || s.PoolTier.Valid
		if hasMeta {
			val := s.DhcpEnabled
			row.DHCP = &val
		}
		out = append(out, row)
	}
	return out
}

func sortPlanRows(rows []PlanRow) {
	typeOrder := map[string]int{
		planRowMeta:    0,
		planRowRules:   1,
		planRowSite:    2,
		planRowPool:    3,
		planRowSegment: 4,
	}
	sort.Slice(rows, func(i, j int) bool {
		a := rows[i]
		b := rows[j]
		ai := typeOrder[a.RowType]
		bi := typeOrder[b.RowType]
		if ai != bi {
			return ai < bi
		}
		if a.Project != b.Project {
			return a.Project < b.Project
		}
		if a.Site != b.Site {
			return a.Site < b.Site
		}
		if a.Pool != b.Pool {
			return a.Pool < b.Pool
		}
		if a.VRF != b.VRF {
			return a.VRF < b.VRF
		}
		av := intValue(a.VLAN)
		bv := intValue(b.VLAN)
		if av != bv {
			return av < bv
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.UID < b.UID
	})
}

func planCSVHeaders() []string {
	return []string{
		"row_type",
		"uid",
		"project",
		"schema_version",
		"site",
		"region",
		"dns",
		"ntp",
		"gateway_policy",
		"reserved_ranges",
		"pool",
		"pool_family",
		"pool_tier",
		"pool_priority",
		"vrf",
		"vlan",
		"name",
		"hosts",
		"prefix",
		"cidr",
		"prefix_v6",
		"cidr_v6",
		"locked",
		"dhcp",
		"dhcp_range",
		"dhcp_reservations",
		"gateway",
		"gateway_v6",
		"tags",
		"notes",
		"domain_name",
		"project_dns",
		"project_ntp",
		"project_gateway_policy",
		"dhcp_search",
		"dhcp_lease_time",
		"dhcp_renew_time",
		"dhcp_rebind_time",
		"dhcp_boot_file",
		"dhcp_next_server",
		"dhcp_vendor_options",
		"growth_rate",
		"growth_months",
		"vlan_scope",
		"require_in_pool",
		"allow_reserved_overlap",
		"oversize_threshold",
		"pool_strategy",
		"pool_tier_fallback",
	}
}

func planRowToCSV(row PlanRow) []string {
	return []string{
		row.RowType,
		row.UID,
		row.Project,
		row.SchemaVersion,
		row.Site,
		row.Region,
		row.DNS,
		row.NTP,
		row.GatewayPolicy,
		row.ReservedRanges,
		row.Pool,
		row.PoolFamily,
		row.PoolTier,
		intPointerString(row.PoolPriority),
		row.VRF,
		intPointerString(row.VLAN),
		row.Name,
		intPointerString(row.Hosts),
		intPointerString(row.Prefix),
		row.CIDR,
		intPointerString(row.PrefixV6),
		row.CIDRV6,
		boolPointerString(row.Locked),
		boolPointerString(row.DHCP),
		row.DHCPRange,
		row.DHCPReservations,
		row.Gateway,
		row.GatewayV6,
		row.Tags,
		row.Notes,
		row.DomainName,
		row.ProjectDNS,
		row.ProjectNTP,
		row.ProjectGatewayPolicy,
		row.DHCPSearch,
		intPointerString(row.DHCPLeaseTime),
		intPointerString(row.DHCPRenewTime),
		intPointerString(row.DHCPRebindTime),
		row.DHCPBootFile,
		row.DHCPNextServer,
		row.DHCPVendorOptions,
		floatPointerString(row.GrowthRate),
		intPointerString(row.GrowthMonths),
		row.VLANScope,
		boolPointerString(row.RequireInPool),
		boolPointerString(row.AllowReservedOverlap),
		intPointerString(row.OversizeThreshold),
		row.PoolStrategy,
		boolPointerString(row.PoolTierFallback),
	}
}

func stableID(kind string, parts ...string) string {
	h := sha1.New()
	_, _ = io.WriteString(h, kind)
	for _, part := range parts {
		_, _ = io.WriteString(h, "\x00")
		_, _ = io.WriteString(h, strings.ToLower(strings.TrimSpace(part)))
	}
	sum := h.Sum(nil)
	return kind + "_" + hex.EncodeToString(sum[:8])
}

func nullIntPtr(v sql.NullInt64) *int {
	if !v.Valid {
		return nil
	}
	val := int(v.Int64)
	return &val
}

func intValue(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func intPointerString(v *int) string {
	if v == nil {
		return ""
	}
	return itoa(*v)
}

func floatPointerString(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', -1, 64)
}

func boolPointerString(v *bool) string {
	if v == nil {
		return ""
	}
	if *v {
		return "true"
	}
	return "false"
}
