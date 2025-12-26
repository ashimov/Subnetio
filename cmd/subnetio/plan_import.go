// Copyright (c) 2025 Berik Ashimov

package main

import (
	"bytes"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
)

func importPlanCSV(c *gin.Context, db *sql.DB, activeProjectID int64) *ImportReport {
	report := &ImportReport{}
	state := newPlanImportState()
	fileHeader, err := c.FormFile("file")
	if err != nil {
		report.Errors = append(report.Errors, "upload failed: "+err.Error())
		return report
	}
	file, err := fileHeader.Open()
	if err != nil {
		report.Errors = append(report.Errors, "open file: "+err.Error())
		return report
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1

	first, err := reader.Read()
	if err == io.EOF {
		report.Errors = append(report.Errors, "empty CSV file")
		return report
	}
	if err != nil {
		report.Errors = append(report.Errors, "read CSV: "+err.Error())
		return report
	}
	if !looksLikeHeader(first) {
		report.Errors = append(report.Errors, "CSV header is required for strict schema")
		return report
	}
	cols, err := mapPlanColumns(first)
	if err != nil {
		report.Errors = append(report.Errors, err.Error())
		return report
	}
	state.setCSVColumns(cols)

	rowIndex := 1
	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		rowIndex++
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("row %d: %v", rowIndex, err))
			continue
		}
		planRow, err := planRowFromCSV(cols, row)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("row %d: %v", rowIndex, err))
			continue
		}
		if err := applyPlanRow(db, report, state, planRow, rowIndex, activeProjectID, "csv"); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("row %d: %v", rowIndex, err))
		}
	}
	state.finalize(report)
	return report
}

func importPlanJSON(c *gin.Context, db *sql.DB, activeProjectID int64) *ImportReport {
	return importPlanBundle(c, db, activeProjectID, "json")
}

func isSupportedSchemaVersion(v string) bool {
	return v == "1" || v == "2"
}

func importPlanYAML(c *gin.Context, db *sql.DB, activeProjectID int64) *ImportReport {
	return importPlanBundle(c, db, activeProjectID, "yaml")
}

func importPlanBundle(c *gin.Context, db *sql.DB, activeProjectID int64, format string) *ImportReport {
	report := &ImportReport{}
	state := newPlanImportState()
	fileHeader, err := c.FormFile("file")
	if err != nil {
		report.Errors = append(report.Errors, "upload failed: "+err.Error())
		return report
	}
	file, err := fileHeader.Open()
	if err != nil {
		report.Errors = append(report.Errors, "open file: "+err.Error())
		return report
	}
	defer file.Close()

	raw, err := io.ReadAll(file)
	if err != nil {
		report.Errors = append(report.Errors, "read file: "+err.Error())
		return report
	}

	var bundle PlanBundle
	switch format {
	case "json":
		if err := decodePlanJSON(raw, &bundle); err != nil {
			report.Errors = append(report.Errors, "parse json: "+err.Error())
			return report
		}
	case "yaml":
		if err := decodePlanYAML(raw, &bundle); err != nil {
			report.Errors = append(report.Errors, "parse yaml: "+err.Error())
			return report
		}
	default:
		report.Errors = append(report.Errors, "unsupported format")
		return report
	}

	if bundle.SchemaVersion == "" {
		report.Errors = append(report.Errors, "schema_version is required")
		return report
	}
	if !isSupportedSchemaVersion(bundle.SchemaVersion) {
		report.Errors = append(report.Errors, fmt.Sprintf("schema_version mismatch: %s", bundle.SchemaVersion))
		return report
	}
	for i, row := range bundle.Rows {
		rowIndex := i + 1
		if err := applyPlanRow(db, report, state, row, rowIndex, activeProjectID, format); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("row %d: %v", rowIndex, err))
		}
	}
	state.finalize(report)
	return report
}

func decodePlanJSON(raw []byte, bundle *PlanBundle) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(bundle)
}

func decodePlanYAML(raw []byte, bundle *PlanBundle) error {
	var anyData any
	if err := yaml.Unmarshal(raw, &anyData); err != nil {
		return err
	}
	asJSON, err := json.Marshal(anyData)
	if err != nil {
		return err
	}
	return decodePlanJSON(asJSON, bundle)
}

type planColumns struct {
	RowType              int
	UID                  int
	Project              int
	SchemaVersion        int
	Site                 int
	Region               int
	DNS                  int
	NTP                  int
	GatewayPolicy        int
	ReservedRanges       int
	Pool                 int
	PoolFamily           int
	PoolTier             int
	PoolPriority         int
	VRF                  int
	VLAN                 int
	Name                 int
	Hosts                int
	Prefix               int
	CIDR                 int
	PrefixV6             int
	CIDRV6               int
	Locked               int
	DHCP                 int
	DHCPRange            int
	DHCPReservations     int
	Gateway              int
	GatewayV6            int
	Tags                 int
	Notes                int
	DomainName           int
	ProjectDNS           int
	ProjectNTP           int
	ProjectGatewayPolicy int
	DHCPSearch           int
	DHCPLeaseTime        int
	DHCPRenewTime        int
	DHCPRebindTime       int
	DHCPBootFile         int
	DHCPNextServer       int
	DHCPVendorOptions    int
	GrowthRate           int
	GrowthMonths         int
	VLANScope            int
	RequireInPool        int
	AllowReservedOverlap int
	OversizeThreshold    int
	PoolStrategy         int
	PoolTierFallback     int
}

