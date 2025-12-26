// Copyright (c) 2025 Berik Ashimov

package main

import (
	"database/sql"
	"strings"
)

type ProjectMeta struct {
	ProjectID      int64
	DomainName     sql.NullString
	DNS            sql.NullString
	NTP            sql.NullString
	GatewayPolicy  sql.NullString
	DhcpSearch     sql.NullString
	DhcpLeaseTime  sql.NullInt64
	DhcpRenewTime  sql.NullInt64
	DhcpRebindTime sql.NullInt64
	DhcpBootFile   sql.NullString
	DhcpNextServer sql.NullString
	DhcpVendorOpts sql.NullString
	GrowthRate     sql.NullFloat64
	GrowthMonths   sql.NullInt64
}

func getProjectMeta(db *sql.DB, projectID int64) (ProjectMeta, error) {
	if projectID <= 0 {
		return ProjectMeta{}, nil
	}
	var meta ProjectMeta
	meta.ProjectID = projectID
	row := db.QueryRow(`
		SELECT domain_name, dns, ntp, gateway_policy,
			dhcp_search, dhcp_lease_time, dhcp_renew_time, dhcp_rebind_time,
			dhcp_boot_file, dhcp_next_server, dhcp_vendor_options,
			growth_rate, growth_months
		FROM project_meta WHERE project_id=?`, projectID)
	switch err := row.Scan(
		&meta.DomainName,
		&meta.DNS,
		&meta.NTP,
		&meta.GatewayPolicy,
		&meta.DhcpSearch,
		&meta.DhcpLeaseTime,
		&meta.DhcpRenewTime,
		&meta.DhcpRebindTime,
		&meta.DhcpBootFile,
		&meta.DhcpNextServer,
		&meta.DhcpVendorOpts,
		&meta.GrowthRate,
		&meta.GrowthMonths,
	); err {
	case nil:
		return meta, nil
	case sql.ErrNoRows:
		return meta, nil
	default:
		return meta, err
	}
}

func saveProjectMeta(db *sql.DB, meta ProjectMeta) error {
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
			domain_name=excluded.domain_name,
			dns=excluded.dns,
			ntp=excluded.ntp,
			gateway_policy=excluded.gateway_policy,
			dhcp_search=excluded.dhcp_search,
			dhcp_lease_time=excluded.dhcp_lease_time,
			dhcp_renew_time=excluded.dhcp_renew_time,
			dhcp_rebind_time=excluded.dhcp_rebind_time,
			dhcp_boot_file=excluded.dhcp_boot_file,
			dhcp_next_server=excluded.dhcp_next_server,
			dhcp_vendor_options=excluded.dhcp_vendor_options,
			growth_rate=excluded.growth_rate,
			growth_months=excluded.growth_months`,
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
