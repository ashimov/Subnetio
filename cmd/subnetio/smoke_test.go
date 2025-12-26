// Copyright (c) 2025 Berik Ashimov

package main

import (
	"database/sql"
	"net/netip"
	"testing"

	_ "modernc.org/sqlite"
)

func TestSmokeAllocate(t *testing.T) {
	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := ensureDefaultProject(db); err != nil {
		t.Fatalf("default project: %v", err)
	}

	res, err := db.Exec(`INSERT INTO projects(name) VALUES('Test Project')`)
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}
	projectID, _ := res.LastInsertId()

	res, err = db.Exec(`INSERT INTO sites(name) VALUES('SAI')`)
	if err != nil {
		t.Fatalf("insert site: %v", err)
	}
	siteID, _ := res.LastInsertId()

	_, _ = db.Exec(`INSERT INTO project_sites(project_id, site_id) VALUES(?, ?)`, projectID, siteID)
	_, _ = db.Exec(`INSERT INTO pools(site_id, cidr) VALUES(?, ?)`, siteID, "10.30.0.0/24")
	_, _ = db.Exec(`INSERT INTO site_meta(site_id, reserved_ranges) VALUES(?, ?)`, siteID, "10.30.0.0/28")

	_, err = db.Exec(`
		INSERT INTO segments(site_id, vrf, vlan, name, prefix, locked, cidr)
		VALUES(?, ?, ?, ?, ?, ?, ?)`,
		siteID, "MGMT", 20, "mgmt", 26, 1, "10.30.0.64/26",
	)
	if err != nil {
		t.Fatalf("insert locked segment: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO segments(site_id, vrf, vlan, name, hosts, locked)
		VALUES(?, ?, ?, ?, ?, ?)`,
		siteID, "PROD", 10, "users", 60, 0,
	)
	if err != nil {
		t.Fatalf("insert segment: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO segments(site_id, vrf, vlan, name, hosts, locked)
		VALUES(?, ?, ?, ?, ?, ?)`,
		siteID, "PROD", 11, "voice", 50, 0,
	)
	if err != nil {
		t.Fatalf("insert segment: %v", err)
	}

	if err := allocateProject(db, projectID); err != nil {
		t.Fatalf("allocate: %v", err)
	}

	segs, err := listSegments(db, projectID)
	if err != nil {
		t.Fatalf("list segments: %v", err)
	}
	if len(segs) < 3 {
		t.Fatalf("expected segments, got %d", len(segs))
	}

	pool := netip.MustParsePrefix("10.30.0.0/24")
	reserved := netip.MustParsePrefix("10.30.0.0/28")
	var allocated []netip.Prefix

	for _, s := range segs {
		if !s.CIDR.Valid {
			t.Fatalf("segment %s missing CIDR", s.Name)
		}
		p, err := netip.ParsePrefix(s.CIDR.String)
		if err != nil {
			t.Fatalf("segment %s bad CIDR: %v", s.Name, err)
		}
		if !prefixWithin(pool, p) {
			t.Fatalf("segment %s out of pool: %s", s.Name, p.String())
		}
		if prefixesOverlap(p, reserved) {
			t.Fatalf("segment %s overlaps reserved: %s", s.Name, p.String())
		}
		for _, prev := range allocated {
			if prefixesOverlap(prev, p) {
				t.Fatalf("overlap: %s vs %s", prev.String(), p.String())
			}
		}
		allocated = append(allocated, p)
	}

	poolsBySite := map[int64][]netip.Prefix{siteID: {pool}}
	reservedBySite := map[int64][]netip.Prefix{siteID: {reserved}}
	statuses, conflicts := analyzeSegments(segs, poolsBySite, map[int64][]netip.Prefix{}, reservedBySite, map[int64][]netip.Prefix{}, defaultProjectRules())
	if len(conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %v", conflicts)
	}
	for _, s := range segs {
		status := statuses[s.ID]
		if status.Level != statusOK {
			t.Fatalf("segment %s status %s", s.Name, status.Level.Label())
		}
	}
}

func TestTemplatesParse(t *testing.T) {
	names := []string{"projects", "sites", "segments", "conflicts", "planning", "generate", "export", "rules"}
	for _, name := range names {
		if _, err := loadTemplate(name); err != nil {
			t.Fatalf("template %s: %v", name, err)
		}
	}
}