func mapPlanColumns(header []string) (planColumns, error) {
	cols := planColumns{
		RowType:              -1,
		UID:                  -1,
		Project:              -1,
		SchemaVersion:        -1,
		Site:                 -1,
		Region:               -1,
		DNS:                  -1,
		NTP:                  -1,
		GatewayPolicy:        -1,
		ReservedRanges:       -1,
		Pool:                 -1,
		PoolFamily:           -1,
		PoolTier:             -1,
		PoolPriority:         -1,
		VRF:                  -1,
		VLAN:                 -1,
		Name:                 -1,
		Hosts:                -1,
		Prefix:               -1,
		CIDR:                 -1,
		PrefixV6:             -1,
		CIDRV6:               -1,
		Locked:               -1,
		DHCP:                 -1,
		DHCPRange:            -1,
		DHCPReservations:     -1,
		Gateway:              -1,
		GatewayV6:            -1,
		Tags:                 -1,
		Notes:                -1,
		DomainName:           -1,
		ProjectDNS:           -1,
		ProjectNTP:           -1,
		ProjectGatewayPolicy: -1,
		DHCPSearch:           -1,
		DHCPLeaseTime:        -1,
		DHCPRenewTime:        -1,
		DHCPRebindTime:       -1,
		DHCPBootFile:         -1,
		DHCPNextServer:       -1,
		DHCPVendorOptions:    -1,
		GrowthRate:           -1,
		GrowthMonths:         -1,
		VLANScope:            -1,
		RequireInPool:        -1,
		AllowReservedOverlap: -1,
		OversizeThreshold:    -1,
		PoolStrategy:         -1,
		PoolTierFallback:     -1,
	}
	var unknown []string
	for i, raw := range header {
		name := normalizeHeader(raw)
		switch name {
		case "rowtype", "type":
			cols.RowType = i
		case "uid", "stableid", "stable":
			cols.UID = i
		case "project", "projectname":
			cols.Project = i
		case "schemaversion", "schema":
			cols.SchemaVersion = i
		case "site", "sitename":
			cols.Site = i
		case "region":
			cols.Region = i
		case "dns":
			cols.DNS = i
		case "ntp":
			cols.NTP = i
		case "gatewaypolicy":
			cols.GatewayPolicy = i
		case "reservedranges":
			cols.ReservedRanges = i
		case "pool":
			cols.Pool = i
		case "poolfamily":
			cols.PoolFamily = i
		case "pooltier":
			cols.PoolTier = i
		case "poolpriority":
			cols.PoolPriority = i
		case "vrf":
			cols.VRF = i
		case "vlan":
			cols.VLAN = i
		case "name":
			cols.Name = i
		case "hosts":
			cols.Hosts = i
		case "prefix":
			cols.Prefix = i
		case "cidr":
			cols.CIDR = i
		case "prefixv6":
			cols.PrefixV6 = i
		case "cidrv6":
			cols.CIDRV6 = i
		case "locked":
			cols.Locked = i
		case "dhcp":
			cols.DHCP = i
		case "dhcprange":
			cols.DHCPRange = i
		case "dhcpreservations":
			cols.DHCPReservations = i
		case "gateway":
			cols.Gateway = i
		case "gatewayv6":
			cols.GatewayV6 = i
		case "tags":
			cols.Tags = i
		case "notes":
			cols.Notes = i
		case "domainname":
			cols.DomainName = i
		case "projectdns":
			cols.ProjectDNS = i
		case "projectntp":
			cols.ProjectNTP = i
		case "projectgatewaypolicy":
			cols.ProjectGatewayPolicy = i
		case "dhcpsearch":
			cols.DHCPSearch = i
		case "dhcpleasetime":
			cols.DHCPLeaseTime = i
		case "dhcprenewtime":
			cols.DHCPRenewTime = i
		case "dhcprebindtime":
			cols.DHCPRebindTime = i
		case "dhcpbootfile":
			cols.DHCPBootFile = i
		case "dhcpnextserver":
			cols.DHCPNextServer = i
		case "dhcpvendoroptions":
			cols.DHCPVendorOptions = i
		case "growthrate":
			cols.GrowthRate = i
		case "growthmonths":
			cols.GrowthMonths = i
		case "vlanscope":
			cols.VLANScope = i
		case "requireinpool":
			cols.RequireInPool = i
		case "allowreservedoverlap":
			cols.AllowReservedOverlap = i
		case "oversizethreshold":
			cols.OversizeThreshold = i
		case "poolstrategy":
			cols.PoolStrategy = i
		case "pooltierfallback":
			cols.PoolTierFallback = i
		default:
			if name != "" {
				unknown = append(unknown, raw)
			}
		}
	}
	if len(unknown) > 0 {
		return cols, fmt.Errorf("unknown columns: %s", strings.Join(unknown, ", "))
	}
	missing := missingPlanColumns(cols)
	if len(missing) > 0 {
		return cols, fmt.Errorf("missing columns: %s", strings.Join(missing, ", "))
	}
	return cols, nil
}

