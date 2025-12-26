// Copyright (c) 2025 Berik Ashimov

package main

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
)

type DefaultsBundle struct {
	Project DefaultsProject `json:"project" yaml:"project"`
	Sites   []DefaultsSite  `json:"sites" yaml:"sites"`
}

type DefaultsProject struct {
	ID         int64        `json:"id" yaml:"id"`
	Name       string       `json:"name" yaml:"name"`
	DomainName string       `json:"domain_name,omitempty" yaml:"domain_name,omitempty"`
	DHCP       DefaultsDHCP `json:"dhcp" yaml:"dhcp"`
}

type DefaultsSite struct {
	Site    string       `json:"site" yaml:"site"`
	Project string       `json:"project,omitempty" yaml:"project,omitempty"`
	DHCP    DefaultsDHCP `json:"dhcp" yaml:"dhcp"`
}

type DefaultsDHCP struct {
	Search        []string `json:"search,omitempty" yaml:"search,omitempty"`
	LeaseTime     int      `json:"lease_time,omitempty" yaml:"lease_time,omitempty"`
	RenewTime     int      `json:"renew_time,omitempty" yaml:"renew_time,omitempty"`
	RebindTime    int      `json:"rebind_time,omitempty" yaml:"rebind_time,omitempty"`
	BootFile      string   `json:"boot_file,omitempty" yaml:"boot_file,omitempty"`
	NextServer    string   `json:"next_server,omitempty" yaml:"next_server,omitempty"`
	VendorOptions []string `json:"vendor_options,omitempty" yaml:"vendor_options,omitempty"`
}

type DefaultsImportReport struct {
	ProjectUpdated bool
	SitesUpdated   int
	Warnings       []string
	Errors         []string
}

func exportDefaultsCSV(c *gin.Context, db *sql.DB, projectID int64) error {
	bundle, err := buildDefaultsBundle(db, projectID)
	if err != nil {
		return err
	}
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=subnetio_defaults.csv")
	w := csv.NewWriter(c.Writer)
	_ = w.Write([]string{
		"project",
		"site",
		"domain_name",
		"dhcp_search",
		"dhcp_lease_time",
		"dhcp_renew_time",
		"dhcp_rebind_time",
		"dhcp_boot_file",
		"dhcp_next_server",
		"dhcp_vendor_options",
	})
	project := bundle.Project
	_ = w.Write([]string{
		project.Name,
		"",
		project.DomainName,
		strings.Join(project.DHCP.Search, ", "),
		intToString(project.DHCP.LeaseTime),
		intToString(project.DHCP.RenewTime),
		intToString(project.DHCP.RebindTime),
		project.DHCP.BootFile,
		project.DHCP.NextServer,
		strings.Join(project.DHCP.VendorOptions, "\n"),
	})
	for _, site := range bundle.Sites {
		_ = w.Write([]string{
			site.Project,
			site.Site,
			"",
			strings.Join(site.DHCP.Search, ", "),
			intToString(site.DHCP.LeaseTime),
			intToString(site.DHCP.RenewTime),
			intToString(site.DHCP.RebindTime),
			site.DHCP.BootFile,
			site.DHCP.NextServer,
			strings.Join(site.DHCP.VendorOptions, "\n"),
		})
	}
	w.Flush()
	return w.Error()
}

func exportDefaultsYAML(c *gin.Context, db *sql.DB, projectID int64) error {
	bundle, err := buildDefaultsBundle(db, projectID)
	if err != nil {
		return err
	}
	out, err := yaml.Marshal(bundle)
	if err != nil {
		return err
	}
	c.Header("Content-Type", "application/x-yaml; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=subnetio_defaults.yaml")
	c.String(200, string(out))
	return nil
}

func exportDefaultsJSON(c *gin.Context, db *sql.DB, projectID int64) error {
	bundle, err := buildDefaultsBundle(db, projectID)
	if err != nil {
		return err
	}
	out, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return err
	}
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=subnetio_defaults.json")
	c.String(200, string(out))
	return nil
}

func buildDefaultsBundle(db *sql.DB, projectID int64) (DefaultsBundle, error) {
	project := Project{ID: projectID, Name: "Default"}
	if p, ok := projectByID(db, projectID); ok {
		project = p
	}
	meta, err := getProjectMeta(db, projectID)
	if err != nil {
		return DefaultsBundle{}, err
	}
	sites, err := listSites(db, projectID)
	if err != nil {
		return DefaultsBundle{}, err
	}
	out := DefaultsBundle{
		Project: buildDefaultsProject(project, meta),
		Sites:   buildDefaultsSites(sites),
	}
	return out, nil
}