func TestRulesStorageAndPolicy(t *testing.T) {
	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	projectID, err := ensureDefaultProject(db)
	if err != nil {
		t.Fatalf("default project: %v", err)
	}
	rules, err := getProjectRules(db, projectID)
	if err != nil {
		t.Fatalf("get rules: %v", err)
	}
	if rules.VLANScope == "" {
		t.Fatalf("expected vlan scope")
	}

	rules.VLANScope = VlanScopeSite
	if err := saveProjectRules(db, projectID, rules); err != nil {
		t.Fatalf("save rules: %v", err)
	}

	res, err := db.Exec(`INSERT INTO sites(name) VALUES('R1')`)
	if err != nil {
		t.Fatalf("insert site: %v", err)
	}
	siteID, _ := res.LastInsertId()
	_, _ = db.Exec(`INSERT INTO project_sites(project_id, site_id) VALUES(?, ?)`, projectID, siteID)
	_, _ = db.Exec(`INSERT INTO pools(site_id, cidr) VALUES(?, ?)`, siteID, "10.20.0.0/24")

	_, _ = db.Exec(`INSERT INTO segments(site_id, vrf, vlan, name, prefix, locked, cidr) VALUES(?, ?, ?, ?, ?, ?, ?)`, siteID, "A", 10, "seg-a", 26, 1, "10.20.0.0/26")
	_, _ = db.Exec(`INSERT INTO segments(site_id, vrf, vlan, name, prefix, locked, cidr) VALUES(?, ?, ?, ?, ?, ?, ?)`, siteID, "B", 10, "seg-b", 26, 1, "10.20.0.64/26")

	sites, _ := listSites(db, projectID)
	segs, _ := listSegments(db, projectID)
	pools, _ := listPools(db, projectID)
	statuses, conflicts := analyzeAll(segs, pools, sites, rules)

	if len(conflicts) == 0 {
		t.Fatalf("expected conflicts for VLAN scope site")
	}
	found := false
	for _, c := range conflicts {
		if c.Kind == "VLAN_DUP" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected VLAN_DUP conflict, got %v", conflicts)
	}
	for _, s := range segs {
		status := statuses[s.ID]
		if status.Level != statusConflict {
			t.Fatalf("segment %s status %s", s.Name, status.Level.Label())
		}
	}
}

func TestReservedOverlapConflict(t *testing.T) {
	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	projectID, err := ensureDefaultProject(db)
	if err != nil {
		t.Fatalf("default project: %v", err)
	}

	res, err := db.Exec(`INSERT INTO sites(name) VALUES('RES')`)
	if err != nil {
		t.Fatalf("insert site: %v", err)
	}
	siteID, _ := res.LastInsertId()
	_, _ = db.Exec(`INSERT INTO project_sites(project_id, site_id) VALUES(?, ?)`, projectID, siteID)
	_, _ = db.Exec(`INSERT INTO pools(site_id, cidr) VALUES(?, ?)`, siteID, "10.60.0.0/24")
	_, _ = db.Exec(`INSERT INTO site_meta(site_id, reserved_ranges) VALUES(?, ?)`, siteID, "10.60.0.0/28")

	_, err = db.Exec(`
		INSERT INTO segments(site_id, vrf, vlan, name, prefix, locked, cidr)
		VALUES(?, ?, ?, ?, ?, ?, ?)`,
		siteID, "PROD", 10, "bad", 28, 1, "10.60.0.0/28",
	)
	if err != nil {
		t.Fatalf("insert segment: %v", err)
	}

	sites, _ := listSites(db, projectID)
	segs, _ := listSegments(db, projectID)
	pools, _ := listPools(db, projectID)
	reservedBySite, _, _ := buildReservedIndex(sites)
	poolsV4, poolsV6 := buildPoolIndex(pools)
	statuses, conflicts := analyzeSegments(segs, poolsV4, poolsV6, reservedBySite, map[int64][]netip.Prefix{}, defaultProjectRules())

	if len(conflicts) == 0 {
		t.Fatalf("expected conflicts")
	}
	found := false
	for _, c := range conflicts {
		if c.Kind == "RESERVED_OVERLAP" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected RESERVED_OVERLAP conflict, got %v", conflicts)
	}
	for _, s := range segs {
		status := statuses[s.ID]
		if status.Level != statusConflict {
			t.Fatalf("segment %s status %s", s.Name, status.Level.Label())
		}
	}
}