func missingPlanColumns(cols planColumns) []string {
	type pair struct {
		name  string
		value int
	}
	fields := []pair{
		{"row_type", cols.RowType},
		{"uid", cols.UID},
		{"project", cols.Project},
		{"schema_version", cols.SchemaVersion},
		{"site", cols.Site},
		{"region", cols.Region},
		{"dns", cols.DNS},
		{"ntp", cols.NTP},
		{"gateway_policy", cols.GatewayPolicy},
		{"reserved_ranges", cols.ReservedRanges},
		{"pool", cols.Pool},
		{"vrf", cols.VRF},
		{"vlan", cols.VLAN},
		{"name", cols.Name},
		{"hosts", cols.Hosts},
		{"prefix", cols.Prefix},
		{"cidr", cols.CIDR},
		{"locked", cols.Locked},
		{"dhcp", cols.DHCP},
		{"dhcp_range", cols.DHCPRange},
		{"dhcp_reservations", cols.DHCPReservations},
		{"gateway", cols.Gateway},
		{"tags", cols.Tags},
		{"notes", cols.Notes},
		{"domain_name", cols.DomainName},
		{"project_dns", cols.ProjectDNS},
		{"project_ntp", cols.ProjectNTP},
		{"project_gateway_policy", cols.ProjectGatewayPolicy},
		{"dhcp_search", cols.DHCPSearch},
		{"dhcp_lease_time", cols.DHCPLeaseTime},
		{"dhcp_renew_time", cols.DHCPRenewTime},
		{"dhcp_rebind_time", cols.DHCPRebindTime},
		{"dhcp_boot_file", cols.DHCPBootFile},
		{"dhcp_next_server", cols.DHCPNextServer},
		{"dhcp_vendor_options", cols.DHCPVendorOptions},
		{"vlan_scope", cols.VLANScope},
		{"require_in_pool", cols.RequireInPool},
		{"allow_reserved_overlap", cols.AllowReservedOverlap},
		{"oversize_threshold", cols.OversizeThreshold},
	}
	var missing []string
	for _, field := range fields {
		if field.value == -1 {
			missing = append(missing, field.name)
		}
	}
	return missing
}

func missingPlanColumnsForSchema(cols planColumns, version string) []string {
	missing := missingPlanColumns(cols)
	if version != "2" {
		return missing
	}
	if cols.PoolFamily == -1 {
		missing = append(missing, "pool_family")
	}
	if cols.PoolTier == -1 {
		missing = append(missing, "pool_tier")
	}
	if cols.PoolPriority == -1 {
		missing = append(missing, "pool_priority")
	}
	if cols.PrefixV6 == -1 {
		missing = append(missing, "prefix_v6")
	}
	if cols.CIDRV6 == -1 {
		missing = append(missing, "cidr_v6")
	}
	if cols.GatewayV6 == -1 {
		missing = append(missing, "gateway_v6")
	}
	if cols.GrowthRate == -1 {
		missing = append(missing, "growth_rate")
	}
	if cols.GrowthMonths == -1 {
		missing = append(missing, "growth_months")
	}
	if cols.PoolStrategy == -1 {
		missing = append(missing, "pool_strategy")
	}
	if cols.PoolTierFallback == -1 {
		missing = append(missing, "pool_tier_fallback")
	}
	return missing
}

