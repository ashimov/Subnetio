package main

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/netip"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	_ "modernc.org/sqlite"
)

//go:embed web/templates/*.gohtml
var tmplFS embed.FS

//go:embed migrations/*.sql
var migFS embed.FS

//go:embed assets/*
var assetFS embed.FS

var tmplCache sync.Map

type Site struct {
	ID             int64
	Name           string
	Project        sql.NullString
	Region         sql.NullString
	DNS            sql.NullString
	NTP            sql.NullString
	GatewayPolicy  sql.NullString
	ReservedRanges sql.NullString
	DhcpSearch     sql.NullString
	DhcpLeaseTime  sql.NullInt64
	DhcpRenewTime  sql.NullInt64
	DhcpRebindTime sql.NullInt64
	DhcpBootFile   sql.NullString
	DhcpNextServer sql.NullString
	DhcpVendorOpts sql.NullString
}

type Project struct {
	ID          int64
	Name        string
	Description sql.NullString
	SiteCount   int
}

type Pool struct {
	ID       int64
	SiteID   int64
	Site     string
	CIDR     string
	Family   string
	Tier     sql.NullString
	Priority int
}

type Segment struct {
	ID               int64
	SiteID           int64
	Site             string
	VRF              string
	VLAN             int
	Name             string
	Hosts            sql.NullInt64
	Prefix           sql.NullInt64
	CIDR             sql.NullString
	PrefixV6         sql.NullInt64
	CIDRV6           sql.NullString
	Locked           bool
	DhcpEnabled      bool
	DhcpRange        sql.NullString
	DhcpReservations sql.NullString
	Gateway          sql.NullString
	GatewayV6        sql.NullString
	Notes            sql.NullString
	Tags             sql.NullString
	PoolTier         sql.NullString
}

func mustEnv(key, def string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	return v
}

func sqliteDSN(raw string) string {
	if strings.Contains(raw, "_pragma=foreign_keys") {
		return raw
	}
	sep := "?"
	if strings.Contains(raw, "?") {
		sep = "&"
	}
	return raw + sep + "_pragma=foreign_keys(1)"
}