func buildDefaultsProject(project Project, meta ProjectMeta) DefaultsProject {
	return DefaultsProject{
		ID:         project.ID,
		Name:       project.Name,
		DomainName: nullString(meta.DomainName),
		DHCP:       defaultsDHCPFromProjectMeta(meta),
	}
}

func buildDefaultsSites(sites []Site) []DefaultsSite {
	out := make([]DefaultsSite, 0, len(sites))
	for _, site := range sites {
		out = append(out, DefaultsSite{
			Site:    site.Name,
			Project: nullString(site.Project),
			DHCP:    defaultsDHCPFromSite(site),
		})
	}
	return out
}

func defaultsDHCPFromProjectMeta(meta ProjectMeta) DefaultsDHCP {
	return DefaultsDHCP{
		Search:        parseCSV(nullString(meta.DhcpSearch)),
		LeaseTime:     nullInt(meta.DhcpLeaseTime),
		RenewTime:     nullInt(meta.DhcpRenewTime),
		RebindTime:    nullInt(meta.DhcpRebindTime),
		BootFile:      nullString(meta.DhcpBootFile),
		NextServer:    nullString(meta.DhcpNextServer),
		VendorOptions: parseLines(nullString(meta.DhcpVendorOpts)),
	}
}

func defaultsDHCPFromSite(site Site) DefaultsDHCP {
	return DefaultsDHCP{
		Search:        parseCSV(nullString(site.DhcpSearch)),
		LeaseTime:     nullInt(site.DhcpLeaseTime),
		RenewTime:     nullInt(site.DhcpRenewTime),
		RebindTime:    nullInt(site.DhcpRebindTime),
		BootFile:      nullString(site.DhcpBootFile),
		NextServer:    nullString(site.DhcpNextServer),
		VendorOptions: parseLines(nullString(site.DhcpVendorOpts)),
	}
}

func importDefaultsCSV(c *gin.Context, db *sql.DB, activeProjectID int64) *DefaultsImportReport {
	report := &DefaultsImportReport{}
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

	columns := defaultDefaultsColumns()
	rowIndex := 1
	if looksLikeHeader(first) {
		columns = mapDefaultsColumns(first)
	} else {
		processDefaultsRow(db, report, columns, first, rowIndex, activeProjectID)
	}

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
		processDefaultsRow(db, report, columns, row, rowIndex, activeProjectID)
	}

	return report
}

func importDefaultsJSON(c *gin.Context, db *sql.DB, activeProjectID int64) *DefaultsImportReport {
	return importDefaultsBundle(c, db, activeProjectID, "json")
}

func importDefaultsYAML(c *gin.Context, db *sql.DB, activeProjectID int64) *DefaultsImportReport {
	return importDefaultsBundle(c, db, activeProjectID, "yaml")
}

func importDefaultsBundle(c *gin.Context, db *sql.DB, activeProjectID int64, format string) *DefaultsImportReport {
	report := &DefaultsImportReport{}
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

	var bundle DefaultsBundle
	switch format {
	case "json":
		if err := json.Unmarshal(raw, &bundle); err != nil {
			report.Errors = append(report.Errors, "parse json: "+err.Error())
			return report
		}
	case "yaml":
		if err := yaml.Unmarshal(raw, &bundle); err != nil {
			report.Errors = append(report.Errors, "parse yaml: "+err.Error())
			return report
		}
	default:
		report.Errors = append(report.Errors, "unsupported format")
		return report
	}

	applyDefaultsBundle(db, report, bundle, activeProjectID)
	return report
}

type defaultsColumns struct {
	Project        int
	Site           int
	DomainName     int
	DhcpSearch     int
	DhcpLeaseTime  int
	DhcpRenewTime  int
	DhcpRebindTime int
	DhcpBootFile   int
	DhcpNextServer int
	DhcpVendorOpts int
}

func defaultDefaultsColumns() defaultsColumns {
	return defaultsColumns{
		Project:        0,
		Site:           1,
		DomainName:     2,
		DhcpSearch:     3,
		DhcpLeaseTime:  4,
		DhcpRenewTime:  5,
		DhcpRebindTime: 6,
		DhcpBootFile:   7,
		DhcpNextServer: 8,
		DhcpVendorOpts: 9,
	}
}

