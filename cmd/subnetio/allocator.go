package main

import (
	"database/sql"
	"errors"
	"math/big"
	"net/netip"
	"sort"
	"strings"
)

type poolItem struct {
	Pool   Pool
	Prefix netip.Prefix
	Tier   string
}

type sqlExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func normalizePoolFamily(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "ipv6" {
		return "ipv6"
	}
	return "ipv4"
}

func poolTierValue(p Pool) string {
	if p.Tier.Valid {
		return strings.ToLower(strings.TrimSpace(p.Tier.String))
	}
	return ""
}

func segmentTierValue(s Segment) string {
	if s.PoolTier.Valid {
		v := strings.TrimSpace(s.PoolTier.String)
		if v != "" {
			return strings.ToLower(v)
		}
	}
	if !s.Tags.Valid {
		return ""
	}
	for _, part := range strings.Split(s.Tags.String, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(part), "tier:") {
			return strings.TrimSpace(strings.TrimPrefix(strings.ToLower(part), "tier:"))
		}
		if strings.HasPrefix(strings.ToLower(part), "tier=") {
			return strings.TrimSpace(strings.TrimPrefix(strings.ToLower(part), "tier="))
		}
	}
	return ""
}

func poolItemsForFamily(pools []Pool, family string) []poolItem {
	items := make([]poolItem, 0, len(pools))
	for _, p := range pools {
		if normalizePoolFamily(p.Family) != family {
			continue
		}
		prefix, err := netip.ParsePrefix(strings.TrimSpace(p.CIDR))
		if err != nil {
			continue
		}
		if family == "ipv4" && !prefix.Addr().Is4() {
			continue
		}
		if family == "ipv6" && !prefix.Addr().Is6() {
			continue
		}
		items = append(items, poolItem{Pool: p, Prefix: prefix, Tier: poolTierValue(p)})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Pool.Priority != items[j].Pool.Priority {
			return items[i].Pool.Priority < items[j].Pool.Priority
		}
		if items[i].Tier != items[j].Tier {
			return items[i].Tier < items[j].Tier
		}
		return items[i].Prefix.String() < items[j].Prefix.String()
	})
	return items
}

func allocateProject(db *sql.DB, projectID int64) error {
	sites, err := listSites(db, projectID)
	if err != nil {
		return err
	}
	rules, _ := getProjectRules(db, projectID)

	for _, site := range sites {
		pools, err := poolsBySite(db, site.ID)
		if err != nil {
			return err
		}
		if len(pools) == 0 {
			continue
		}

		segs, err := segmentsBySite(db, site.ID)
		if err != nil {
			return err
		}

		reservedV4, reservedV6, _ := reservedRangesBySite(db, site.ID)

		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if err := allocateFamily(tx, site.ID, segs, pools, reservedV4, rules, "ipv4"); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := allocateFamily(tx, site.ID, segs, pools, reservedV6, rules, "ipv6"); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}

	return nil
}

func allocateFamily(execer sqlExecer, siteID int64, segs []Segment, pools []Pool, reserved []netip.Prefix, rules ProjectRules, family string) error {
	items := poolItemsForFamily(pools, family)
	if len(items) == 0 {
		return nil
	}

	var used []netip.Prefix
	for _, s := range segs {
		if !s.Locked {
			continue
		}
		cidr := segmentCIDRByFamily(s, family)
		if !cidr.Valid {
			continue
		}
		p, err := netip.ParsePrefix(cidr.String)
		if err == nil {
			used = append(used, p)
		}
	}
	used = append(used, reserved...)

	candidates := make([]Segment, 0, len(segs))
	for _, s := range segs {
		if s.Locked {
			continue
		}
		want := desiredPrefixByFamily(s, family)
		if want == 0 {
			continue
		}
		candidates = append(candidates, s)
	}

	if len(candidates) == 0 {
		return nil
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		return desiredPrefixByFamily(candidates[i], family) < desiredPrefixByFamily(candidates[j], family)
	})

	allocations := map[int64]netip.Prefix{}
	var conflicts []Conflict
	switch rules.PoolStrategy {
	case PoolStrategyContig:
		allocations, conflicts = allocateContiguous(items, candidates, used, rules, family, true)
	case PoolStrategyTiered:
		allocations, conflicts = allocateSpillover(items, candidates, used, rules, family, true)
	default:
		allocations, conflicts = allocateSpillover(items, candidates, used, rules, family, true)
	}
	if len(conflicts) > 0 {
		return errors.New(conflicts[0].Detail)
	}

	if err := clearCIDRsByFamily(execer, siteID, family); err != nil {
		return err
	}
	for id, p := range allocations {
		if err := updateSegmentCIDRByFamily(execer, id, family, p.String()); err != nil {
			return err
		}
	}
	return nil
}