func planRowFromCSV(cols planColumns, row []string) (PlanRow, error) {
	get := func(idx int) string {
		if idx < 0 || idx >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[idx])
	}

	rowType := strings.ToLower(strings.TrimSpace(get(cols.RowType)))
	vlan, err := parseOptionalInt(get(cols.VLAN))
	if err != nil {
		return PlanRow{}, fmt.Errorf("vlan: %w", err)
	}
	hosts, err := parseOptionalInt(get(cols.Hosts))
	if err != nil {
		return PlanRow{}, fmt.Errorf("hosts: %w", err)
	}
	prefix, err := parseOptionalInt(get(cols.Prefix))
	if err != nil {
		return PlanRow{}, fmt.Errorf("prefix: %w", err)
	}
	prefixV6, err := parseOptionalInt(get(cols.PrefixV6))
	if err != nil {
		return PlanRow{}, fmt.Errorf("prefix_v6: %w", err)
	}
	locked, err := parseOptionalBool(get(cols.Locked))
	if err != nil {
		return PlanRow{}, fmt.Errorf("locked: %w", err)
	}
	dhcp, err := parseOptionalBool(get(cols.DHCP))
	if err != nil {
		return PlanRow{}, fmt.Errorf("dhcp: %w", err)
	}
	dhcpLease, err := parseOptionalInt(get(cols.DHCPLeaseTime))
	if err != nil {
		return PlanRow{}, fmt.Errorf("dhcp_lease_time: %w", err)
	}
	dhcpRenew, err := parseOptionalInt(get(cols.DHCPRenewTime))
	if err != nil {
		return PlanRow{}, fmt.Errorf("dhcp_renew_time: %w", err)
	}
	dhcpRebind, err := parseOptionalInt(get(cols.DHCPRebindTime))
	if err != nil {
		return PlanRow{}, fmt.Errorf("dhcp_rebind_time: %w", err)
	}
	requireInPool, err := parseOptionalBool(get(cols.RequireInPool))
	if err != nil {
		return PlanRow{}, fmt.Errorf("require_in_pool: %w", err)
	}
	allowReserved, err := parseOptionalBool(get(cols.AllowReservedOverlap))
	if err != nil {
		return PlanRow{}, fmt.Errorf("allow_reserved_overlap: %w", err)
	}
	oversize, err := parseOptionalInt(get(cols.OversizeThreshold))
	if err != nil {
		return PlanRow{}, fmt.Errorf("oversize_threshold: %w", err)
	}
	poolPriority, err := parseOptionalInt(get(cols.PoolPriority))
	if err != nil {
		return PlanRow{}, fmt.Errorf("pool_priority: %w", err)
	}
	growthRate, err := parseOptionalFloat(get(cols.GrowthRate))
	if err != nil {
		return PlanRow{}, fmt.Errorf("growth_rate: %w", err)
	}
	growthMonths, err := parseOptionalInt(get(cols.GrowthMonths))
	if err != nil {
		return PlanRow{}, fmt.Errorf("growth_months: %w", err)
	}
	poolTierFallback, err := parseOptionalBool(get(cols.PoolTierFallback))
	if err != nil {
		return PlanRow{}, fmt.Errorf("pool_tier_fallback: %w", err)
	}

	return PlanRow{
		RowType:              rowType,
		UID:                  get(cols.UID),
		Project:              get(cols.Project),
		SchemaVersion:        get(cols.SchemaVersion),
		Site:                 get(cols.Site),
		Region:               get(cols.Region),
		DNS:                  get(cols.DNS),
		NTP:                  get(cols.NTP),
		GatewayPolicy:        get(cols.GatewayPolicy),
		ReservedRanges:       get(cols.ReservedRanges),
		Pool:                 get(cols.Pool),
		PoolFamily:           get(cols.PoolFamily),
		PoolTier:             get(cols.PoolTier),
		PoolPriority:         poolPriority,
		VRF:                  get(cols.VRF),
		VLAN:                 vlan,
		Name:                 get(cols.Name),
		Hosts:                hosts,
		Prefix:               prefix,
		CIDR:                 get(cols.CIDR),
		PrefixV6:             prefixV6,
		CIDRV6:               get(cols.CIDRV6),
		Locked:               locked,
		DHCP:                 dhcp,
		DHCPRange:            get(cols.DHCPRange),
		DHCPReservations:     get(cols.DHCPReservations),
		Gateway:              get(cols.Gateway),
		GatewayV6:            get(cols.GatewayV6),
		Tags:                 get(cols.Tags),
		Notes:                get(cols.Notes),
		DomainName:           get(cols.DomainName),
		ProjectDNS:           get(cols.ProjectDNS),
		ProjectNTP:           get(cols.ProjectNTP),
		ProjectGatewayPolicy: get(cols.ProjectGatewayPolicy),
		DHCPSearch:           get(cols.DHCPSearch),
		DHCPLeaseTime:        dhcpLease,
		DHCPRenewTime:        dhcpRenew,
		DHCPRebindTime:       dhcpRebind,
		DHCPBootFile:         get(cols.DHCPBootFile),
		DHCPNextServer:       get(cols.DHCPNextServer),
		DHCPVendorOptions:    get(cols.DHCPVendorOptions),
		GrowthRate:           growthRate,
		GrowthMonths:         growthMonths,
		VLANScope:            get(cols.VLANScope),
		RequireInPool:        requireInPool,
		AllowReservedOverlap: allowReserved,
		OversizeThreshold:    oversize,
		PoolStrategy:         get(cols.PoolStrategy),
		PoolTierFallback:     poolTierFallback,
	}, nil
}

func applyPlanRow(db *sql.DB, report *ImportReport, state *planImportState, row PlanRow, rowIndex int, activeProjectID int64, source string) error {
	rowType := strings.TrimSpace(strings.ToLower(row.RowType))
	switch rowType {
	case planRowMeta, planRowRules, planRowSite, planRowPool, planRowSegment:
	default:
		return fmt.Errorf("invalid row_type: %s", row.RowType)
	}

	projectID, projectName, created, err := resolveProjectID(db, row.Project, activeProjectID)
	if err != nil {
		return err
	}
	if created {
		report.ProjectsAdded++
	}
	state.registerProject(projectName)

	expectedUID := expectedPlanUID(rowType, projectName, row)
	if row.UID != "" && expectedUID != "" && row.UID != expectedUID {
		return fmt.Errorf("uid mismatch (expected %s)", expectedUID)
	}

	switch rowType {
	case planRowMeta:
		if err := validateMetaRow(row); err != nil {
			return err
		}
		if row.SchemaVersion == "" {
			return fmt.Errorf("schema_version required for meta row")
		}
		if !isSupportedSchemaVersion(row.SchemaVersion) {
			return fmt.Errorf("schema_version mismatch: %s", row.SchemaVersion)
		}
		if err := state.validateSchemaColumns(row.SchemaVersion); err != nil {
			return err
		}
		if state.metaSeen(projectName) {
			return fmt.Errorf("duplicate meta row for project")
		}
		state.markMeta(projectName)
		return applyPlanMetaRow(db, projectID, row)
	case planRowRules:
		if err := validateRulesRow(row); err != nil {
			return err
		}
		if state.rulesSeen(projectName) {
			return fmt.Errorf("duplicate rules row for project")
		}
		state.markRules(projectName)
		return applyPlanRulesRow(db, projectID, row)
	case planRowSite:
		if err := validateSiteRow(row); err != nil {
			return err
		}
		return applyPlanSiteRow(db, report, projectID, row)
	case planRowPool:
		if err := validatePoolRow(row); err != nil {
			return err
		}
		return applyPlanPoolRow(db, report, projectID, row)
	case planRowSegment:
		if err := validateSegmentRow(row); err != nil {
			return err
		}
		return applyPlanSegmentRow(db, report, projectID, row, rowIndex, source)
	}
	return nil
}