func mapDefaultsColumns(header []string) defaultsColumns {
	cols := defaultsColumns{
		Project:        -1,
		Site:           -1,
		DomainName:     -1,
		DhcpSearch:     -1,
		DhcpLeaseTime:  -1,
		DhcpRenewTime:  -1,
		DhcpRebindTime: -1,
		DhcpBootFile:   -1,
		DhcpNextServer: -1,
		DhcpVendorOpts: -1,
	}
	for i, raw := range header {
		name := normalizeHeader(raw)
		switch name {
		case "project":
			cols.Project = i
		case "site", "sitename":
			cols.Site = i
		case "domain", "domainname":
			cols.DomainName = i
		case "dhcpsearch", "search":
			cols.DhcpSearch = i
		case "dhcpleasetime", "dhcplease", "leasetime", "lease":
			cols.DhcpLeaseTime = i
		case "dhcprenewtime", "dhcprenew", "renewtime", "renew":
			cols.DhcpRenewTime = i
		case "dhcprebindtime", "dhcprebind", "rebindtime", "rebind":
			cols.DhcpRebindTime = i
		case "dhcpbootfile", "dhcpboot", "bootfile":
			cols.DhcpBootFile = i
		case "dhcpnextserver", "dhcpnext", "nextserver":
			cols.DhcpNextServer = i
		case "dhcpvendoroptions", "dhcpvendor", "vendoroptions":
			cols.DhcpVendorOpts = i
		}
	}
	return cols
}

func processDefaultsRow(db *sql.DB, report *DefaultsImportReport, cols defaultsColumns, row []string, rowIndex int, activeProjectID int64) {
	get := func(idx int) string {
		if idx < 0 || idx >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[idx])
	}

	projectName := get(cols.Project)
	siteName := get(cols.Site)
	domainName := get(cols.DomainName)
	dhcpSearch := get(cols.DhcpSearch)
	dhcpLease := get(cols.DhcpLeaseTime)
	dhcpRenew := get(cols.DhcpRenewTime)
	dhcpRebind := get(cols.DhcpRebindTime)
	dhcpBoot := get(cols.DhcpBootFile)
	dhcpNext := get(cols.DhcpNextServer)
	dhcpVendor := get(cols.DhcpVendorOpts)

	projectID := activeProjectID
	if projectName != "" {
		if id, err := ensureProjectID(db, projectName); err == nil {
			projectID = id
		} else {
			report.Errors = append(report.Errors, fmt.Sprintf("row %d: project error: %v", rowIndex, err))
			return
		}
	}
	if projectID == 0 {
		report.Errors = append(report.Errors, fmt.Sprintf("row %d: project is required", rowIndex))
		return
	}

	if siteName == "" {
		meta := ProjectMeta{
			ProjectID:      projectID,
			DomainName:     parseNullString(domainName),
			DhcpSearch:     parseNullString(dhcpSearch),
			DhcpLeaseTime:  parseNullInt(dhcpLease),
			DhcpRenewTime:  parseNullInt(dhcpRenew),
			DhcpRebindTime: parseNullInt(dhcpRebind),
			DhcpBootFile:   parseNullString(dhcpBoot),
			DhcpNextServer: parseNullString(dhcpNext),
			DhcpVendorOpts: parseNullString(dhcpVendor),
		}
		if err := saveProjectMetaPartial(db, meta); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("row %d: project meta error: %v", rowIndex, err))
			return
		}
		report.ProjectUpdated = true
		return
	}

	siteID, _, err := getOrCreateSiteID(db, siteName)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("row %d: site error: %v", rowIndex, err))
		return
	}
	_, _ = db.Exec(`
		INSERT INTO project_sites(project_id, site_id)
		VALUES(?, ?)
		ON CONFLICT(site_id) DO UPDATE SET project_id=excluded.project_id`,
		projectID, siteID,
	)
	defaults := DefaultsDHCP{
		Search:        parseCSV(dhcpSearch),
		LeaseTime:     atoiDefault(dhcpLease, 0),
		RenewTime:     atoiDefault(dhcpRenew, 0),
		RebindTime:    atoiDefault(dhcpRebind, 0),
		BootFile:      dhcpBoot,
		NextServer:    dhcpNext,
		VendorOptions: parseLines(dhcpVendor),
	}
	if err := saveSiteDefaults(db, siteID, defaults); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("row %d: site meta error: %v", rowIndex, err))
		return
	}
	report.SitesUpdated++
}