func allocateSpillover(items []poolItem, segments []Segment, used []netip.Prefix, rules ProjectRules, family string, strict bool) (map[int64]netip.Prefix, []Conflict) {
	alloc := map[int64]netip.Prefix{}
	var conflicts []Conflict
	for _, s := range segments {
		want := desiredPrefixByFamily(s, family)
		if want == 0 {
			continue
		}
		poolList := items
		if rules.PoolStrategy == PoolStrategyTiered {
			tier := segmentTierValue(s)
			poolList = filterPoolsByTier(items, tier, rules.PoolTierFallback)
		}
		var allocated *netip.Prefix
		for _, pool := range poolList {
			p, ok := allocateInPool(pool.Prefix, want, used)
			if ok {
				allocated = &p
				used = append(used, p)
				break
			}
		}
		if allocated == nil {
			conflicts = append(conflicts, Conflict{
				Kind:   "ALLOCATE_FAIL",
				Detail: "segment " + s.Name + " could not be allocated (" + family + ")",
				Level:  statusWarning.Label(),
			})
			if strict {
				break
			}
			continue
		}
		alloc[s.ID] = *allocated
	}
	return alloc, conflicts
}

func allocateContiguous(items []poolItem, segments []Segment, used []netip.Prefix, rules ProjectRules, family string, strict bool) (map[int64]netip.Prefix, []Conflict) {
	alloc := map[int64]netip.Prefix{}
	var conflicts []Conflict
	pending := make([]Segment, 0, len(segments))
	pending = append(pending, segments...)
	for _, pool := range items {
		if len(pending) == 0 {
			break
		}
		nextPending := make([]Segment, 0, len(pending))
		for _, s := range pending {
			want := desiredPrefixByFamily(s, family)
			if want == 0 {
				continue
			}
			if rules.PoolStrategy == PoolStrategyTiered {
				tier := segmentTierValue(s)
				if !poolTierMatches(pool, tier, rules.PoolTierFallback) {
					nextPending = append(nextPending, s)
					continue
				}
			}
			p, ok := allocateInPool(pool.Prefix, want, used)
			if ok {
				used = append(used, p)
				alloc[s.ID] = p
				continue
			}
			nextPending = append(nextPending, s)
		}
		pending = nextPending
	}
	if len(pending) > 0 {
		for _, s := range pending {
			conflicts = append(conflicts, Conflict{
				Kind:   "ALLOCATE_FAIL",
				Detail: "segment " + s.Name + " could not be allocated (" + family + ")",
				Level:  statusWarning.Label(),
			})
			if strict {
				break
			}
		}
	}
	return alloc, conflicts
}

func poolTierMatches(pool poolItem, tier string, fallback bool) bool {
	if tier != "" {
		if pool.Tier == tier {
			return true
		}
		return fallback
	}
	if pool.Tier == "" {
		return true
	}
	return fallback
}

func filterPoolsByTier(items []poolItem, tier string, fallback bool) []poolItem {
	if tier == "" {
		var empty []poolItem
		for _, p := range items {
			if p.Tier == "" {
				empty = append(empty, p)
			}
		}
		if len(empty) > 0 {
			return empty
		}
		if fallback {
			return items
		}
		return nil
	}
	var out []poolItem
	for _, p := range items {
		if p.Tier == tier {
			out = append(out, p)
		}
	}
	if len(out) == 0 && fallback {
		return items
	}
	return out
}

func allocateInPool(pool netip.Prefix, want int, used []netip.Prefix) (netip.Prefix, bool) {
	if pool.Addr().Is4() {
		return allocateInPoolIPv4(pool, want, used)
	}
	return allocateInPoolIPv6(pool, want, used)
}