func validateMetaRow(row PlanRow) error {
	if row.Site != "" || row.Region != "" || row.DNS != "" || row.NTP != "" || row.GatewayPolicy != "" || row.ReservedRanges != "" {
		return fmt.Errorf("meta row cannot include site fields")
	}
	if row.Pool != "" || row.PoolFamily != "" || row.PoolTier != "" || row.PoolPriority != nil || row.VRF != "" || row.Name != "" || row.CIDR != "" || row.CIDRV6 != "" {
		return fmt.Errorf("meta row cannot include segment fields")
	}
	if row.VLAN != nil || row.Hosts != nil || row.Prefix != nil || row.PrefixV6 != nil || row.Locked != nil || row.DHCP != nil {
		return fmt.Errorf("meta row cannot include numeric/boolean segment fields")
	}
	if row.VLANScope != "" || row.RequireInPool != nil || row.AllowReservedOverlap != nil || row.OversizeThreshold != nil || row.PoolStrategy != "" || row.PoolTierFallback != nil {
		return fmt.Errorf("meta row cannot include rules fields")
	}
	return nil
}

func validateRulesRow(row PlanRow) error {
	if row.VLANScope == "" {
		return fmt.Errorf("vlan_scope is required")
	}
	if row.RequireInPool == nil || row.AllowReservedOverlap == nil || row.OversizeThreshold == nil {
		return fmt.Errorf("rules booleans and oversize_threshold are required")
	}
	if row.PoolStrategy != "" {
		strategy := strings.ToLower(strings.TrimSpace(row.PoolStrategy))
		if strategy != PoolStrategySpillover && strategy != PoolStrategyContig && strategy != PoolStrategyTiered {
			return fmt.Errorf("invalid pool_strategy: %s", row.PoolStrategy)
		}
	}
	if row.Site != "" || row.Region != "" || row.DNS != "" || row.NTP != "" || row.GatewayPolicy != "" || row.ReservedRanges != "" {
		return fmt.Errorf("rules row cannot include site fields")
	}
	if row.Pool != "" || row.PoolFamily != "" || row.PoolTier != "" || row.PoolPriority != nil || row.VRF != "" || row.Name != "" || row.CIDR != "" || row.CIDRV6 != "" {
		return fmt.Errorf("rules row cannot include segment fields")
	}
	if row.VLAN != nil || row.Hosts != nil || row.Prefix != nil || row.PrefixV6 != nil || row.Locked != nil || row.DHCP != nil {
		return fmt.Errorf("rules row cannot include numeric/boolean segment fields")
	}
	if row.DomainName != "" || row.ProjectDNS != "" || row.ProjectNTP != "" || row.ProjectGatewayPolicy != "" || row.DHCPSearch != "" || row.DHCPLeaseTime != nil || row.DHCPRenewTime != nil || row.DHCPRebindTime != nil || row.DHCPBootFile != "" || row.DHCPNextServer != "" || row.DHCPVendorOptions != "" || row.GrowthRate != nil || row.GrowthMonths != nil {
		return fmt.Errorf("rules row cannot include meta fields")
	}
	return nil
}

func validateSiteRow(row PlanRow) error {
	if strings.TrimSpace(row.Site) == "" {
		return fmt.Errorf("site is required")
	}
	if row.Pool != "" || row.PoolFamily != "" || row.PoolTier != "" || row.PoolPriority != nil || row.VRF != "" || row.Name != "" || row.CIDR != "" || row.CIDRV6 != "" {
		return fmt.Errorf("site row cannot include segment fields")
	}
	if row.VLAN != nil || row.Hosts != nil || row.Prefix != nil || row.PrefixV6 != nil || row.Locked != nil || row.DHCP != nil {
		return fmt.Errorf("site row cannot include numeric/boolean segment fields")
	}
	if row.DomainName != "" || row.ProjectDNS != "" || row.ProjectNTP != "" || row.ProjectGatewayPolicy != "" || row.DHCPSearch != "" || row.DHCPLeaseTime != nil || row.DHCPRenewTime != nil || row.DHCPRebindTime != nil || row.DHCPBootFile != "" || row.DHCPNextServer != "" || row.DHCPVendorOptions != "" || row.GrowthRate != nil || row.GrowthMonths != nil {
		return fmt.Errorf("site row cannot include meta fields")
	}
	if row.VLANScope != "" || row.RequireInPool != nil || row.AllowReservedOverlap != nil || row.OversizeThreshold != nil || row.PoolStrategy != "" || row.PoolTierFallback != nil {
		return fmt.Errorf("site row cannot include rules fields")
	}
	return nil
}

func validatePoolRow(row PlanRow) error {
	if strings.TrimSpace(row.Site) == "" {
		return fmt.Errorf("site is required")
	}
	if strings.TrimSpace(row.Pool) == "" {
		return fmt.Errorf("pool is required")
	}
	if row.PoolFamily != "" {
		family := strings.ToLower(strings.TrimSpace(row.PoolFamily))
		if family != "ipv4" && family != "ipv6" {
			return fmt.Errorf("invalid pool_family: %s", row.PoolFamily)
		}
	}
	if row.VRF != "" || row.Name != "" || row.CIDR != "" || row.CIDRV6 != "" {
		return fmt.Errorf("pool row cannot include segment fields")
	}
	if row.VLAN != nil || row.Hosts != nil || row.Prefix != nil || row.PrefixV6 != nil || row.Locked != nil || row.DHCP != nil {
		return fmt.Errorf("pool row cannot include numeric/boolean segment fields")
	}
	if row.DomainName != "" || row.ProjectDNS != "" || row.ProjectNTP != "" || row.ProjectGatewayPolicy != "" || row.DHCPSearch != "" || row.DHCPLeaseTime != nil || row.DHCPRenewTime != nil || row.DHCPRebindTime != nil || row.DHCPBootFile != "" || row.DHCPNextServer != "" || row.DHCPVendorOptions != "" || row.GrowthRate != nil || row.GrowthMonths != nil {
		return fmt.Errorf("pool row cannot include meta fields")
	}
	if row.VLANScope != "" || row.RequireInPool != nil || row.AllowReservedOverlap != nil || row.OversizeThreshold != nil || row.PoolStrategy != "" || row.PoolTierFallback != nil {
		return fmt.Errorf("pool row cannot include rules fields")
	}
	return nil
}