func applyDefaultsBundle(db *sql.DB, report *DefaultsImportReport, bundle DefaultsBundle, activeProjectID int64) {
	projectID := activeProjectID
	if bundle.Project.ID > 0 {
		if projectExists(db, bundle.Project.ID) {
			projectID = bundle.Project.ID
		} else {
			report.Warnings = append(report.Warnings, "project id not found, using active project")
		}
	} else if strings.TrimSpace(bundle.Project.Name) != "" {
		if id, err := ensureProjectID(db, bundle.Project.Name); err == nil {
			projectID = id
		} else {
			report.Errors = append(report.Errors, "project error: "+err.Error())
			return
		}
	}
	if projectID == 0 {
		report.Errors = append(report.Errors, "project is required")
		return
	}
	if hasProjectDefaults(bundle.Project) {
		meta := projectMetaFromDefaults(bundle.Project, projectID)
		if err := saveProjectMetaPartial(db, meta); err != nil {
			report.Errors = append(report.Errors, "project meta error: "+err.Error())
			return
		}
		report.ProjectUpdated = true
	}

	for _, site := range bundle.Sites {
		siteName := strings.TrimSpace(site.Site)
		if siteName == "" {
			continue
		}
		siteProjectID := projectID
		if strings.TrimSpace(site.Project) != "" {
			if id, err := ensureProjectID(db, site.Project); err == nil {
				siteProjectID = id
			} else {
				report.Errors = append(report.Errors, "site project error: "+err.Error())
				continue
			}
		}
		siteID, _, err := getOrCreateSiteID(db, siteName)
		if err != nil {
			report.Errors = append(report.Errors, "site error: "+err.Error())
			continue
		}
		_, _ = db.Exec(`
			INSERT INTO project_sites(project_id, site_id)
			VALUES(?, ?)
			ON CONFLICT(site_id) DO UPDATE SET project_id=excluded.project_id`,
			siteProjectID, siteID,
		)
		if err := saveSiteDefaults(db, siteID, site.DHCP); err != nil {
			report.Errors = append(report.Errors, "site meta error: "+err.Error())
			continue
		}
		report.SitesUpdated++
	}
}

func projectMetaFromDefaults(project DefaultsProject, projectID int64) ProjectMeta {
	return ProjectMeta{
		ProjectID:      projectID,
		DomainName:     parseNullString(project.DomainName),
		DhcpSearch:     parseNullString(strings.Join(project.DHCP.Search, ", ")),
		DhcpLeaseTime:  intToNull(project.DHCP.LeaseTime),
		DhcpRenewTime:  intToNull(project.DHCP.RenewTime),
		DhcpRebindTime: intToNull(project.DHCP.RebindTime),
		DhcpBootFile:   parseNullString(project.DHCP.BootFile),
		DhcpNextServer: parseNullString(project.DHCP.NextServer),
		DhcpVendorOpts: parseNullString(strings.Join(project.DHCP.VendorOptions, "\n")),
	}
}

func hasProjectDefaults(project DefaultsProject) bool {
	if strings.TrimSpace(project.DomainName) != "" {
		return true
	}
	if len(project.DHCP.Search) > 0 {
		return true
	}
	if project.DHCP.LeaseTime > 0 || project.DHCP.RenewTime > 0 || project.DHCP.RebindTime > 0 {
		return true
	}
	if strings.TrimSpace(project.DHCP.BootFile) != "" || strings.TrimSpace(project.DHCP.NextServer) != "" {
		return true
	}
	if len(project.DHCP.VendorOptions) > 0 {
		return true
	}
	return false
}

