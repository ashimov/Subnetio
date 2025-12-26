package main

import (
	"database/sql"
	"errors"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

type PlanChange struct {
	Site      string
	VRF       string
	VLAN      int
	Name      string
	OldCIDR   string
	NewCIDR   string
	OldCIDRV6 string
	NewCIDRV6 string
	Status    string
	StatusV6  string
}

type WhatIfResult struct {
	Segment        Segment
	ProposedCIDR   string
	ProposedCIDRV6 string
	Changes        []PlanChange
	Unallocated    []PlanChange
	Conflicts      []Conflict
	Summary        string
}

func parseWhatIfSegment(c *gin.Context, sites []Site) (Segment, error) {
	siteID, _ := strconv.ParseInt(c.PostForm("whatif_site_id"), 10, 64)
	vrf := strings.TrimSpace(c.PostForm("whatif_vrf"))
	vlan, _ := strconv.Atoi(c.PostForm("whatif_vlan"))
	name := strings.TrimSpace(c.PostForm("whatif_name"))
	hostsStr := strings.TrimSpace(c.PostForm("whatif_hosts"))
	prefixStr := strings.TrimSpace(c.PostForm("whatif_prefix"))
	prefixV6Str := strings.TrimSpace(c.PostForm("whatif_prefix_v6"))

	if siteID <= 0 || vrf == "" || vlan <= 0 || name == "" {
		return Segment{}, errors.New("what-if: site, vrf, vlan, and name are required")
	}
	var siteName string
	for _, s := range sites {
		if s.ID == siteID {
			siteName = s.Name
			break
		}
	}
	if siteName == "" {
		return Segment{}, errors.New("what-if: invalid site")
	}

	var hosts sql.NullInt64
	if hostsStr != "" {
		if v, err := strconv.ParseInt(hostsStr, 10, 64); err == nil && v > 0 {
			hosts = sql.NullInt64{Int64: v, Valid: true}
		}
	}
	var prefix sql.NullInt64
	if prefixStr != "" {
		prefixStr = strings.TrimPrefix(prefixStr, "/")
		if v, err := strconv.ParseInt(prefixStr, 10, 64); err == nil && v >= 1 && v <= 32 {
			prefix = sql.NullInt64{Int64: v, Valid: true}
		}
	}
	var prefixV6 sql.NullInt64
	if prefixV6Str != "" {
		prefixV6Str = strings.TrimPrefix(prefixV6Str, "/")
		if v, err := strconv.ParseInt(prefixV6Str, 10, 64); err == nil && v >= 1 && v <= 128 {
			prefixV6 = sql.NullInt64{Int64: v, Valid: true}
		}
	}
	if !hosts.Valid && !prefix.Valid && !prefixV6.Valid {
		return Segment{}, errors.New("what-if: hosts or prefix required")
	}

	return Segment{
		ID:       0,
		SiteID:   siteID,
		Site:     siteName,
		VRF:      vrf,
		VLAN:     vlan,
		Name:     name,
		Hosts:    hosts,
		Prefix:   prefix,
		PrefixV6: prefixV6,
		Locked:   false,
	}, nil
}

func runWhatIfPlan(existing []Segment, pools []Pool, sites []Site, extra Segment, rules ProjectRules) WhatIfResult {
	planSegments := append([]Segment{}, existing...)
	planSegments = append(planSegments, extra)
	reservedV4, reservedV6, _ := buildReservedIndex(sites)
	planV4, planV6, planConflicts := planAllocations(planSegments, pools, reservedV4, reservedV6, rules)
	plannedSegments := applyPlan(planSegments, planV4, planV6)

	_, conflicts := analyzeAll(plannedSegments, pools, sites, rules)
	conflicts = append(planConflicts, conflicts...)

	result := WhatIfResult{Segment: extra, Conflicts: conflicts}
	if p, ok := planV4[extra.ID]; ok {
		result.ProposedCIDR = p.String()
	}
	if p, ok := planV6[extra.ID]; ok {
		result.ProposedCIDRV6 = p.String()
	}

	for _, s := range existing {
		oldCIDR := cidrString(s.CIDR)
		oldCIDRV6 := cidrString(s.CIDRV6)
		newCIDR := ""
		if p, ok := planV4[s.ID]; ok {
			newCIDR = p.String()
		}
		newCIDRV6 := ""
		if p, ok := planV6[s.ID]; ok {
			newCIDRV6 = p.String()
		}
		if newCIDR == "" && oldCIDR == "" {
			if newCIDRV6 == "" && oldCIDRV6 == "" {
				continue
			}
		}
		if newCIDR == oldCIDR && newCIDRV6 == oldCIDRV6 {
			continue
		}
		change := PlanChange{
			Site:      s.Site,
			VRF:       s.VRF,
			VLAN:      s.VLAN,
			Name:      s.Name,
			OldCIDR:   oldCIDR,
			NewCIDR:   newCIDR,
			OldCIDRV6: oldCIDRV6,
			NewCIDRV6: newCIDRV6,
		}
		if newCIDR == "" {
			change.Status = "unallocated"
			result.Unallocated = append(result.Unallocated, change)
			continue
		}
		if newCIDRV6 == "" && oldCIDRV6 != "" {
			change.StatusV6 = "unallocated"
		}
		change.Status = "moved"
		result.Changes = append(result.Changes, change)
	}

	sort.Slice(result.Changes, func(i, j int) bool {
		if result.Changes[i].Site != result.Changes[j].Site {
			return result.Changes[i].Site < result.Changes[j].Site
		}
		if result.Changes[i].VRF != result.Changes[j].VRF {
			return result.Changes[i].VRF < result.Changes[j].VRF
		}
		return result.Changes[i].VLAN < result.Changes[j].VLAN
	})

	result.Summary = "changes: " + itoa(len(result.Changes)) + ", unallocated: " + itoa(len(result.Unallocated))
	return result
}

func applyPlan(segs []Segment, planV4 map[int64]netip.Prefix, planV6 map[int64]netip.Prefix) []Segment {
	out := make([]Segment, 0, len(segs))
	for _, s := range segs {
		if p, ok := planV4[s.ID]; ok {
			s.CIDR = sql.NullString{String: p.String(), Valid: true}
		} else {
			s.CIDR = sql.NullString{Valid: false}
		}
		if p, ok := planV6[s.ID]; ok {
			s.CIDRV6 = sql.NullString{String: p.String(), Valid: true}
		} else {
			s.CIDRV6 = sql.NullString{Valid: false}
		}
		out = append(out, s)
	}
	return out
}