func validateSegmentRow(row PlanRow) error {
	if strings.TrimSpace(row.Site) == "" {
		return fmt.Errorf("site is required")
	}
	if strings.TrimSpace(row.VRF) == "" {
		return fmt.Errorf("vrf is required")
	}
	if row.VLAN == nil || *row.VLAN <= 0 {
		return fmt.Errorf("vlan is required")
	}
	if strings.TrimSpace(row.Name) == "" {
		return fmt.Errorf("segment name is required")
	}
	if row.Locked == nil {
		return fmt.Errorf("locked is required")
	}
	if row.DomainName != "" || row.ProjectDNS != "" || row.ProjectNTP != "" || row.ProjectGatewayPolicy != "" || row.DHCPSearch != "" || row.DHCPLeaseTime != nil || row.DHCPRenewTime != nil || row.DHCPRebindTime != nil || row.DHCPBootFile != "" || row.DHCPNextServer != "" || row.DHCPVendorOptions != "" || row.GrowthRate != nil || row.GrowthMonths != nil {
		return fmt.Errorf("segment row cannot include meta fields")
	}
	if row.VLANScope != "" || row.RequireInPool != nil || row.AllowReservedOverlap != nil || row.OversizeThreshold != nil || row.PoolStrategy != "" || row.PoolTierFallback != nil {
		return fmt.Errorf("segment row cannot include rules fields")
	}
	if row.Region != "" || row.DNS != "" || row.NTP != "" || row.GatewayPolicy != "" || row.ReservedRanges != "" {
		return fmt.Errorf("segment row cannot include site fields")
	}
	if row.Pool != "" {
		return fmt.Errorf("segment row cannot include pool")
	}
	if row.PoolFamily != "" || row.PoolPriority != nil {
		return fmt.Errorf("segment row cannot include pool family/priority")
	}
	if row.DHCP == nil && (row.DHCPRange != "" || row.DHCPReservations != "" || row.Gateway != "" || row.GatewayV6 != "" || row.Tags != "" || row.Notes != "" || row.PoolTier != "") {
		return fmt.Errorf("dhcp flag required when segment meta fields are provided")
	}
	if row.CIDR != "" {
		if _, err := netip.ParsePrefix(row.CIDR); err != nil {
			return fmt.Errorf("invalid cidr: %s", row.CIDR)
		}
	}
	if row.CIDRV6 != "" {
		if p, err := netip.ParsePrefix(row.CIDRV6); err != nil || !p.Addr().Is6() {
			return fmt.Errorf("invalid cidr_v6: %s", row.CIDRV6)
		}
	}
	if row.Prefix != nil {
		if *row.Prefix < 1 || *row.Prefix > 32 {
			return fmt.Errorf("invalid prefix: %d", *row.Prefix)
		}
	}
	if row.PrefixV6 != nil {
		if *row.PrefixV6 < 1 || *row.PrefixV6 > 128 {
			return fmt.Errorf("invalid prefix_v6: %d", *row.PrefixV6)
		}
	}
	return nil
}

func applyPlanMetaRow(db *sql.DB, projectID int64, row PlanRow) error {
	meta := ProjectMeta{
		ProjectID:      projectID,
		DomainName:     parseNullString(row.DomainName),
		DNS:            parseNullString(row.ProjectDNS),
		NTP:            parseNullString(row.ProjectNTP),
		GatewayPolicy:  parseNullString(row.ProjectGatewayPolicy),
		DhcpSearch:     parseNullString(row.DHCPSearch),
		DhcpLeaseTime:  intPtrToNull(row.DHCPLeaseTime),
		DhcpRenewTime:  intPtrToNull(row.DHCPRenewTime),
		DhcpRebindTime: intPtrToNull(row.DHCPRebindTime),
		DhcpBootFile:   parseNullString(row.DHCPBootFile),
		DhcpNextServer: parseNullString(row.DHCPNextServer),
		DhcpVendorOpts: parseNullString(row.DHCPVendorOptions),
		GrowthRate:     floatPtrToNull(row.GrowthRate),
		GrowthMonths:   intPtrToNull(row.GrowthMonths),
	}
	return saveProjectMeta(db, meta)
}

func applyPlanRulesRow(db *sql.DB, projectID int64, row PlanRow) error {
	strategy := strings.ToLower(strings.TrimSpace(row.PoolStrategy))
	if strategy == "" {
		strategy = PoolStrategySpillover
	}
	fallback := true
	if row.PoolTierFallback != nil {
		fallback = boolValue(row.PoolTierFallback)
	}
	rules := ProjectRules{
		VLANScope:            strings.TrimSpace(row.VLANScope),
		RequireInPool:        boolValue(row.RequireInPool),
		AllowReservedOverlap: boolValue(row.AllowReservedOverlap),
		OversizeThreshold:    intValue(row.OversizeThreshold),
		PoolStrategy:         strategy,
		PoolTierFallback:     fallback,
	}
	return saveProjectRules(db, projectID, rules)
}