func saveSiteDefaults(db *sql.DB, siteID int64, dhcp DefaultsDHCP) error {
	if siteID <= 0 {
		return nil
	}
	search := strings.TrimSpace(strings.Join(dhcp.Search, ", "))
	vendor := strings.TrimSpace(strings.Join(dhcp.VendorOptions, "\n"))
	_, err := db.Exec(`
		INSERT INTO site_meta(
			site_id, dhcp_search, dhcp_lease_time, dhcp_renew_time, dhcp_rebind_time,
			dhcp_boot_file, dhcp_next_server, dhcp_vendor_options
		)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(site_id) DO UPDATE SET
			dhcp_search=excluded.dhcp_search,
			dhcp_lease_time=excluded.dhcp_lease_time,
			dhcp_renew_time=excluded.dhcp_renew_time,
			dhcp_rebind_time=excluded.dhcp_rebind_time,
			dhcp_boot_file=excluded.dhcp_boot_file,
			dhcp_next_server=excluded.dhcp_next_server,
			dhcp_vendor_options=excluded.dhcp_vendor_options`,
		siteID,
		nullStringToAny(search),
		intToAny(dhcp.LeaseTime),
		intToAny(dhcp.RenewTime),
		intToAny(dhcp.RebindTime),
		nullStringToAny(strings.TrimSpace(dhcp.BootFile)),
		nullStringToAny(strings.TrimSpace(dhcp.NextServer)),
		nullStringToAny(vendor),
	)
	return err
}

func saveProjectMetaPartial(db *sql.DB, meta ProjectMeta) error {
	if meta.ProjectID <= 0 {
		return nil
	}
	_, err := db.Exec(`
		INSERT INTO project_meta(
			project_id, domain_name, dns, ntp, gateway_policy,
			dhcp_search, dhcp_lease_time, dhcp_renew_time, dhcp_rebind_time,
			dhcp_boot_file, dhcp_next_server, dhcp_vendor_options,
			growth_rate, growth_months
		)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id) DO UPDATE SET
			domain_name=COALESCE(excluded.domain_name, project_meta.domain_name),
			dns=COALESCE(excluded.dns, project_meta.dns),
			ntp=COALESCE(excluded.ntp, project_meta.ntp),
			gateway_policy=COALESCE(excluded.gateway_policy, project_meta.gateway_policy),
			dhcp_search=COALESCE(excluded.dhcp_search, project_meta.dhcp_search),
			dhcp_lease_time=COALESCE(excluded.dhcp_lease_time, project_meta.dhcp_lease_time),
			dhcp_renew_time=COALESCE(excluded.dhcp_renew_time, project_meta.dhcp_renew_time),
			dhcp_rebind_time=COALESCE(excluded.dhcp_rebind_time, project_meta.dhcp_rebind_time),
			dhcp_boot_file=COALESCE(excluded.dhcp_boot_file, project_meta.dhcp_boot_file),
			dhcp_next_server=COALESCE(excluded.dhcp_next_server, project_meta.dhcp_next_server),
			dhcp_vendor_options=COALESCE(excluded.dhcp_vendor_options, project_meta.dhcp_vendor_options),
			growth_rate=COALESCE(excluded.growth_rate, project_meta.growth_rate),
			growth_months=COALESCE(excluded.growth_months, project_meta.growth_months)`,
		meta.ProjectID,
		nullStringToAny(strings.TrimSpace(meta.DomainName.String)),
		nullStringToAny(strings.TrimSpace(meta.DNS.String)),
		nullStringToAny(strings.TrimSpace(meta.NTP.String)),
		nullStringToAny(strings.TrimSpace(meta.GatewayPolicy.String)),
		nullStringToAny(strings.TrimSpace(meta.DhcpSearch.String)),
		nullIntToAny(meta.DhcpLeaseTime),
		nullIntToAny(meta.DhcpRenewTime),
		nullIntToAny(meta.DhcpRebindTime),
		nullStringToAny(strings.TrimSpace(meta.DhcpBootFile.String)),
		nullStringToAny(strings.TrimSpace(meta.DhcpNextServer.String)),
		nullStringToAny(strings.TrimSpace(meta.DhcpVendorOpts.String)),
		nullFloatToAny(meta.GrowthRate),
		nullIntToAny(meta.GrowthMonths),
	)
	return err
}

func intToAny(v int) any {
	if v <= 0 {
		return nil
	}
	return v
}

func intToNull(v int) sql.NullInt64 {
	if v <= 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(v), Valid: true}
}

func intToString(v int) string {
	if v <= 0 {
		return ""
	}
	return itoa(v)
}

func nullInt(v sql.NullInt64) int {
	if v.Valid {
		return int(v.Int64)
	}
	return 0
}

func ensureProjectID(db *sql.DB, name string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, fmt.Errorf("project name required")
	}
	_, err := db.Exec(`INSERT OR IGNORE INTO projects(name) VALUES(?)`, name)
	if err != nil {
		return 0, err
	}
	var id int64
	if err := db.QueryRow(`SELECT id FROM projects WHERE name=?`, name).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}