func allocateInPoolIPv6(pool netip.Prefix, want int, used []netip.Prefix) (netip.Prefix, bool) {
	if !pool.Addr().Is6() {
		return netip.Prefix{}, false
	}
	pool = pool.Masked()
	if want < pool.Bits() {
		return netip.Prefix{}, false
	}
	step := new(big.Int).Lsh(big.NewInt(1), uint(128-want))
	poolStart := addrToBig(pool.Addr())
	poolSize := prefixSize(pool)
	poolEnd := new(big.Int).Sub(new(big.Int).Add(poolStart, poolSize), big.NewInt(1))

	usedRanges := buildUsedRangesBig(pool, used)
	cur := alignUp(poolStart, step)
	idx := 0
	for {
		candEnd := new(big.Int).Sub(new(big.Int).Add(cur, step), big.NewInt(1))
		if candEnd.Cmp(poolEnd) > 0 {
			return netip.Prefix{}, false
		}
		for idx < len(usedRanges) && usedRanges[idx].end.Cmp(cur) < 0 {
			idx++
		}
		if idx >= len(usedRanges) || candEnd.Cmp(usedRanges[idx].start) < 0 {
			addr, ok := bigToAddr(cur, 128)
			if !ok {
				return netip.Prefix{}, false
			}
			return netip.PrefixFrom(addr, want).Masked(), true
		}
		cur = new(big.Int).Add(usedRanges[idx].end, big.NewInt(1))
		cur = alignUp(cur, step)
	}
}

type bigRange struct {
	start *big.Int
	end   *big.Int
}

func buildUsedRangesBig(pool netip.Prefix, used []netip.Prefix) []bigRange {
	if len(used) == 0 {
		return nil
	}
	poolStart := addrToBig(pool.Addr())
	poolSize := prefixSize(pool)
	poolEnd := new(big.Int).Sub(new(big.Int).Add(poolStart, poolSize), big.NewInt(1))
	var out []bigRange
	for _, p := range used {
		p = p.Masked()
		start := addrToBig(p.Addr())
		size := prefixSize(p)
		end := new(big.Int).Sub(new(big.Int).Add(start, size), big.NewInt(1))
		if end.Cmp(poolStart) < 0 || start.Cmp(poolEnd) > 0 {
			continue
		}
		if start.Cmp(poolStart) < 0 {
			start = new(big.Int).Set(poolStart)
		}
		if end.Cmp(poolEnd) > 0 {
			end = new(big.Int).Set(poolEnd)
		}
		out = append(out, bigRange{start: start, end: end})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].start.Cmp(out[j].start) < 0
	})
	return mergeBigRanges(out)
}

func mergeBigRanges(ranges []bigRange) []bigRange {
	if len(ranges) == 0 {
		return nil
	}
	sorted := make([]bigRange, len(ranges))
	copy(sorted, ranges)
	out := []bigRange{sorted[0]}
	for i := 1; i < len(sorted); i++ {
		last := &out[len(out)-1]
		if sorted[i].start.Cmp(new(big.Int).Add(last.end, big.NewInt(1))) <= 0 {
			if sorted[i].end.Cmp(last.end) > 0 {
				last.end = sorted[i].end
			}
			continue
		}
		out = append(out, sorted[i])
	}
	return out
}

func desiredPrefixByFamily(s Segment, family string) int {
	if family == "ipv6" {
		if s.PrefixV6.Valid {
			return int(s.PrefixV6.Int64)
		}
		return 0
	}
	return desiredPrefix(s)
}

func segmentCIDRByFamily(s Segment, family string) sql.NullString {
	if family == "ipv6" {
		return s.CIDRV6
	}
	return s.CIDR
}

func clearCIDRsByFamily(execer sqlExecer, siteID int64, family string) error {
	if family == "ipv6" {
		_, err := execer.Exec(`UPDATE segments SET cidr_v6=NULL WHERE site_id=? AND locked=0`, siteID)
		return err
	}
	_, err := execer.Exec(`UPDATE segments SET cidr=NULL WHERE site_id=? AND locked=0`, siteID)
	return err
}

func updateSegmentCIDRByFamily(execer sqlExecer, segmentID int64, family string, cidr string) error {
	if family == "ipv6" {
		_, err := execer.Exec(`UPDATE segments SET cidr_v6=? WHERE id=?`, cidr, segmentID)
		return err
	}
	_, err := execer.Exec(`UPDATE segments SET cidr=? WHERE id=?`, cidr, segmentID)
	return err
}