func applyPlanSiteRow(db *sql.DB, report *ImportReport, projectID int64, row PlanRow) error {
	siteID, created, err := getOrCreateSiteID(db, row.Site)
	if err != nil {
		return fmt.Errorf("site error: %v", err)
	}
	if created {
		report.SitesAdded++
	}
	_, _ = db.Exec(`INSERT INTO project_sites(project_id, site_id) VALUES(?, ?) ON CONFLICT(site_id) DO UPDATE SET project_id=excluded.project_id`, projectID, siteID)
	_, err = db.Exec(`
		INSERT INTO site_meta(site_id, region, dns, ntp, gateway_policy, reserved_ranges)
		VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(site_id) DO UPDATE SET
			region=excluded.region,
			dns=excluded.dns,
			ntp=excluded.ntp,
			gateway_policy=excluded.gateway_policy,
			reserved_ranges=excluded.reserved_ranges`,
		siteID,
		nullStringToAny(row.Region),
		nullStringToAny(row.DNS),
		nullStringToAny(row.NTP),
		nullStringToAny(row.GatewayPolicy),
		nullStringToAny(row.ReservedRanges),
	)
	return err
}

func applyPlanPoolRow(db *sql.DB, report *ImportReport, projectID int64, row PlanRow) error {
	siteID, created, err := getOrCreateSiteID(db, row.Site)
	if err != nil {
		return fmt.Errorf("site error: %v", err)
	}
	if created {
		report.SitesAdded++
	}
	_, _ = db.Exec(`INSERT INTO project_sites(project_id, site_id) VALUES(?, ?) ON CONFLICT(site_id) DO UPDATE SET project_id=excluded.project_id`, projectID, siteID)
	if _, err := netip.ParsePrefix(row.Pool); err != nil {
		return fmt.Errorf("invalid pool: %s", row.Pool)
	}
	if !poolExists(db, siteID, row.Pool) {
		family := normalizePoolFamily(row.PoolFamily)
		priority := intValue(row.PoolPriority)
		_, err := db.Exec(`INSERT INTO pools(site_id, cidr, family, tier, priority) VALUES(?, ?, ?, ?, ?)`,
			siteID, row.Pool, family, nullStringToAny(row.PoolTier), priority)
		if err != nil {
			return fmt.Errorf("insert pool: %v", err)
		}
		report.PoolsAdded++
	} else {
		family := normalizePoolFamily(row.PoolFamily)
		priority := intValue(row.PoolPriority)
		_, _ = db.Exec(`UPDATE pools SET family=?, tier=?, priority=? WHERE site_id=? AND cidr=?`,
			family, nullStringToAny(row.PoolTier), priority, siteID, row.Pool)
	}
	return nil
}