func main() {
	dbPath := mustEnv("DB_PATH", "./subnetio.sqlite")
	listen := mustEnv("LISTEN_ADDR", "0.0.0.0:8080")

	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err := migrate(db); err != nil {
		log.Fatal(err)
	}

	defaultProjectID, err := ensureDefaultProject(db)
	if err != nil {
		log.Fatal(err)
	}

	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	assetSub, err := fs.Sub(assetFS, "assets")
	if err != nil {
		log.Fatal(err)
	}
	r.StaticFS("/assets", http.FS(assetSub))

	r.GET("/healthz", func(c *gin.Context) { c.String(200, "ok") })
	r.GET("/", func(c *gin.Context) { c.Redirect(302, "/segments") })

	// Projects
	r.GET("/projects", func(c *gin.Context) {
		data, activeProjectID := baseData(c, db, defaultProjectID)
		meta, _ := getProjectMeta(db, activeProjectID)
		data["Active"] = "projects"
		data["ProjectMeta"] = meta
		render(c, "projects", data)
	})
	r.POST("/projects", func(c *gin.Context) {
		name := strings.TrimSpace(c.PostForm("name"))
		desc := strings.TrimSpace(c.PostForm("description"))
		if name != "" {
			res, err := db.Exec(`INSERT OR IGNORE INTO projects(name, description) VALUES(?, ?)`, name, nullStringToAny(desc))
			if err == nil {
				if rows, _ := res.RowsAffected(); rows > 0 {
					projectID, _ := res.LastInsertId()
					project := Project{
						ID:          projectID,
						Name:        name,
						Description: parseNullString(desc),
					}
					writeAudit(db, c, auditRecord{
						ProjectID:  projectID,
						Action:     "create",
						EntityType: "project",
						EntityID:   sql.NullInt64{Int64: projectID, Valid: true},
						EntityLabel: sql.NullString{String: name, Valid: true},
						After:      snapshotProject(project),
					})
				}
			}
		}
		c.Redirect(302, "/projects")
	})
	r.POST("/projects/meta", func(c *gin.Context) {
		_, activeProjectID := baseData(c, db, defaultProjectID)
		projectID, _ := strconv.ParseInt(c.PostForm("project_id"), 10, 64)
		if projectID == 0 {
			projectID = activeProjectID
		}
		beforeMeta, _ := getProjectMeta(db, projectID)
		project := Project{ID: projectID}
		if p, ok := projectByID(db, projectID); ok {
			project = p
		}
		meta := ProjectMeta{
			ProjectID:      projectID,
			DomainName:     parseNullString(c.PostForm("domain_name")),
			DNS:            parseNullString(c.PostForm("project_dns")),
			NTP:            parseNullString(c.PostForm("project_ntp")),
			GatewayPolicy:  parseNullString(c.PostForm("project_gateway_policy")),
			DhcpSearch:     parseNullString(c.PostForm("dhcp_search")),
			DhcpLeaseTime:  parseNullInt(c.PostForm("dhcp_lease_time")),
			DhcpRenewTime:  parseNullInt(c.PostForm("dhcp_renew_time")),
			DhcpRebindTime: parseNullInt(c.PostForm("dhcp_rebind_time")),
			DhcpBootFile:   parseNullString(c.PostForm("dhcp_boot_file")),
			DhcpNextServer: parseNullString(c.PostForm("dhcp_next_server")),
			DhcpVendorOpts: parseNullString(c.PostForm("dhcp_vendor_options")),
			GrowthRate:     parseNullFloat(c.PostForm("growth_rate")),
			GrowthMonths:   parseNullInt(c.PostForm("growth_months")),
		}
		_ = saveProjectMeta(db, meta)
		afterMeta, _ := getProjectMeta(db, projectID)
		writeAudit(db, c, auditRecord{
			ProjectID:  projectID,
			Action:     "update",
			EntityType: "project_meta",
			EntityID:   sql.NullInt64{Int64: projectID, Valid: true},
			EntityLabel: sql.NullString{String: project.Name, Valid: true},
			Before:     snapshotProjectMeta(beforeMeta),
			After:      snapshotProjectMeta(afterMeta),
		})
		c.Redirect(302, "/projects?project_id="+itoa64(projectID))
	})
	r.POST("/projects/delete", func(c *gin.Context) {
		projectID, _ := strconv.ParseInt(c.PostForm("project_id"), 10, 64)
		if projectID != defaultProjectID {
			if project, ok := projectByID(db, projectID); ok {
				writeAudit(db, c, auditRecord{
					ProjectID:  projectID,
					Action:     "delete",
					EntityType: "project",
					EntityID:   sql.NullInt64{Int64: projectID, Valid: true},
					EntityLabel: sql.NullString{String: project.Name, Valid: true},
					Before:     snapshotProject(project),
				})
			}
		}
		_ = deleteProject(db, projectID, defaultProjectID)
		c.Redirect(302, "/projects")
	})

	// Sites
	r.GET("/sites", func(c *gin.Context) {
		data, activeProjectID := baseData(c, db, defaultProjectID)
		sites, _ := listSites(db, activeProjectID)
		pools, _ := listPools(db, activeProjectID)
		if msg := strings.TrimSpace(c.Query("pool_error")); msg != "" {
			switch msg {
			case "invalid_cidr":
				cidr := strings.TrimSpace(c.Query("pool_cidr"))
				if cidr != "" {
					data["PoolError"] = "Некорректный CIDR пула: " + cidr
				} else {
					data["PoolError"] = "Некорректный CIDR пула."
				}
			default:
				data["PoolError"] = "Не удалось сохранить пул."
			}
		}
		data["Active"] = "sites"
		data["Sites"] = sites
		data["Pools"] = pools
		render(c, "sites", data)
	})
	r.POST("/sites", func(c *gin.Context) {
		name := strings.TrimSpace(c.PostForm("name"))
		projectID, _ := strconv.ParseInt(c.PostForm("project_id"), 10, 64)
		region := strings.TrimSpace(c.PostForm("region"))
		dns := strings.TrimSpace(c.PostForm("dns"))
		ntp := strings.TrimSpace(c.PostForm("ntp"))
		gatewayPolicy := strings.TrimSpace(c.PostForm("gateway_policy"))
		reservedRanges := strings.TrimSpace(c.PostForm("reserved_ranges"))
		dhcpSearch := strings.TrimSpace(c.PostForm("dhcp_search"))
		dhcpLease := parseNullInt(c.PostForm("dhcp_lease_time"))
		dhcpRenew := parseNullInt(c.PostForm("dhcp_renew_time"))
		dhcpRebind := parseNullInt(c.PostForm("dhcp_rebind_time"))
		dhcpBootFile := strings.TrimSpace(c.PostForm("dhcp_boot_file"))
		dhcpNextServer := strings.TrimSpace(c.PostForm("dhcp_next_server"))
		dhcpVendorOpts := strings.TrimSpace(c.PostForm("dhcp_vendor_options"))

		if name != "" {
			var siteID int64
			var existed bool
			if err := db.QueryRow(`SELECT id FROM sites WHERE name=?`, name).Scan(&siteID); err == nil && siteID > 0 {
				existed = true
			}
			var beforeSite *Site
			if existed {
				if s, ok := siteByID(db, siteID); ok {
					beforeSite = &s
				}
			}
			if !existed {
				res, err := db.Exec(`INSERT INTO sites(name) VALUES(?)`, name)
				if err == nil {
					siteID, _ = res.LastInsertId()
				}
			}
			if siteID > 0 {
				if projectID == 0 {
					projectID = defaultProjectID
				}
				_, _ = db.Exec(`
					INSERT INTO project_sites(project_id, site_id)
					VALUES(?, ?)
					ON CONFLICT(site_id) DO UPDATE SET project_id=excluded.project_id`,
					projectID, siteID,
				)
				_, _ = db.Exec(`
					INSERT INTO site_meta(
						site_id, region, dns, ntp, gateway_policy, reserved_ranges,
						dhcp_search, dhcp_lease_time, dhcp_renew_time, dhcp_rebind_time,
						dhcp_boot_file, dhcp_next_server, dhcp_vendor_options
					)
					VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
					ON CONFLICT(site_id) DO UPDATE SET
						region=excluded.region,
						dns=excluded.dns,
						ntp=excluded.ntp,
						gateway_policy=excluded.gateway_policy,
						reserved_ranges=excluded.reserved_ranges,
						dhcp_search=excluded.dhcp_search,
						dhcp_lease_time=excluded.dhcp_lease_time,
						dhcp_renew_time=excluded.dhcp_renew_time,
						dhcp_rebind_time=excluded.dhcp_rebind_time,
						dhcp_boot_file=excluded.dhcp_boot_file,
						dhcp_next_server=excluded.dhcp_next_server,
						dhcp_vendor_options=excluded.dhcp_vendor_options`,
					siteID,
					nullStringToAny(region),
					nullStringToAny(dns),
					nullStringToAny(ntp),
					nullStringToAny(gatewayPolicy),
					nullStringToAny(reservedRanges),
					nullStringToAny(dhcpSearch),
					nullIntToAny(dhcpLease),
					nullIntToAny(dhcpRenew),
					nullIntToAny(dhcpRebind),
					nullStringToAny(dhcpBootFile),
					nullStringToAny(dhcpNextServer),
					nullStringToAny(dhcpVendorOpts),
				)
				if s, ok := siteByID(db, siteID); ok {
					action := "update"
					if !existed {
						action = "create"
					}
					var before any
					if beforeSite != nil {
						before = snapshotSite(*beforeSite)
					}
					writeAudit(db, c, auditRecord{
						ProjectID:  projectID,
						Action:     action,
						EntityType: "site",
						EntityID:   sql.NullInt64{Int64: siteID, Valid: true},
						EntityLabel: sql.NullString{String: s.Name, Valid: true},
						Before:     before,
						After:      snapshotSite(s),
					})
				}
			}
		}
		c.Redirect(302, "/sites")
	})
	r.POST("/pools", func(c *gin.Context) {
		siteID, _ := strconv.ParseInt(c.PostForm("site_id"), 10, 64)
		cidr := strings.TrimSpace(c.PostForm("cidr"))
		tier := strings.TrimSpace(c.PostForm("tier"))
		priority := atoiDefault(c.PostForm("priority"), 0)
		if siteID > 0 && cidr != "" {
			prefix, err := netip.ParsePrefix(cidr)
			if err != nil {
				projectID := projectIDBySite(db, siteID)
				values := url.Values{}
				if projectID > 0 {
					values.Set("project_id", itoa64(projectID))
				}
				values.Set("pool_error", "invalid_cidr")
				values.Set("pool_cidr", cidr)
				c.Redirect(302, "/sites?"+values.Encode())
				return
			}
			family := "ipv4"
			if prefix.Addr().Is6() {
				family = "ipv6"
			}
			cidr = prefix.String()
			res, err := db.Exec(`INSERT INTO pools(site_id, cidr, family, tier, priority) VALUES(?, ?, ?, ?, ?)`,
				siteID, cidr, family, nullStringToAny(tier), priority)
			if err == nil {
				poolID, _ := res.LastInsertId()
				if pool, ok := poolByID(db, poolID); ok {
					projectID := projectIDBySite(db, siteID)
					writeAudit(db, c, auditRecord{
						ProjectID:  projectID,
						Action:     "create",
						EntityType: "pool",
						EntityID:   sql.NullInt64{Int64: poolID, Valid: true},
						EntityLabel: sql.NullString{String: pool.CIDR, Valid: true},
						After:      snapshotPool(pool),
					})
				}
			}
		}
		c.Redirect(302, "/sites")
	})
	r.POST("/pools/update", func(c *gin.Context) {
		poolID, _ := strconv.ParseInt(c.PostForm("pool_id"), 10, 64)
		cidr := strings.TrimSpace(c.PostForm("cidr"))
		tier := strings.TrimSpace(c.PostForm("tier"))
		priority := atoiDefault(c.PostForm("priority"), 0)
		projectID := parseProjectID(c.PostForm("project_id"))
		if projectID == 0 && poolID > 0 {
			if pool, ok := poolByID(db, poolID); ok {
				projectID = projectIDBySite(db, pool.SiteID)
			}
		}
		if poolID > 0 && cidr != "" {
			prefix, err := netip.ParsePrefix(cidr)
			if err != nil {
				values := url.Values{}
				if projectID > 0 {
					values.Set("project_id", itoa64(projectID))
				}
				values.Set("pool_error", "invalid_cidr")
				values.Set("pool_cidr", cidr)
				c.Redirect(302, "/sites?"+values.Encode())
				return
			}
			family := "ipv4"
			if prefix.Addr().Is6() {
				family = "ipv6"
			}
			cidr = prefix.String()
			var before *Pool
			if p, ok := poolByID(db, poolID); ok {
				before = &p
			}
			_, _ = db.Exec(`UPDATE pools SET cidr=?, family=?, tier=?, priority=? WHERE id=?`,
				cidr, family, nullStringToAny(tier), priority, poolID)
			if after, ok := poolByID(db, poolID); ok {
				var beforeSnap any
				if before != nil {
					beforeSnap = snapshotPool(*before)
				}
				writeAudit(db, c, auditRecord{
					ProjectID:  projectIDBySite(db, after.SiteID),
					Action:     "update",
					EntityType: "pool",
					EntityID:   sql.NullInt64{Int64: poolID, Valid: true},
					EntityLabel: sql.NullString{String: after.CIDR, Valid: true},
					Before:     beforeSnap,
					After:      snapshotPool(after),
				})
			}
		}
		if projectID > 0 {
			c.Redirect(302, "/sites?project_id="+itoa64(projectID))
			return
		}
		c.Redirect(302, "/sites")
	})
	r.POST("/pools/delete", func(c *gin.Context) {
		poolID, _ := strconv.ParseInt(c.PostForm("pool_id"), 10, 64)
		projectID := parseProjectID(c.PostForm("project_id"))
		if pool, ok := poolByID(db, poolID); ok {
			if projectID == 0 {
				projectID = projectIDBySite(db, pool.SiteID)
			}
			writeAudit(db, c, auditRecord{
				ProjectID:  projectID,
				Action:     "delete",
				EntityType: "pool",
				EntityID:   sql.NullInt64{Int64: poolID, Valid: true},
				EntityLabel: sql.NullString{String: pool.CIDR, Valid: true},
				Before:     snapshotPool(pool),
			})
		}
		if poolID > 0 {
			_, _ = db.Exec(`DELETE FROM pools WHERE id=?`, poolID)
		}
		if projectID > 0 {
			c.Redirect(302, "/sites?project_id="+itoa64(projectID))
			return
		}
		c.Redirect(302, "/sites")
	})
	r.POST("/sites/delete", func(c *gin.Context) {
		siteID, _ := strconv.ParseInt(c.PostForm("site_id"), 10, 64)
		projectID := parseProjectID(c.PostForm("project_id"))
		if site, ok := siteByID(db, siteID); ok {
			if projectID == 0 {
				projectID = projectIDBySite(db, siteID)
			}
			writeAudit(db, c, auditRecord{
				ProjectID:  projectID,
				Action:     "delete",
				EntityType: "site",
				EntityID:   sql.NullInt64{Int64: siteID, Valid: true},
				EntityLabel: sql.NullString{String: site.Name, Valid: true},
				Before:     snapshotSite(site),
			})
		}
		_ = deleteSite(db, siteID)
		if projectID > 0 {
			c.Redirect(302, "/sites?project_id="+itoa64(projectID))
			return
		}
		c.Redirect(302, "/sites")
	})

	// Segments
	r.GET("/segments", func(c *gin.Context) {
		data, activeProjectID := baseData(c, db, defaultProjectID)
		sites, _ := listSites(db, activeProjectID)
		segs, _ := listSegments(db, activeProjectID)
		pools, _ := listPools(db, activeProjectID)
		rules, _ := getProjectRules(db, activeProjectID)
		statuses, conflicts := analyzeAll(segs, pools, sites, rules)
		views := buildSegmentViews(segs, statuses, pools)
		filters := parseSegmentFilters(c)
		filtered := applySegmentFilters(views, filters)
		presets, _ := listFilterPresets(db, activeProjectID, "segments")

		if msg := strings.TrimSpace(c.Query("filter_ok")); msg != "" {
			switch msg {
			case "saved":
				data["SegmentFilterOk"] = "Фильтр сохранен."
			case "deleted":
				data["SegmentFilterOk"] = "Сохраненный фильтр удален."
			}
		}
		if msg := strings.TrimSpace(c.Query("filter_error")); msg != "" {
			switch msg {
			case "name":
				data["SegmentFilterError"] = "Укажите название для сохраненного фильтра."
			case "empty":
				data["SegmentFilterError"] = "Нет активных фильтров для сохранения."
			case "invalid":
				data["SegmentFilterError"] = "Некорректные параметры фильтра."
			case "save":
				data["SegmentFilterError"] = "Не удалось сохранить фильтр."
			case "delete":
				data["SegmentFilterError"] = "Не удалось удалить фильтр."
			}
		}

		data["Active"] = "segments"
		data["Sites"] = sites
		data["Segments"] = filtered
		data["SegmentsTotal"] = len(views)
		data["SegmentsShown"] = len(filtered)
		data["SegmentFilters"] = filters
		data["SegmentFiltersQuery"] = segmentFiltersQuery(filters)
		data["SegmentFiltersActive"] = filtersActive(filters)
		data["SegmentPresets"] = presets
		data["Conflicts"] = conflicts
		data["Rules"] = rules
		render(c, "segments", data)
	})

	r.POST("/segments", func(c *gin.Context) {
		siteID, _ := strconv.ParseInt(c.PostForm("site_id"), 10, 64)
		vrf := strings.TrimSpace(c.PostForm("vrf"))
		vlan, _ := strconv.Atoi(c.PostForm("vlan"))
		name := strings.TrimSpace(c.PostForm("name"))
		hostsStr := strings.TrimSpace(c.PostForm("hosts"))
		prefixStr := strings.TrimSpace(c.PostForm("prefix"))
		prefixV6Str := strings.TrimSpace(c.PostForm("prefix_v6"))
		locked := c.PostForm("locked") == "on"
		dhcpEnabled := c.PostForm("dhcp_enabled") == "on"
		dhcpRange := strings.TrimSpace(c.PostForm("dhcp_range"))
		dhcpReservations := strings.TrimSpace(c.PostForm("dhcp_reservations"))
		gateway := strings.TrimSpace(c.PostForm("gateway"))
		gatewayV6 := strings.TrimSpace(c.PostForm("gateway_v6"))
		notes := strings.TrimSpace(c.PostForm("notes"))
		tags := strings.TrimSpace(c.PostForm("tags"))
		poolTier := strings.TrimSpace(c.PostForm("pool_tier"))

		var hosts sql.NullInt64
		if hostsStr != "" {
			if v, err := strconv.ParseInt(hostsStr, 10, 64); err == nil && v > 0 {
				hosts = sql.NullInt64{Int64: v, Valid: true}
			}
		}
		var prefix sql.NullInt64
		if prefixStr != "" {
			if v, err := strconv.ParseInt(prefixStr, 10, 64); err == nil && v >= 1 && v <= 32 {
				prefix = sql.NullInt64{Int64: v, Valid: true}
			}
		}
		var prefixV6 sql.NullInt64
		if prefixV6Str != "" {
			if v, err := strconv.ParseInt(prefixV6Str, 10, 64); err == nil && v >= 1 && v <= 128 {
				prefixV6 = sql.NullInt64{Int64: v, Valid: true}
			}
		}

		if siteID > 0 && vrf != "" && vlan > 0 && name != "" {
			res, _ := db.Exec(`
				INSERT INTO segments(site_id, vrf, vlan, name, hosts, prefix, prefix_v6, locked)
				VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
				siteID, vrf, vlan, name,
				nullIntToAny(hosts), nullIntToAny(prefix), nullIntToAny(prefixV6),
				boolToInt(locked),
			)
			segID, _ := res.LastInsertId()
			if segID > 0 {
				_, _ = db.Exec(`
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
					boolToInt(dhcpEnabled),
					nullStringToAny(dhcpRange),
					nullStringToAny(dhcpReservations),
					nullStringToAny(gateway),
					nullStringToAny(gatewayV6),
					nullStringToAny(notes),
					nullStringToAny(tags),
					nullStringToAny(poolTier),
				)
				if seg, ok := segmentByID(db, segID); ok {
					projectID := projectIDBySite(db, siteID)
					writeAudit(db, c, auditRecord{
						ProjectID:  projectID,
						Action:     "create",
						EntityType: "segment",
						EntityID:   sql.NullInt64{Int64: segID, Valid: true},
						EntityLabel: sql.NullString{String: seg.Name, Valid: true},
						After:      snapshotSegment(seg),
					})
				}
			}
		}
		c.Redirect(302, "/segments")
	})
	r.POST("/segments/update", func(c *gin.Context) {
		segmentID, _ := strconv.ParseInt(c.PostForm("segment_id"), 10, 64)
		vrf := strings.TrimSpace(c.PostForm("vrf"))
		vlan, _ := strconv.Atoi(c.PostForm("vlan"))
		name := strings.TrimSpace(c.PostForm("name"))
		hostsStr := strings.TrimSpace(c.PostForm("hosts"))
		prefixStr := strings.TrimSpace(c.PostForm("prefix"))
		prefixV6Str := strings.TrimSpace(c.PostForm("prefix_v6"))
		locked := c.PostForm("locked") == "on"
		dhcpEnabled := c.PostForm("dhcp_enabled") == "on"
		dhcpRange := strings.TrimSpace(c.PostForm("dhcp_range"))
		dhcpReservations := strings.TrimSpace(c.PostForm("dhcp_reservations"))
		gateway := strings.TrimSpace(c.PostForm("gateway"))
		gatewayV6 := strings.TrimSpace(c.PostForm("gateway_v6"))
		notes := strings.TrimSpace(c.PostForm("notes"))
		tags := strings.TrimSpace(c.PostForm("tags"))
		poolTier := strings.TrimSpace(c.PostForm("pool_tier"))
		projectID := parseProjectID(c.PostForm("project_id"))
		returnTo := normalizeSegmentFilterQuery(c.PostForm("return_to"))

		var hosts sql.NullInt64
		if hostsStr != "" {
			if v, err := strconv.ParseInt(hostsStr, 10, 64); err == nil && v > 0 {
				hosts = sql.NullInt64{Int64: v, Valid: true}
			}
		}
		var prefix sql.NullInt64
		if prefixStr != "" {
			if v, err := strconv.ParseInt(prefixStr, 10, 64); err == nil && v >= 1 && v <= 32 {
				prefix = sql.NullInt64{Int64: v, Valid: true}
			}
		}
		var prefixV6 sql.NullInt64
		if prefixV6Str != "" {
			if v, err := strconv.ParseInt(prefixV6Str, 10, 64); err == nil && v >= 1 && v <= 128 {
				prefixV6 = sql.NullInt64{Int64: v, Valid: true}
			}
		}

		if segmentID > 0 && vrf != "" && vlan > 0 && name != "" {
			var before *Segment
			if seg, ok := segmentByID(db, segmentID); ok {
				before = &seg
			}
			_, _ = db.Exec(`
				UPDATE segments SET
					vrf=?,
					vlan=?,
					name=?,
					hosts=?,
					prefix=?,
					prefix_v6=?,
					locked=?
				WHERE id=?`,
				vrf,
				vlan,
				name,
				nullIntToAny(hosts),
				nullIntToAny(prefix),
				nullIntToAny(prefixV6),
				boolToInt(locked),
				segmentID,
			)

			metaProvided := dhcpEnabled || dhcpRange != "" || dhcpReservations != "" || gateway != "" || gatewayV6 != "" || tags != "" || notes != "" || poolTier != ""
			if metaProvided {
				_, _ = db.Exec(`
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
					segmentID,
					boolToInt(dhcpEnabled),
					nullStringToAny(dhcpRange),
					nullStringToAny(dhcpReservations),
					nullStringToAny(gateway),
					nullStringToAny(gatewayV6),
					nullStringToAny(notes),
					nullStringToAny(tags),
					nullStringToAny(poolTier),
				)
			} else {
				_, _ = db.Exec(`DELETE FROM segment_meta WHERE segment_id=?`, segmentID)
			}

			if after, ok := segmentByID(db, segmentID); ok {
				var beforeSnap any
				if before != nil {
					beforeSnap = snapshotSegment(*before)
					if projectID == 0 {
						projectID = projectIDBySite(db, before.SiteID)
					}
				}
				if projectID == 0 {
					projectID = projectIDBySite(db, after.SiteID)
				}
				writeAudit(db, c, auditRecord{
					ProjectID:  projectIDBySite(db, after.SiteID),
					Action:     "update",
					EntityType: "segment",
					EntityID:   sql.NullInt64{Int64: segmentID, Valid: true},
					EntityLabel: sql.NullString{String: after.Name, Valid: true},
					Before:     beforeSnap,
					After:      snapshotSegment(after),
				})
			}
		}
		if projectID > 0 {
			redirect := "/segments?project_id=" + itoa64(projectID)
			if returnTo != "" {
				redirect += "&" + returnTo
			}
			c.Redirect(302, redirect)
			return
		}
		if returnTo != "" {
			c.Redirect(302, "/segments?"+returnTo)
			return
		}
		c.Redirect(302, "/segments")
	})
	r.POST("/segments/delete", func(c *gin.Context) {
		segmentID, _ := strconv.ParseInt(c.PostForm("segment_id"), 10, 64)
		projectID := parseProjectID(c.PostForm("project_id"))
		returnTo := normalizeSegmentFilterQuery(c.PostForm("return_to"))
		if seg, ok := segmentByID(db, segmentID); ok {
			if projectID == 0 {
				projectID = projectIDBySite(db, seg.SiteID)
			}
			writeAudit(db, c, auditRecord{
				ProjectID:  projectID,
				Action:     "delete",
				EntityType: "segment",
				EntityID:   sql.NullInt64{Int64: segmentID, Valid: true},
				EntityLabel: sql.NullString{String: seg.Name, Valid: true},
				Before:     snapshotSegment(seg),
			})
		}
		_ = deleteSegment(db, segmentID)
		if projectID > 0 {
			redirect := "/segments?project_id=" + itoa64(projectID)
			if returnTo != "" {
				redirect += "&" + returnTo
			}
			c.Redirect(302, redirect)
			return
		}
		if returnTo != "" {
			c.Redirect(302, "/segments?"+returnTo)
			return
		}
		c.Redirect(302, "/segments")
	})

	r.POST("/filters/save", func(c *gin.Context) {
		projectID := parseProjectID(c.PostForm("project_id"))
		if projectID == 0 {
			_, projectID = baseData(c, db, defaultProjectID)
		}
		page := strings.TrimSpace(c.PostForm("page"))
		if page == "" {
			page = "segments"
		}
		name := strings.TrimSpace(c.PostForm("name"))
		normalizedQuery := normalizeSegmentFilterQuery(c.PostForm("query"))
		if page != "segments" {
			c.Redirect(302, segmentsRedirectURL(projectID, "", "filter_error", "invalid"))
			return
		}
		if name == "" {
			c.Redirect(302, segmentsRedirectURL(projectID, normalizedQuery, "filter_error", "name"))
			return
		}
		if normalizedQuery == "" {
			c.Redirect(302, segmentsRedirectURL(projectID, "", "filter_error", "empty"))
			return
		}
		if err := saveFilterPreset(db, projectID, page, name, normalizedQuery); err != nil {
			c.Redirect(302, segmentsRedirectURL(projectID, normalizedQuery, "filter_error", "save"))
			return
		}
		c.Redirect(302, segmentsRedirectURL(projectID, normalizedQuery, "filter_ok", "saved"))
	})

	r.POST("/filters/delete", func(c *gin.Context) {
		projectID := parseProjectID(c.PostForm("project_id"))
		if projectID == 0 {
			_, projectID = baseData(c, db, defaultProjectID)
		}
		page := strings.TrimSpace(c.PostForm("page"))
		if page == "" {
			page = "segments"
		}
		presetID, _ := strconv.ParseInt(c.PostForm("preset_id"), 10, 64)
		returnTo := normalizeSegmentFilterQuery(c.PostForm("return_to"))
		if page != "segments" || presetID <= 0 {
			c.Redirect(302, segmentsRedirectURL(projectID, returnTo, "filter_error", "invalid"))
			return
		}
		if err := deleteFilterPreset(db, projectID, presetID, page); err != nil {
			c.Redirect(302, segmentsRedirectURL(projectID, returnTo, "filter_error", "delete"))
			return
		}
		c.Redirect(302, segmentsRedirectURL(projectID, returnTo, "filter_ok", "deleted"))
	})

	// Allocate (VLSM IPv4)
	r.POST("/allocate", func(c *gin.Context) {
		_, activeProjectID := baseData(c, db, defaultProjectID)
		beforeSegs, _ := listSegments(db, activeProjectID)
		if err := allocateProject(db, activeProjectID); err != nil {
			c.String(500, fmt.Sprintf("allocate error: %v", err))
			return
		}
		afterSegs, _ := listSegments(db, activeProjectID)
		project := Project{ID: activeProjectID}
		if p, ok := projectByID(db, activeProjectID); ok {
			project = p
		}
		summary := buildAllocationSummary(beforeSegs, afterSegs)
		writeAudit(db, c, auditRecord{
			ProjectID:  activeProjectID,
			Action:     "allocate",
			EntityType: "allocation",
			EntityID:   sql.NullInt64{Int64: activeProjectID, Valid: true},
			EntityLabel: sql.NullString{String: project.Name, Valid: true},
			After:      summary,
		})
		c.Redirect(302, "/segments?project_id="+itoa64(activeProjectID))
	})

	// Conflicts & Rules
	r.GET("/conflicts", func(c *gin.Context) {
		data, activeProjectID := baseData(c, db, defaultProjectID)
		sites, _ := listSites(db, activeProjectID)
		segs, _ := listSegments(db, activeProjectID)
		pools, _ := listPools(db, activeProjectID)
		rules, _ := getProjectRules(db, activeProjectID)
		_, conflicts := analyzeAll(segs, pools, sites, rules)
		data["Active"] = "conflicts"
		data["Conflicts"] = conflicts
		data["Rules"] = rules
		render(c, "conflicts", data)
	})

	// Planning
	r.GET("/planning", func(c *gin.Context) {
		data, activeProjectID := baseData(c, db, defaultProjectID)
		sites, _ := listSites(db, activeProjectID)
		segs, _ := listSegments(db, activeProjectID)
		pools, _ := listPools(db, activeProjectID)
		meta, _ := getProjectMeta(db, activeProjectID)
		growthDefault := 5.0
		if meta.GrowthRate.Valid {
			growthDefault = meta.GrowthRate.Float64
		}
		monthsDefault := 12
		if meta.GrowthMonths.Valid {
			monthsDefault = int(meta.GrowthMonths.Int64)
		}
		growthRate := parseQueryFloat(c.Query("growth_rate"), growthDefault)
		months := parseQueryInt(c.Query("months"), monthsDefault)
		v6Unit := parseQueryInt(c.Query("v6_unit"), 64)
		report := buildCapacityReport(segs, pools, sites, growthRate, months, v6Unit)
		data["Active"] = "planning"
		data["Capacity"] = report
		data["Meta"] = meta
		render(c, "planning", data)
	})

	// Generate (templates)
	r.GET("/generate", func(c *gin.Context) {
		data, activeProjectID := baseData(c, db, defaultProjectID)
		sites, _ := listSites(db, activeProjectID)
		segs, _ := listSegments(db, activeProjectID)
		pools, _ := listPools(db, activeProjectID)
		rules, _ := getProjectRules(db, activeProjectID)
		statuses, _ := analyzeAll(segs, pools, sites, rules)
		views := buildSegmentViews(segs, statuses, pools)
		opts := parseGenerateOptions(c)
		project := Project{ID: activeProjectID}
		if p, ok := projectByID(db, activeProjectID); ok {
			project = p
		}
		meta, _ := getProjectMeta(db, activeProjectID)
		templateInfo := TemplateInfo{}
		preview := ""
		diff := ""
		scopeKey := buildScopeKey(opts)
		legacyScopeKey := buildScopeKeyLegacy(opts)
		deployed := DeployedConfig{}
		deployedDiff := ""
		if opts.Template != "" {
			if result, err := generateConfig(opts, views, sites, project, meta); err == nil {
				preview = result.Output
				templateInfo = TemplateInfo{
					Name:    result.Metadata.Template,
					Version: result.Metadata.TemplateVersion,
					Source:  result.Metadata.TemplateSource,
				}
				if opts.ShowDiff {
					baseOpts := opts
					baseOpts.SiteFilter = ""
					baseOpts.VRFFilter = ""
					baseOpts.SegmentFilter = ""
					baseOpts.ShowDiff = false
					if baseResult, err := generateConfig(baseOpts, views, sites, project, meta); err == nil {
						diff = unifiedDiff(baseResult.Output, preview)
					}
				}
				if cfg, ok, _ := getDeployedConfig(db, activeProjectID, opts.Template, scopeKey); ok {
					deployed = cfg
					deployedDiff = unifiedDiff(deployed.Content, preview)
				} else if legacyScopeKey != scopeKey {
					if cfg, ok, _ := getDeployedConfig(db, activeProjectID, opts.Template, legacyScopeKey); ok {
						_ = saveDeployedConfig(db, activeProjectID, opts.Template, scopeKey, cfg.Content)
						_ = deleteDeployedConfig(db, activeProjectID, opts.Template, legacyScopeKey)
						if migrated, ok, _ := getDeployedConfig(db, activeProjectID, opts.Template, scopeKey); ok {
							deployed = migrated
						} else {
							deployed = cfg
							deployed.ScopeKey = scopeKey
						}
						deployedDiff = unifiedDiff(deployed.Content, preview)
					}
				}
			} else {
				preview = "error: " + err.Error()
			}
		}
		data["Active"] = "generate"
		data["TemplateInfo"] = templateInfo
		data["Preview"] = preview
		data["Diff"] = diff
		data["Deployed"] = deployed
		data["DeployedDiff"] = deployedDiff
		data["ScopeKey"] = scopeKey
		data["Gen"] = opts
		data["QueryString"] = opts.QueryString(activeProjectID)
		data["Sites"] = sites
		data["Meta"] = meta
		data["Example"] = templateExample(opts.Template)
		render(c, "generate", data)
	})
	r.POST("/generate/deployed/save", func(c *gin.Context) {
		projectID := parseProjectID(c.PostForm("project_id"))
		template := strings.TrimSpace(c.PostForm("template"))
		scopeKey := strings.TrimSpace(c.PostForm("scope_key"))
		content := c.PostForm("content")
		if scopeKey == "" {
			scopeKey = "project"
		}
		if template != "" {
			_ = saveDeployedConfig(db, projectID, template, scopeKey, content)
		}
		query := strings.TrimPrefix(c.PostForm("query_string"), "?")
		if query != "" {
			c.Redirect(302, "/generate?"+query)
			return
		}
		c.Redirect(302, "/generate?project_id="+itoa64(projectID))
	})
	r.POST("/generate/deployed/delete", func(c *gin.Context) {
		projectID := parseProjectID(c.PostForm("project_id"))
		template := strings.TrimSpace(c.PostForm("template"))
		scopeKey := strings.TrimSpace(c.PostForm("scope_key"))
		if scopeKey == "" {
			scopeKey = "project"
		}
		if template != "" {
			_ = deleteDeployedConfig(db, projectID, template, scopeKey)
		}
		query := strings.TrimPrefix(c.PostForm("query_string"), "?")
		if query != "" {
			c.Redirect(302, "/generate?"+query)
			return
		}
		c.Redirect(302, "/generate?project_id="+itoa64(projectID))
	})
	r.GET("/generate/download", func(c *gin.Context) {
		_, activeProjectID := baseData(c, db, defaultProjectID)
		sites, _ := listSites(db, activeProjectID)
		segs, _ := listSegments(db, activeProjectID)
		pools, _ := listPools(db, activeProjectID)
		rules, _ := getProjectRules(db, activeProjectID)
		statuses, _ := analyzeAll(segs, pools, sites, rules)
		views := buildSegmentViews(segs, statuses, pools)
		opts := parseGenerateOptions(c)
		if opts.Template == "" {
			c.String(400, "template is required")
			return
		}
		project := Project{ID: activeProjectID}
		if p, ok := projectByID(db, activeProjectID); ok {
			project = p
		}
		meta, _ := getProjectMeta(db, activeProjectID)
		result, err := generateConfig(opts, views, sites, project, meta)
		if err != nil {
			c.String(500, err.Error())
			return
		}
		ext := templateExtension(opts.Template)
		filename := "subnetio_" + opts.Template + "." + ext
		contentType := "text/plain; charset=utf-8"
		c.Header("Content-Type", contentType)
		c.Header("Content-Disposition", "attachment; filename="+filename)
		c.String(200, result.Output)
	})
	r.GET("/generate/bundle", func(c *gin.Context) {
		_, activeProjectID := baseData(c, db, defaultProjectID)
		sites, _ := listSites(db, activeProjectID)
		segs, _ := listSegments(db, activeProjectID)
		pools, _ := listPools(db, activeProjectID)
		rules, _ := getProjectRules(db, activeProjectID)
		statuses, _ := analyzeAll(segs, pools, sites, rules)
		views := buildSegmentViews(segs, statuses, pools)
		opts := parseGenerateOptions(c)
		if opts.Template == "" {
			c.String(400, "template is required")
			return
		}
		project := Project{ID: activeProjectID}
		if p, ok := projectByID(db, activeProjectID); ok {
			project = p
		}
		meta, _ := getProjectMeta(db, activeProjectID)
		result, err := generateConfig(opts, views, sites, project, meta)
		if err != nil {
			c.String(500, err.Error())
			return
		}
		result.Metadata.Checksum = checksumSHA256(result.Output)
		metaBytes, err := encodeMetadataJSON(result.Metadata)
		if err != nil {
			c.String(500, err.Error())
			return
		}
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		configName := "subnetio_" + opts.Template + "." + templateExtension(opts.Template)
		configFile, err := zw.Create(configName)
		if err != nil {
			c.String(500, err.Error())
			return
		}
		if _, err := configFile.Write([]byte(result.Output)); err != nil {
			c.String(500, err.Error())
			return
		}
		metaFile, err := zw.Create("metadata.json")
		if err != nil {
			c.String(500, err.Error())
			return
		}
		if _, err := metaFile.Write(metaBytes); err != nil {
			c.String(500, err.Error())
			return
		}
		if err := zw.Close(); err != nil {
			c.String(500, err.Error())
			return
		}
		filename := "subnetio_bundle_" + opts.Template + ".zip"
		c.Header("Content-Type", "application/zip")
		c.Header("Content-Disposition", "attachment; filename="+filename)
		c.Data(200, "application/zip", buf.Bytes())
	})

	// Templates
	r.GET("/templates", func(c *gin.Context) {
		data, activeProjectID := baseData(c, db, defaultProjectID)
		data["Active"] = "templates"
		if msg := strings.TrimSpace(c.Query("upload_error")); msg != "" {
			data["TemplateUploadError"] = msg
		}
		if msg := strings.TrimSpace(c.Query("upload_ok")); msg != "" {
			data["TemplateUploadSuccess"] = msg
		}

		templateCatalog := listTemplateCatalog()
		data["TemplateCatalog"] = templateCatalog
		data["TemplateSelectedInfo"] = TemplateInfo{}
		data["TemplateHelpers"] = []string{
			"itoa - int to string",
			"safeName - normalize names",
			"groupLabel - site/VRF label",
			"join - strings.Join",
			"trim - strings.TrimSpace",
			"quoteList - quote list items",
			"ciscoLease - seconds to lease",
			"ciscoDomainSearch - option 119 format",
			"firstVLAN - first VLAN in group",
			"mikrotikDhcpLine - DHCP line",
		}

		sites, _ := listSites(db, activeProjectID)
		segs, _ := listSegments(db, activeProjectID)
		pools, _ := listPools(db, activeProjectID)
		rules, _ := getProjectRules(db, activeProjectID)
		statuses, _ := analyzeAll(segs, pools, sites, rules)
		views := buildSegmentViews(segs, statuses, pools)
		data["TemplateSegments"] = views

		selectedSegmentID := parseProjectID(c.Query("segment_id"))
		if selectedSegmentID == 0 && len(views) > 0 {
			selectedSegmentID = views[0].ID
		}
		data["SelectedSegmentID"] = selectedSegmentID

		selectedTemplate := strings.TrimSpace(c.Query("template"))
		if selectedTemplate == "" && len(templateCatalog) > 0 {
			selectedTemplate = templateCatalog[0].Name
		}
		if selectedTemplate != "" {
			normalized, err := normalizeTemplateName(selectedTemplate)
			if err != nil {
				data["TemplateError"] = err.Error()
				selectedTemplate = ""
			} else {
				selectedTemplate = normalized
			}
		}
		data["TemplateSelected"] = selectedTemplate
		if selectedTemplate != "" {
			data["TemplateExample"] = templateExample(selectedTemplate)
		}

		var version string
		var source string
		if selectedTemplate != "" {
			if info, err := loadTemplateSource(selectedTemplate); err == nil {
				version = info.Version
				source = info.Source
				data["TemplateSelectedInfo"] = TemplateInfo{
					Name:    selectedTemplate,
					Version: version,
					Source:  source,
				}
			} else {
				data["TemplateError"] = err.Error()
			}
		}

		project := Project{ID: activeProjectID}
		if p, ok := projectByID(db, activeProjectID); ok {
			project = p
		}
		meta, _ := getProjectMeta(db, activeProjectID)
		opts := GenerateOptions{
			Template:    selectedTemplate,
			IncludeVRF:  true,
			IncludeVLAN: true,
			IncludeDHCP: true,
		}
		if selectedSegmentID > 0 {
			opts.SegmentFilter = itoa64(selectedSegmentID)
		}
		domain := resolveDomain(opts, meta)
		defaults := projectDHCPDefaults(meta, domain)
		siteDefaults := buildSiteDefaults(sites, meta)
		dhcpBySite := buildDHCPBySite(sites, defaults, domain)
		renderSegments := buildRenderSegments(opts, views, sites, domain, dhcpBySite, siteDefaults)
		metadata := buildMetadata(opts, project, domain, renderSegments, defaults, version, source)
		prefix := "#"
		if selectedTemplate != "" {
			prefix = templateCommentPrefix(selectedTemplate)
		}
		header := metadataHeader(metadata, prefix)
		ctx := TemplateContext{
			Meta:     metadata,
			Header:   header,
			Options:  opts,
			Groups:   groupSegments(renderSegments),
			Segments: renderSegments,
			Defaults: defaults,
		}
		if len(renderSegments) > 0 {
			if raw, err := json.MarshalIndent(ctx, "", "  "); err == nil {
				data["TemplatePreview"] = string(raw)
			} else {
				data["TemplateError"] = err.Error()
			}
		}
		if c.Query("render") != "" && selectedTemplate != "" {
			result, err := generateConfig(opts, views, sites, project, meta)
			if err != nil {
				data["TemplateRenderError"] = err.Error()
			} else {
				data["TemplateOutput"] = result.Output
			}
		}
		render(c, "templates", data)
	})
	r.POST("/templates/upload", func(c *gin.Context) {
		_, activeProjectID := baseData(c, db, defaultProjectID)
		rawName := strings.TrimSpace(c.PostForm("template_name"))
		fileHeader, _ := c.FormFile("template_file")
		contentField := strings.TrimSpace(c.PostForm("template_content"))
		if rawName == "" && fileHeader != nil {
			rawName = strings.TrimSuffix(fileHeader.Filename, filepath.Ext(fileHeader.Filename))
		}
		name, err := normalizeTemplateName(rawName)
		if err != nil {
			redirectTemplateMessage(c, activeProjectID, rawName, "upload_error", "invalid template name")
			return
		}

		var content []byte
		if fileHeader != nil {
			file, err := fileHeader.Open()
			if err != nil {
				redirectTemplateMessage(c, activeProjectID, name, "upload_error", "failed to read template file")
				return
			}
			defer file.Close()
			data, err := io.ReadAll(file)
			if err != nil {
				redirectTemplateMessage(c, activeProjectID, name, "upload_error", "failed to read template file")
				return
			}
			content = data
		} else if contentField != "" {
			content = []byte(contentField)
		}
		if len(content) == 0 {
			redirectTemplateMessage(c, activeProjectID, name, "upload_error", "template content is empty")
			return
		}
		if len(content) > 1<<20 {
			redirectTemplateMessage(c, activeProjectID, name, "upload_error", "template is too large (max 1MB)")
			return
		}
		if _, err := template.New(name).Funcs(templateFuncs()).Parse(string(content)); err != nil {
			redirectTemplateMessage(c, activeProjectID, name, "upload_error", "template parse error: "+err.Error())
			return
		}

		if err := os.MkdirAll(customTemplateDir, 0o755); err != nil {
			redirectTemplateMessage(c, activeProjectID, name, "upload_error", "failed to create templates dir")
			return
		}
		path := customTemplatePath(name)
		var before []byte
		if existing, err := os.ReadFile(path); err == nil {
			before = existing
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			redirectTemplateMessage(c, activeProjectID, name, "upload_error", "failed to write template")
			return
		}
		action := "create"
		if len(before) > 0 {
			action = "update"
		}
		writeAudit(db, c, auditRecord{
			ProjectID:  activeProjectID,
			Action:     action,
			EntityType: "template",
			EntityLabel: sql.NullString{String: name, Valid: true},
			Before:     templateSnapshotIfAny(name, "override", before),
			After:      snapshotTemplate(name, "override", content),
		})
		redirectTemplateMessage(c, activeProjectID, name, "upload_ok", "template saved")
	})
	r.POST("/templates/delete", func(c *gin.Context) {
		_, activeProjectID := baseData(c, db, defaultProjectID)
		rawName := strings.TrimSpace(c.PostForm("template_name"))
		name, err := normalizeTemplateName(rawName)
		if err != nil {
			redirectTemplateMessage(c, activeProjectID, rawName, "upload_error", "invalid template name")
			return
		}
		path := customTemplatePath(name)
		before, err := os.ReadFile(path)
		if err != nil {
			redirectTemplateMessage(c, activeProjectID, name, "upload_error", "override not found")
			return
		}
		if err := os.Remove(path); err != nil {
			redirectTemplateMessage(c, activeProjectID, name, "upload_error", "failed to delete template")
			return
		}
		writeAudit(db, c, auditRecord{
			ProjectID:  activeProjectID,
			Action:     "delete",
			EntityType: "template",
			EntityLabel: sql.NullString{String: name, Valid: true},
			Before:     snapshotTemplate(name, "override", before),
		})
		redirectTemplateMessage(c, activeProjectID, name, "upload_ok", "template deleted")
	})

	// Export
	r.GET("/export", func(c *gin.Context) {
		data, _ := baseData(c, db, defaultProjectID)
		data["Active"] = "export"
		render(c, "export", data)
	})
	r.GET("/export/csv", func(c *gin.Context) {
		_, activeProjectID := baseData(c, db, defaultProjectID)
		if err := exportCSV(c, db, activeProjectID); err != nil {
			c.String(500, err.Error())
		}
	})
	r.GET("/export/xlsx", func(c *gin.Context) {
		_, activeProjectID := baseData(c, db, defaultProjectID)
		if err := exportXLSX(c, db, activeProjectID); err != nil {
			c.String(500, err.Error())
		}
	})
	r.GET("/export/yaml", func(c *gin.Context) {
		_, activeProjectID := baseData(c, db, defaultProjectID)
		if err := exportYAML(c, db, activeProjectID); err != nil {
			c.String(500, err.Error())
		}
	})
	r.GET("/export/json", func(c *gin.Context) {
		_, activeProjectID := baseData(c, db, defaultProjectID)
		if err := exportJSON(c, db, activeProjectID); err != nil {
			c.String(500, err.Error())
		}
	})
	r.GET("/export/defaults/csv", func(c *gin.Context) {
		_, activeProjectID := baseData(c, db, defaultProjectID)
		if err := exportDefaultsCSV(c, db, activeProjectID); err != nil {
			c.String(500, err.Error())
		}
	})
	r.GET("/export/defaults/yaml", func(c *gin.Context) {
		_, activeProjectID := baseData(c, db, defaultProjectID)
		if err := exportDefaultsYAML(c, db, activeProjectID); err != nil {
			c.String(500, err.Error())
		}
	})
	r.GET("/export/defaults/json", func(c *gin.Context) {
		_, activeProjectID := baseData(c, db, defaultProjectID)
		if err := exportDefaultsJSON(c, db, activeProjectID); err != nil {
			c.String(500, err.Error())
		}
	})
	r.GET("/export/audit/csv", func(c *gin.Context) {
		_, activeProjectID := baseData(c, db, defaultProjectID)
		if err := exportAuditCSV(c, db, activeProjectID); err != nil {
			c.String(500, err.Error())
		}
	})
	r.GET("/export/audit/json", func(c *gin.Context) {
		_, activeProjectID := baseData(c, db, defaultProjectID)
		if err := exportAuditJSON(c, db, activeProjectID); err != nil {
			c.String(500, err.Error())
		}
	})

	// Import
	r.POST("/import/csv", func(c *gin.Context) {
		data, activeProjectID := baseData(c, db, defaultProjectID)
		meta, _ := getProjectMeta(db, activeProjectID)
		report := importCSVPlan(c, db, activeProjectID)
		project := Project{ID: activeProjectID}
		if p, ok := projectByID(db, activeProjectID); ok {
			project = p
		}
		writeAudit(db, c, auditRecord{
			ProjectID:  activeProjectID,
			Action:     "import",
			EntityType: "plan",
			EntityID:   sql.NullInt64{Int64: activeProjectID, Valid: true},
			EntityLabel: sql.NullString{String: project.Name, Valid: true},
			After: auditImportSummary{
				Source:        "csv",
				ProjectsAdded: report.ProjectsAdded,
				SitesAdded:    report.SitesAdded,
				PoolsAdded:    report.PoolsAdded,
				SegmentsAdded: report.SegmentsAdded,
				Warnings:      report.Warnings,
				Errors:        report.Errors,
			},
		})
		data["Active"] = "projects"
		data["ProjectMeta"] = meta
		data["ImportReport"] = report
		render(c, "projects", data)
	})
	r.POST("/import/yaml", func(c *gin.Context) {
		data, activeProjectID := baseData(c, db, defaultProjectID)
		meta, _ := getProjectMeta(db, activeProjectID)
		report := importPlanYAML(c, db, activeProjectID)
		project := Project{ID: activeProjectID}
		if p, ok := projectByID(db, activeProjectID); ok {
			project = p
		}
		writeAudit(db, c, auditRecord{
			ProjectID:  activeProjectID,
			Action:     "import",
			EntityType: "plan",
			EntityID:   sql.NullInt64{Int64: activeProjectID, Valid: true},
			EntityLabel: sql.NullString{String: project.Name, Valid: true},
			After: auditImportSummary{
				Source:        "yaml",
				ProjectsAdded: report.ProjectsAdded,
				SitesAdded:    report.SitesAdded,
				PoolsAdded:    report.PoolsAdded,
				SegmentsAdded: report.SegmentsAdded,
				Warnings:      report.Warnings,
				Errors:        report.Errors,
			},
		})
		data["Active"] = "projects"
		data["ProjectMeta"] = meta
		data["ImportReport"] = report
		render(c, "projects", data)
	})
	r.POST("/import/json", func(c *gin.Context) {
		data, activeProjectID := baseData(c, db, defaultProjectID)
		meta, _ := getProjectMeta(db, activeProjectID)
		report := importPlanJSON(c, db, activeProjectID)
		project := Project{ID: activeProjectID}
		if p, ok := projectByID(db, activeProjectID); ok {
			project = p
		}
		writeAudit(db, c, auditRecord{
			ProjectID:  activeProjectID,
			Action:     "import",
			EntityType: "plan",
			EntityID:   sql.NullInt64{Int64: activeProjectID, Valid: true},
			EntityLabel: sql.NullString{String: project.Name, Valid: true},
			After: auditImportSummary{
				Source:        "json",
				ProjectsAdded: report.ProjectsAdded,
				SitesAdded:    report.SitesAdded,
				PoolsAdded:    report.PoolsAdded,
				SegmentsAdded: report.SegmentsAdded,
				Warnings:      report.Warnings,
				Errors:        report.Errors,
			},
		})
		data["Active"] = "projects"
		data["ProjectMeta"] = meta
		data["ImportReport"] = report
		render(c, "projects", data)
	})
	r.POST("/import/defaults/csv", func(c *gin.Context) {
		data, activeProjectID := baseData(c, db, defaultProjectID)
		report := importDefaultsCSV(c, db, activeProjectID)
		project := Project{ID: activeProjectID}
		if p, ok := projectByID(db, activeProjectID); ok {
			project = p
		}
		writeAudit(db, c, auditRecord{
			ProjectID:  activeProjectID,
			Action:     "import",
			EntityType: "defaults",
			EntityID:   sql.NullInt64{Int64: activeProjectID, Valid: true},
			EntityLabel: sql.NullString{String: project.Name, Valid: true},
			After: auditDefaultsImportSummary{
				Source:         "csv",
				ProjectUpdated: report.ProjectUpdated,
				SitesUpdated:   report.SitesUpdated,
				Warnings:       report.Warnings,
				Errors:         report.Errors,
			},
		})
		meta, _ := getProjectMeta(db, activeProjectID)
		data["Active"] = "projects"
		data["ProjectMeta"] = meta
		data["DefaultsImportReport"] = report
		render(c, "projects", data)
	})
	r.POST("/import/defaults/yaml", func(c *gin.Context) {
		data, activeProjectID := baseData(c, db, defaultProjectID)
		report := importDefaultsYAML(c, db, activeProjectID)
		project := Project{ID: activeProjectID}
		if p, ok := projectByID(db, activeProjectID); ok {
			project = p
		}
		writeAudit(db, c, auditRecord{
			ProjectID:  activeProjectID,
			Action:     "import",
			EntityType: "defaults",
			EntityID:   sql.NullInt64{Int64: activeProjectID, Valid: true},
			EntityLabel: sql.NullString{String: project.Name, Valid: true},
			After: auditDefaultsImportSummary{
				Source:         "yaml",
				ProjectUpdated: report.ProjectUpdated,
				SitesUpdated:   report.SitesUpdated,
				Warnings:       report.Warnings,
				Errors:         report.Errors,
			},
		})
		meta, _ := getProjectMeta(db, activeProjectID)
		data["Active"] = "projects"
		data["ProjectMeta"] = meta
		data["DefaultsImportReport"] = report
		render(c, "projects", data)
	})
	r.POST("/import/defaults/json", func(c *gin.Context) {
		data, activeProjectID := baseData(c, db, defaultProjectID)
		report := importDefaultsJSON(c, db, activeProjectID)
		project := Project{ID: activeProjectID}
		if p, ok := projectByID(db, activeProjectID); ok {
			project = p
		}
		writeAudit(db, c, auditRecord{
			ProjectID:  activeProjectID,
			Action:     "import",
			EntityType: "defaults",
			EntityID:   sql.NullInt64{Int64: activeProjectID, Valid: true},
			EntityLabel: sql.NullString{String: project.Name, Valid: true},
			After: auditDefaultsImportSummary{
				Source:         "json",
				ProjectUpdated: report.ProjectUpdated,
				SitesUpdated:   report.SitesUpdated,
				Warnings:       report.Warnings,
				Errors:         report.Errors,
			},
		})
		meta, _ := getProjectMeta(db, activeProjectID)
		data["Active"] = "projects"
		data["ProjectMeta"] = meta
		data["DefaultsImportReport"] = report
		render(c, "projects", data)
	})

	// Rules
	r.GET("/rules", func(c *gin.Context) {
		data, activeProjectID := baseData(c, db, defaultProjectID)
		rules, _ := getProjectRules(db, activeProjectID)
		meta, _ := getProjectMeta(db, activeProjectID)
		data["Active"] = "rules"
		data["Rules"] = rules
		data["Meta"] = meta
		render(c, "rules", data)
	})
	r.POST("/rules", func(c *gin.Context) {
		_, activeProjectID := baseData(c, db, defaultProjectID)
		beforeRules, _ := getProjectRules(db, activeProjectID)
		preset := strings.TrimSpace(c.PostForm("preset"))
		rules, ok := presetRules(preset)
		if !ok {
			rules = ProjectRules{
				VLANScope:            strings.TrimSpace(c.PostForm("vlan_scope")),
				RequireInPool:        c.PostForm("require_in_pool") == "on",
				AllowReservedOverlap: c.PostForm("allow_reserved_overlap") == "on",
				OversizeThreshold:    atoiDefault(c.PostForm("oversize_threshold"), 50),
				PoolStrategy:         strings.TrimSpace(c.PostForm("pool_strategy")),
				PoolTierFallback:     c.PostForm("pool_tier_fallback") == "on",
			}
		}
		_ = saveProjectRules(db, activeProjectID, rules)
		afterRules, _ := getProjectRules(db, activeProjectID)
		project := Project{ID: activeProjectID}
		if p, ok := projectByID(db, activeProjectID); ok {
			project = p
		}
		writeAudit(db, c, auditRecord{
			ProjectID:  activeProjectID,
			Action:     "update",
			EntityType: "rules",
			EntityID:   sql.NullInt64{Int64: activeProjectID, Valid: true},
			EntityLabel: sql.NullString{String: project.Name, Valid: true},
			Before:     snapshotRules(beforeRules),
			After:      snapshotRules(afterRules),
		})
		c.Redirect(302, "/rules?project_id="+itoa64(activeProjectID))
	})
	r.POST("/rules/delete", func(c *gin.Context) {
		projectID := parseProjectID(c.PostForm("project_id"))
		if projectID == 0 {
			_, projectID = baseData(c, db, defaultProjectID)
		}
		beforeRules, _ := getProjectRules(db, projectID)
		_ = deleteProjectRules(db, projectID)
		project := Project{ID: projectID}
		if p, ok := projectByID(db, projectID); ok {
			project = p
		}
		writeAudit(db, c, auditRecord{
			ProjectID:  projectID,
			Action:     "reset",
			EntityType: "rules",
			EntityID:   sql.NullInt64{Int64: projectID, Valid: true},
			EntityLabel: sql.NullString{String: project.Name, Valid: true},
			Before:     snapshotRules(beforeRules),
			After:      snapshotRules(defaultProjectRules()),
		})
		c.Redirect(302, "/rules?project_id="+itoa64(projectID))
	})

	// What-if allocation
	r.POST("/whatif", func(c *gin.Context) {
		data, activeProjectID := baseData(c, db, defaultProjectID)
		sites, _ := listSites(db, activeProjectID)
		segs, _ := listSegments(db, activeProjectID)
		pools, _ := listPools(db, activeProjectID)
		rules, _ := getProjectRules(db, activeProjectID)

		whatIfSeg, err := parseWhatIfSegment(c, sites)
		if err != nil {
			filters := parseSegmentFilters(c)
			views := buildSegmentViews(segs, map[int64]SegmentStatus{}, pools)
			filtered := applySegmentFilters(views, filters)
			presets, _ := listFilterPresets(db, activeProjectID, "segments")

			data["Active"] = "segments"
			data["Sites"] = sites
			data["Segments"] = filtered
			data["SegmentsTotal"] = len(views)
			data["SegmentsShown"] = len(filtered)
			data["SegmentFilters"] = filters
			data["SegmentFiltersQuery"] = segmentFiltersQuery(filters)
			data["SegmentFiltersActive"] = filtersActive(filters)
			data["SegmentPresets"] = presets
			data["Conflicts"] = []Conflict{{Kind: "WHATIF_ERROR", Detail: err.Error(), Level: statusWarning.Label()}}
			render(c, "segments", data)
			return
		}
		planResult := runWhatIfPlan(segs, pools, sites, whatIfSeg, rules)
		statuses, conflicts := analyzeAll(segs, pools, sites, rules)
		filters := parseSegmentFilters(c)
		views := buildSegmentViews(segs, statuses, pools)
		filtered := applySegmentFilters(views, filters)
		presets, _ := listFilterPresets(db, activeProjectID, "segments")

		data["Active"] = "segments"
		data["Sites"] = sites
		data["Segments"] = filtered
		data["SegmentsTotal"] = len(views)
		data["SegmentsShown"] = len(filtered)
		data["SegmentFilters"] = filters
		data["SegmentFiltersQuery"] = segmentFiltersQuery(filters)
		data["SegmentFiltersActive"] = filtersActive(filters)
		data["SegmentPresets"] = presets
		data["Conflicts"] = conflicts
		data["Rules"] = rules
		data["WhatIf"] = planResult
		render(c, "segments", data)
	})

	log.Printf("listening on http://%s", listen)
	if err := r.Run(listen); err != nil {
		log.Fatal(err)
	}
}

func render(c *gin.Context, name string, data any) {
	tmpl, err := loadTemplate(name)
	if err != nil {
		c.String(500, err.Error())
		return
	}
	c.Status(http.StatusOK)
	c.Header("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(c.Writer, "layout", data); err != nil {
		c.String(500, err.Error())
	}
}

func loadTemplate(name string) (*template.Template, error) {
	if cached, ok := tmplCache.Load(name); ok {
		return cached.(*template.Template), nil
	}
	files := []string{
		"web/templates/layout.gohtml",
		"web/templates/" + name + ".gohtml",
	}
	tmpl, err := template.New("").ParseFS(tmplFS, files...)
	if err != nil {
		return nil, err
	}
	tmplCache.Store(name, tmpl)
	return tmpl, nil
}

func customTemplatePath(name string) string {
	return filepath.Join(customTemplateDir, name+".tmpl")
}

func templateSnapshotIfAny(name, source string, content []byte) any {
	if len(content) == 0 {
		return nil
	}
	return snapshotTemplate(name, source, content)
}

func redirectTemplateMessage(c *gin.Context, projectID int64, templateName, key, message string) {
	values := url.Values{}
	if projectID > 0 {
		values.Set("project_id", itoa64(projectID))
	}
	if strings.TrimSpace(templateName) != "" {
		values.Set("template", templateName)
	}
	values.Set(key, message)
	target := "/templates"
	if encoded := values.Encode(); encoded != "" {
		target += "?" + encoded
	}
	c.Redirect(302, target)
}

func ensureDefaultProject(db *sql.DB) (int64, error) {
	_, _ = db.Exec(`INSERT OR IGNORE INTO projects(name) VALUES('Default')`)
	var id int64
	if err := db.QueryRow(`SELECT id FROM projects WHERE name='Default'`).Scan(&id); err != nil {
		return 0, err
	}
	_, _ = db.Exec(`
		INSERT INTO project_sites(project_id, site_id)
		SELECT ?, s.id
		FROM sites s
		LEFT JOIN project_sites ps ON ps.site_id = s.id
		WHERE ps.site_id IS NULL`, id)
	return id, nil
}

func listSites(db *sql.DB, projectID int64) ([]Site, error) {
	query := `
		SELECT s.id, s.name,
			p.name,
			m.region, m.dns, m.ntp, m.gateway_policy, m.reserved_ranges,
			m.dhcp_search, m.dhcp_lease_time, m.dhcp_renew_time, m.dhcp_rebind_time,
			m.dhcp_boot_file, m.dhcp_next_server, m.dhcp_vendor_options
		FROM sites s
		LEFT JOIN project_sites ps ON ps.site_id = s.id
		LEFT JOIN projects p ON p.id = ps.project_id
		LEFT JOIN site_meta m ON m.site_id = s.id
	`
	var args []any
	if projectID > 0 {
		query += " WHERE ps.project_id=?"
		args = append(args, projectID)
	}
	query += " ORDER BY s.name"
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Site
	for rows.Next() {
		var s Site
		if err := rows.Scan(
			&s.ID, &s.Name,
			&s.Project,
			&s.Region, &s.DNS, &s.NTP, &s.GatewayPolicy, &s.ReservedRanges,
			&s.DhcpSearch, &s.DhcpLeaseTime, &s.DhcpRenewTime, &s.DhcpRebindTime,
			&s.DhcpBootFile, &s.DhcpNextServer, &s.DhcpVendorOpts,
		); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func listPools(db *sql.DB, projectID int64) ([]Pool, error) {
	query := `
		SELECT p.id, p.site_id, s.name, p.cidr,
			COALESCE(p.family, 'ipv4'), p.tier, COALESCE(p.priority, 0)
		FROM pools p
		JOIN sites s ON s.id = p.site_id
	`
	var args []any
	if projectID > 0 {
		query += " JOIN project_sites ps ON ps.site_id = s.id WHERE ps.project_id=?"
		args = append(args, projectID)
	}
	query += " ORDER BY s.name, COALESCE(p.priority, 0), COALESCE(p.family, 'ipv4'), p.cidr"
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Pool
	for rows.Next() {
		var p Pool
		if err := rows.Scan(&p.ID, &p.SiteID, &p.Site, &p.CIDR, &p.Family, &p.Tier, &p.Priority); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func listSegments(db *sql.DB, projectID int64) ([]Segment, error) {
	query := `
		SELECT s.id, s.site_id, si.name, s.vrf, s.vlan, s.name, s.hosts, s.prefix, s.cidr,
			s.prefix_v6, s.cidr_v6, s.locked,
			sm.dhcp_enabled, sm.dhcp_range, sm.dhcp_reservations, sm.gateway, sm.gateway_v6,
			sm.notes, sm.tags, sm.pool_tier
		FROM segments s
		JOIN sites si ON si.id = s.site_id
		LEFT JOIN segment_meta sm ON sm.segment_id = s.id
	`
	var args []any
	if projectID > 0 {
		query += " JOIN project_sites ps ON ps.site_id = si.id WHERE ps.project_id=?"
		args = append(args, projectID)
	}
	query += " ORDER BY si.name, s.vrf, s.vlan, s.name"
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Segment
	for rows.Next() {
		var seg Segment
		var lockedInt int
		var dhcpEnabledInt sql.NullInt64
		if err := rows.Scan(
			&seg.ID, &seg.SiteID, &seg.Site, &seg.VRF, &seg.VLAN, &seg.Name,
			&seg.Hosts, &seg.Prefix, &seg.CIDR,
			&seg.PrefixV6, &seg.CIDRV6, &lockedInt,
			&dhcpEnabledInt, &seg.DhcpRange, &seg.DhcpReservations, &seg.Gateway, &seg.GatewayV6,
			&seg.Notes, &seg.Tags, &seg.PoolTier,
		); err != nil {
			return nil, err
		}
		seg.Locked = lockedInt != 0
		seg.DhcpEnabled = dhcpEnabledInt.Valid && dhcpEnabledInt.Int64 != 0
		out = append(out, seg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func listProjects(db *sql.DB) ([]Project, error) {
	rows, err := db.Query(`
		SELECT p.id, p.name, p.description, COUNT(ps.site_id)
		FROM projects p
		LEFT JOIN project_sites ps ON ps.project_id = p.id
		GROUP BY p.id
		ORDER BY p.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.SiteCount); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullIntToAny(v sql.NullInt64) any {
	if v.Valid {
		return v.Int64
	}
	return nil
}

func nullStringToAny(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

func nullFloatToAny(v sql.NullFloat64) any {
	if v.Valid {
		return v.Float64
	}
	return nil
}

func parseNullString(raw string) sql.NullString {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: raw, Valid: true}
}

func parseNullInt(raw string) sql.NullInt64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return sql.NullInt64{}
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v <= 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: v, Valid: true}
}

func parseNullFloat(raw string) sql.NullFloat64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return sql.NullFloat64{}
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v < 0 {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: v, Valid: true}
}

func atoiDefault(raw string, def int) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return v
}