func planAllocations(segs []Segment, pools []Pool, reservedV4 map[int64][]netip.Prefix, reservedV6 map[int64][]netip.Prefix, rules ProjectRules) (map[int64]netip.Prefix, map[int64]netip.Prefix, []Conflict) {
	segmentsBySite := map[int64][]Segment{}
	for _, s := range segs {
		segmentsBySite[s.SiteID] = append(segmentsBySite[s.SiteID], s)
	}
	poolsBySite := map[int64][]Pool{}
	for _, p := range pools {
		poolsBySite[p.SiteID] = append(poolsBySite[p.SiteID], p)
	}

	planV4 := map[int64]netip.Prefix{}
	planV6 := map[int64]netip.Prefix{}
	var conflicts []Conflict

	for siteID, siteSegs := range segmentsBySite {
		sitePools := poolsBySite[siteID]

		allocV4, cfV4 := planAllocateFamily(siteSegs, sitePools, reservedV4[siteID], rules, "ipv4")
		allocV6, cfV6 := planAllocateFamily(siteSegs, sitePools, reservedV6[siteID], rules, "ipv6")
		for id, p := range allocV4 {
			planV4[id] = p
		}
		for id, p := range allocV6 {
			planV6[id] = p
		}
		conflicts = append(conflicts, cfV4...)
		conflicts = append(conflicts, cfV6...)
	}

	return planV4, planV6, conflicts
}

func planAllocateFamily(segs []Segment, pools []Pool, reserved []netip.Prefix, rules ProjectRules, family string) (map[int64]netip.Prefix, []Conflict) {
	items := poolItemsForFamily(pools, family)
	if len(items) == 0 {
		if !segmentsNeedFamily(segs, family) {
			return nil, nil
		}
		return nil, []Conflict{{
			Kind:   "POOL_MISSING",
			Detail: "no pools for family " + family,
			Level:  statusWarning.Label(),
		}}
	}

	var used []netip.Prefix
	plan := map[int64]netip.Prefix{}
	var conflicts []Conflict

	for _, s := range segs {
		if !s.Locked {
			continue
		}
		cidr := segmentCIDRByFamily(s, family)
		if !cidr.Valid {
			conflicts = append(conflicts, Conflict{
				Kind:   "LOCKED_NO_CIDR",
				Detail: "segment " + s.Name + " is locked without CIDR (" + family + ")",
				Level:  statusWarning.Label(),
			})
			continue
		}
		if p, err := netip.ParsePrefix(cidr.String); err == nil {
			used = append(used, p)
			plan[s.ID] = p
		}
	}
	used = append(used, reserved...)

	var candidates []Segment
	for _, s := range segs {
		if s.Locked {
			continue
		}
		want := desiredPrefixByFamily(s, family)
		if want == 0 {
			conflicts = append(conflicts, Conflict{
				Kind:   "SIZE_MISSING",
				Detail: "segment " + s.Name + " has no size request (" + family + ")",
				Level:  statusWarning.Label(),
			})
			continue
		}
		candidates = append(candidates, s)
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		return desiredPrefixByFamily(candidates[i], family) < desiredPrefixByFamily(candidates[j], family)
	})

	var alloc map[int64]netip.Prefix
	var cf []Conflict
	switch rules.PoolStrategy {
	case PoolStrategyContig:
		alloc, cf = allocateContiguous(items, candidates, used, rules, family, false)
	case PoolStrategyTiered:
		alloc, cf = allocateSpillover(items, candidates, used, rules, family, false)
	default:
		alloc, cf = allocateSpillover(items, candidates, used, rules, family, false)
	}
	conflicts = append(conflicts, cf...)
	for id, p := range alloc {
		plan[id] = p
	}
	return plan, conflicts
}

func segmentsNeedFamily(segs []Segment, family string) bool {
	for _, s := range segs {
		if family == "ipv6" {
			if s.PrefixV6.Valid || s.CIDRV6.Valid {
				return true
			}
			continue
		}
		if s.Prefix.Valid || s.Hosts.Valid || s.CIDR.Valid {
			return true
		}
	}
	return false
}