func applyPlanSegmentRow(db *sql.DB, report *ImportReport, projectID int64, row PlanRow, rowIndex int, source string) error {
	siteID, created, err := getOrCreateSiteID(db, row.Site)
	if err != nil {
		return fmt.Errorf("site error: %v", err)
	}
	if created {
		report.SitesAdded++
	}
	_, _ = db.Exec(`INSERT INTO project_sites(project_id, site_id) VALUES(?, ?) ON CONFLICT(site_id) DO UPDATE SET project_id=excluded.project_id`, projectID, siteID)

	segID, exists, err := findSegmentID(db, siteID, row.VRF, intValue(row.VLAN), row.Name)
	if err != nil {
		return fmt.Errorf("segment lookup error: %v", err)
	}
	hosts := intPtrToNull(row.Hosts)
	prefix := intPtrToNull(row.Prefix)
	prefixV6 := intPtrToNull(row.PrefixV6)
	cidr := strings.TrimSpace(row.CIDR)
	cidrV6 := strings.TrimSpace(row.CIDRV6)

	if !exists {
		res, err := db.Exec(`
			INSERT INTO segments(site_id, vrf, vlan, name, hosts, prefix, prefix_v6, locked, cidr, cidr_v6)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			siteID, row.VRF, intValue(row.VLAN), row.Name,
			nullIntToAny(hosts), nullIntToAny(prefix), nullIntToAny(prefixV6),
			boolToInt(boolValue(row.Locked)), nullStringToAny(cidr), nullStringToAny(cidrV6),
		)
		if err != nil {
			return fmt.Errorf("insert segment failed: %v", err)
		}
		segID, _ = res.LastInsertId()
		report.SegmentsAdded++
	} else {
		_, err := db.Exec(`
			UPDATE segments SET
				hosts=?,
				prefix=?,
				prefix_v6=?,
				cidr=?,
				cidr_v6=?,
				locked=?
			WHERE id=?`,
			nullIntToAny(hosts),
			nullIntToAny(prefix),
			nullIntToAny(prefixV6),
			nullStringToAny(cidr),
			nullStringToAny(cidrV6),
			boolToInt(boolValue(row.Locked)),
			segID,
		)
		if err != nil {
			return fmt.Errorf("update segment failed: %v", err)
		}
	}

	metaProvided := row.DHCP != nil || row.DHCPRange != "" || row.DHCPReservations != "" || row.Gateway != "" || row.GatewayV6 != "" || row.Tags != "" || row.Notes != "" || row.PoolTier != ""
	if metaProvided {
		_, err := db.Exec(`
			INSERT INTO segment_meta(segment_id, dhcp_enabled, dhcp_range, dhcp_reservations, gateway, gateway_v6, notes, tags, pool_tier)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(segment_id) DO UPDATE SET
				dhcp_enabled=excluded.dhcp_enabled,
				dhcp_range=excluded.dhcp_range,
				dhcp_reservations=excluded.dhcp_reservations,
				gateway=excluded.gateway,
				gateway_v6=excluded.gateway_v6,
				notes=excluded.notes,
				tags=excluded.tags,
				pool_tier=excluded.pool_tier`,
			segID,
			boolToInt(boolValue(row.DHCP)),
			nullStringToAny(strings.TrimSpace(row.DHCPRange)),
			nullStringToAny(strings.TrimSpace(row.DHCPReservations)),
			nullStringToAny(strings.TrimSpace(row.Gateway)),
			nullStringToAny(strings.TrimSpace(row.GatewayV6)),
			nullStringToAny(strings.TrimSpace(row.Notes)),
			nullStringToAny(strings.TrimSpace(row.Tags)),
			nullStringToAny(strings.TrimSpace(row.PoolTier)),
		)
		if err != nil {
			return fmt.Errorf("segment meta failed: %v", err)
		}
	} else {
		_, _ = db.Exec(`DELETE FROM segment_meta WHERE segment_id=?`, segID)
	}
	return nil
}

func resolveProjectID(db *sql.DB, name string, activeProjectID int64) (int64, string, bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		if activeProjectID <= 0 {
			return 0, "", false, fmt.Errorf("project is required")
		}
		project := Project{ID: activeProjectID, Name: "Default"}
		if p, ok := projectByID(db, activeProjectID); ok {
			project = p
		}
		return activeProjectID, project.Name, false, nil
	}
	id, created, err := getOrCreateProjectID(db, name)
	return id, name, created, err
}

func expectedPlanUID(rowType, projectName string, row PlanRow) string {
	switch rowType {
	case planRowMeta:
		return stableID(planRowMeta, projectName)
	case planRowRules:
		return stableID(planRowRules, projectName)
	case planRowSite:
		if row.Site == "" {
			return ""
		}
		return stableID(planRowSite, projectName, row.Site)
	case planRowPool:
		if row.Site == "" || row.Pool == "" {
			return ""
		}
		return stableID(planRowPool, projectName, row.Site, row.Pool)
	case planRowSegment:
		if row.Site == "" || row.VRF == "" || row.VLAN == nil || row.Name == "" {
			return ""
		}
		return stableID(planRowSegment, projectName, row.Site, row.VRF, itoa(*row.VLAN), row.Name)
	default:
		return ""
	}
}

func parseOptionalInt(raw string) (*int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	value, err := parseStrictInt(raw)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func parseOptionalFloat(raw string) (*float64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil, err
	}
	if value < 0 {
		return nil, fmt.Errorf("negative float %q", raw)
	}
	return &value, nil
}

func parseOptionalBool(raw string) (*bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	value, ok := parseStrictBool(raw)
	if !ok {
		return nil, fmt.Errorf("invalid boolean %q", raw)
	}
	return &value, nil
}

func parseStrictInt(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("empty integer")
	}
	out := parseInt(raw)
	if out <= 0 && raw != "0" {
		return 0, fmt.Errorf("invalid integer %q", raw)
	}
	return out, nil
}

func parseStrictBool(raw string) (bool, bool) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	switch raw {
	case "1", "true", "yes", "y", "on":
		return true, true
	case "0", "false", "no", "n", "off":
		return false, true
	default:
		return false, false
	}
}

func boolValue(v *bool) bool {
	if v == nil {
		return false
	}
	return *v
}

func intPtrToNull(v *int) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*v), Valid: true}
}

func floatPtrToNull(v *float64) sql.NullFloat64 {
	if v == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *v, Valid: true}
}

type planImportState struct {
	projects map[string]bool
	meta     map[string]bool
	rules    map[string]bool
	csvCols  *planColumns
}

func newPlanImportState() *planImportState {
	return &planImportState{
		projects: map[string]bool{},
		meta:     map[string]bool{},
		rules:    map[string]bool{},
	}
}

func (s *planImportState) setCSVColumns(cols planColumns) {
	s.csvCols = &cols
}

func (s *planImportState) registerProject(name string) {
	if name == "" {
		return
	}
	s.projects[name] = true
}

func (s *planImportState) metaSeen(name string) bool {
	return s.meta[name]
}

func (s *planImportState) rulesSeen(name string) bool {
	return s.rules[name]
}

func (s *planImportState) markMeta(name string) {
	if name != "" {
		s.meta[name] = true
	}
}

func (s *planImportState) markRules(name string) {
	if name != "" {
		s.rules[name] = true
	}
}

func (s *planImportState) finalize(report *ImportReport) {
	for project := range s.projects {
		if !s.meta[project] {
			report.Errors = append(report.Errors, fmt.Sprintf("project %s: meta row missing", project))
		}
		if !s.rules[project] {
			report.Errors = append(report.Errors, fmt.Sprintf("project %s: rules row missing", project))
		}
	}
}

func (s *planImportState) validateSchemaColumns(version string) error {
	if s.csvCols == nil {
		return nil
	}
	missing := missingPlanColumnsForSchema(*s.csvCols, version)
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("schema_version %s requires columns: %s", version, strings.Join(missing, ", "))
}
