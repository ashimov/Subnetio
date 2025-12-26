// Copyright (c) 2025 Berik Ashimov

package main

import (
	"database/sql"
	"errors"
	"strings"
)

type ProjectRules struct {
	VLANScope            string
	RequireInPool        bool
	AllowReservedOverlap bool
	OversizeThreshold    int
	PoolStrategy         string
	PoolTierFallback     bool
}

const (
	VlanScopeSiteVRF = "site_vrf"
	VlanScopeSite    = "site"
	VlanScopeGlobal  = "global"
)

const (
	PoolStrategySpillover = "spillover"
	PoolStrategyContig    = "contiguous"
	PoolStrategyTiered    = "tiered"
)

func defaultProjectRules() ProjectRules {
	return ProjectRules{
		VLANScope:            VlanScopeSiteVRF,
		RequireInPool:        true,
		AllowReservedOverlap: false,
		OversizeThreshold:    50,
		PoolStrategy:         PoolStrategySpillover,
		PoolTierFallback:     true,
	}
}

func presetRules(name string) (ProjectRules, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "strict":
		return ProjectRules{
			VLANScope:            VlanScopeSite,
			RequireInPool:        true,
			AllowReservedOverlap: false,
			OversizeThreshold:    50,
			PoolStrategy:         PoolStrategySpillover,
			PoolTierFallback:     true,
		}, true
	case "balanced":
		return ProjectRules{
			VLANScope:            VlanScopeSiteVRF,
			RequireInPool:        true,
			AllowReservedOverlap: false,
			OversizeThreshold:    50,
			PoolStrategy:         PoolStrategySpillover,
			PoolTierFallback:     true,
		}, true
	case "legacy":
		return ProjectRules{
			VLANScope:            VlanScopeSiteVRF,
			RequireInPool:        false,
			AllowReservedOverlap: true,
			OversizeThreshold:    70,
			PoolStrategy:         PoolStrategySpillover,
			PoolTierFallback:     true,
		}, true
	default:
		return ProjectRules{}, false
	}
}

func getProjectRules(db *sql.DB, projectID int64) (ProjectRules, error) {
	if projectID <= 0 {
		return defaultProjectRules(), nil
	}
	var rules ProjectRules
	var requireInPool int
	var allowReserved int
	var oversize int
	var poolTierFallback int
	row := db.QueryRow(`
		SELECT vlan_scope, require_in_pool, allow_reserved_overlap, oversize_threshold,
			COALESCE(pool_strategy, 'spillover'), COALESCE(pool_tier_fallback, 1)
		FROM project_rules WHERE project_id=?`, projectID)
	switch err := row.Scan(&rules.VLANScope, &requireInPool, &allowReserved, &oversize, &rules.PoolStrategy, &poolTierFallback); err {
	case nil:
		rules.RequireInPool = requireInPool != 0
		rules.AllowReservedOverlap = allowReserved != 0
		rules.OversizeThreshold = oversize
		rules.PoolTierFallback = poolTierFallback != 0
		return normalizeRules(rules), nil
	case sql.ErrNoRows:
		def := defaultProjectRules()
		if err := saveProjectRules(db, projectID, def); err != nil {
			return def, err
		}
		return def, nil
	default:
		return ProjectRules{}, err
	}
}

func saveProjectRules(db *sql.DB, projectID int64, rules ProjectRules) error {
	if projectID <= 0 {
		return errors.New("project id required")
	}
	rules = normalizeRules(rules)
	_, err := db.Exec(`
		INSERT INTO project_rules(project_id, vlan_scope, require_in_pool, allow_reserved_overlap, oversize_threshold, pool_strategy, pool_tier_fallback)
		VALUES(?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id) DO UPDATE SET
			vlan_scope=excluded.vlan_scope,
			require_in_pool=excluded.require_in_pool,
			allow_reserved_overlap=excluded.allow_reserved_overlap,
			oversize_threshold=excluded.oversize_threshold,
			pool_strategy=excluded.pool_strategy,
			pool_tier_fallback=excluded.pool_tier_fallback`,
		projectID,
		rules.VLANScope,
		boolToInt(rules.RequireInPool),
		boolToInt(rules.AllowReservedOverlap),
		rules.OversizeThreshold,
		rules.PoolStrategy,
		boolToInt(rules.PoolTierFallback),
	)
	return err
}

func normalizeRules(rules ProjectRules) ProjectRules {
	switch rules.VLANScope {
	case VlanScopeSite, VlanScopeGlobal:
		// keep
	default:
		rules.VLANScope = VlanScopeSiteVRF
	}
	if rules.OversizeThreshold <= 0 {
		rules.OversizeThreshold = 50
	}
	if rules.OversizeThreshold > 95 {
		rules.OversizeThreshold = 95
	}
	switch rules.PoolStrategy {
	case PoolStrategyContig, PoolStrategyTiered:
		// keep
	default:
		rules.PoolStrategy = PoolStrategySpillover
	}
	return rules
}

func vlanKey(s Segment, rules ProjectRules) string {
	switch rules.VLANScope {
	case VlanScopeGlobal:
		return itoa(s.VLAN)
	case VlanScopeSite:
		return s.Site + "|" + itoa(s.VLAN)
	default:
		return s.Site + "|" + s.VRF + "|" + itoa(s.VLAN)
	}
}
