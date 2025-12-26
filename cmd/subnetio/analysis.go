// Copyright (c) 2025 Berik Ashimov

package main

import (
	"database/sql"
	"math/big"
	"math/bits"
	"net"
	"net/netip"
	"sort"
	"strings"
)

type SegmentView struct {
	Segment
	Request      string
	CIDR         string
	RequestV6    string
	CIDRV6       string
	Mask         string
	Network      string
	Broadcast    string
	Gateway      string
	GatewayV6    string
	DhcpRange    string
	PoolLabel    string
	PoolLabelV6  string
	StatusLabel  string
	StatusClass  string
	StatusDetail string
	Reservations string
}

type SegmentStatus struct {
	Level   statusLevel
	Details []string
}

type statusLevel int

const (
	statusOK statusLevel = iota
	statusWarning
	statusConflict
)

func (s statusLevel) Label() string {
	switch s {
	case statusWarning:
		return "Warning"
	case statusConflict:
		return "Conflict"
	default:
		return "OK"
	}
}

func (s statusLevel) Class() string {
	switch s {
	case statusWarning:
		return "warning"
	case statusConflict:
		return "danger"
	default:
		return "success"
	}
}

type prefixDetails struct {
	Network     string
	Broadcast   string
	Mask        string
	FirstUsable string
	LastUsable  string
}

func buildPoolIndex(pools []Pool) (map[int64][]netip.Prefix, map[int64][]netip.Prefix) {
	outV4 := make(map[int64][]netip.Prefix)
	outV6 := make(map[int64][]netip.Prefix)
	for _, p := range pools {
		prefix, err := netip.ParsePrefix(p.CIDR)
		if err != nil {
			continue
		}
		family := normalizePoolFamily(p.Family)
		if family == "ipv6" && prefix.Addr().Is6() {
			outV6[p.SiteID] = append(outV6[p.SiteID], prefix)
			continue
		}
		if prefix.Addr().Is4() {
			outV4[p.SiteID] = append(outV4[p.SiteID], prefix)
		}
	}
	return outV4, outV6
}

type poolRef struct {
	Pool   Pool
	Prefix netip.Prefix
}

func buildPoolRefs(pools []Pool) map[int64][]poolRef {
	out := make(map[int64][]poolRef)
	for _, p := range pools {
		prefix, err := netip.ParsePrefix(p.CIDR)
		if err != nil {
			continue
		}
		out[p.SiteID] = append(out[p.SiteID], poolRef{Pool: p, Prefix: prefix})
	}
	return out
}

func poolLabelForPrefix(p netip.Prefix, pools []poolRef) string {
	best := -1
	for i, ref := range pools {
		if p.Addr().Is4() != ref.Prefix.Addr().Is4() {
			continue
		}
		if !prefixWithin(ref.Prefix, p) {
			continue
		}
		if best == -1 || poolRefBetter(ref, pools[best]) {
			best = i
		}
	}
	if best == -1 {
		return ""
	}
	label := pools[best].Pool.CIDR
	if pools[best].Pool.Tier.Valid {
		tier := strings.TrimSpace(pools[best].Pool.Tier.String)
		if tier != "" {
			label += " [" + tier + "]"
		}
	}
	if pools[best].Pool.Priority > 0 {
		label += " p" + itoa(pools[best].Pool.Priority)
	}
	return label
}

func poolRefBetter(a, b poolRef) bool {
	if a.Pool.Priority != b.Pool.Priority {
		return a.Pool.Priority < b.Pool.Priority
	}
	if a.Prefix.Bits() != b.Prefix.Bits() {
		return a.Prefix.Bits() > b.Prefix.Bits()
	}
	return a.Prefix.String() < b.Prefix.String()
}

func buildReservedIndex(sites []Site) (map[int64][]netip.Prefix, map[int64][]netip.Prefix, []Conflict) {
	outV4 := make(map[int64][]netip.Prefix)
	outV6 := make(map[int64][]netip.Prefix)
	var conflicts []Conflict
	for _, s := range sites {
		if !s.ReservedRanges.Valid {
			continue
		}
		raw := strings.TrimSpace(s.ReservedRanges.String)
		if raw == "" {
			continue
		}
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			prefix, err := netip.ParsePrefix(part)
			if err != nil {
				conflicts = append(conflicts, Conflict{
					Kind:   "RESERVED_PARSE",
					Detail: "site=" + s.Name + " bad reserved range: " + part,
					Level:  statusWarning.Label(),
				})
				continue
			}
			if prefix.Addr().Is4() {
				outV4[s.ID] = append(outV4[s.ID], prefix)
				continue
			}
			if prefix.Addr().Is6() {
				outV6[s.ID] = append(outV6[s.ID], prefix)
			}
		}
	}
	return outV4, outV6, conflicts
}

func analyzeSegments(segs []Segment, poolsV4 map[int64][]netip.Prefix, poolsV6 map[int64][]netip.Prefix, reservedV4 map[int64][]netip.Prefix, reservedV6 map[int64][]netip.Prefix, rules ProjectRules) (map[int64]SegmentStatus, []Conflict) {
	statuses := map[int64]*SegmentStatus{}
	var conflicts []Conflict

	segByID := map[int64]Segment{}
	prefixByID := map[int64]netip.Prefix{}
	prefixByIDV6 := map[int64]netip.Prefix{}
	prefixOK := map[int64]bool{}
	prefixOKV6 := map[int64]bool{}

	for _, s := range segs {
		segByID[s.ID] = s
	}

	for _, s := range segs {
		if !s.Prefix.Valid && !s.Hosts.Valid {
			addStatus(statuses, s.ID, statusWarning, "size request missing")
		}
		if s.PrefixV6.Valid && !s.CIDRV6.Valid {
			addStatus(statuses, s.ID, statusWarning, "v6 not allocated")
		}
		if !s.CIDR.Valid {
			addStatus(statuses, s.ID, statusWarning, "not allocated")
		} else {
			p, err := netip.ParsePrefix(s.CIDR.String)
			if err != nil {
				addStatus(statuses, s.ID, statusConflict, "invalid CIDR")
				conflicts = append(conflicts, Conflict{
					Kind:   "CIDR_PARSE",
					Detail: "segment " + s.Name + " site=" + s.Site + " cidr=" + s.CIDR.String + " parse error",
					Level:  statusConflict.Label(),
				})
			} else {
				prefixByID[s.ID] = p
				prefixOK[s.ID] = true

				pools := poolsV4[s.SiteID]
				if len(pools) == 0 {
					addStatus(statuses, s.ID, statusWarning, "no pool defined for site")
				} else if !prefixInAnyPool(p, pools) {
					level := statusWarning
					if rules.RequireInPool {
						level = statusConflict
					}
					addStatus(statuses, s.ID, level, "out of pool")
					conflicts = append(conflicts, Conflict{
						Kind:   "OUT_OF_POOL",
						Detail: "segment " + s.Name + " site=" + s.Site + " cidr=" + p.String() + " outside pools: " + joinPrefixes(pools),
						Level:  level.Label(),
					})
				}

				if ranges := reservedV4[s.SiteID]; len(ranges) > 0 {
					for _, r := range ranges {
						if prefixesOverlap(r, p) {
							level := statusWarning
							if !rules.AllowReservedOverlap {
								level = statusConflict
							}
							addStatus(statuses, s.ID, level, "overlaps reserved range")
							conflicts = append(conflicts, Conflict{
								Kind:   "RESERVED_OVERLAP",
								Detail: "segment " + s.Name + " site=" + s.Site + " cidr=" + p.String() + " overlaps reserved " + r.String(),
								Level:  level.Label(),
							})
							break
						}
					}
				}
			}
		}

		if s.CIDRV6.Valid {
			p6, err := netip.ParsePrefix(s.CIDRV6.String)
			if err != nil {
				addStatus(statuses, s.ID, statusConflict, "invalid CIDR v6")
				conflicts = append(conflicts, Conflict{
					Kind:   "CIDR6_PARSE",
					Detail: "segment " + s.Name + " site=" + s.Site + " cidr_v6=" + s.CIDRV6.String + " parse error",
					Level:  statusConflict.Label(),
				})
			} else {
				prefixByIDV6[s.ID] = p6
				prefixOKV6[s.ID] = true

				pools := poolsV6[s.SiteID]
				if len(pools) == 0 {
					addStatus(statuses, s.ID, statusWarning, "no v6 pool defined for site")
				} else if !prefixInAnyPool(p6, pools) {
					level := statusWarning
					if rules.RequireInPool {
						level = statusConflict
					}
					addStatus(statuses, s.ID, level, "v6 out of pool")
					conflicts = append(conflicts, Conflict{
						Kind:   "OUT_OF_POOL_V6",
						Detail: "segment " + s.Name + " site=" + s.Site + " cidr_v6=" + p6.String() + " outside v6 pools: " + joinPrefixes(pools),
						Level:  level.Label(),
					})
				}

				if ranges := reservedV6[s.SiteID]; len(ranges) > 0 {
					for _, r := range ranges {
						if prefixesOverlap(r, p6) {
							level := statusWarning
							if !rules.AllowReservedOverlap {
								level = statusConflict
							}
							addStatus(statuses, s.ID, level, "overlaps v6 reserved range")
							conflicts = append(conflicts, Conflict{
								Kind:   "RESERVED_OVERLAP_V6",
								Detail: "segment " + s.Name + " site=" + s.Site + " cidr_v6=" + p6.String() + " overlaps reserved " + r.String(),
								Level:  level.Label(),
							})
							break
						}
					}
				}
			}
		}
	}

	type key struct{ site, vrf string }
	group := map[key][]int64{}
	for _, s := range segs {
		if !prefixOK[s.ID] {
			continue
		}
		group[key{s.Site, s.VRF}] = append(group[key{s.Site, s.VRF}], s.ID)
	}
	for k, ids := range group {
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				a := ids[i]
				b := ids[j]
				p1 := prefixByID[a]
				p2 := prefixByID[b]
				if prefixesOverlap(p1, p2) {
					s1 := segByID[a]
					s2 := segByID[b]
					addStatus(statuses, s1.ID, statusConflict, "overlap with "+s2.Name)
					addStatus(statuses, s2.ID, statusConflict, "overlap with "+s1.Name)
					conflicts = append(conflicts, Conflict{
						Kind:   "OVERLAP",
						Detail: "site=" + k.site + " vrf=" + k.vrf + ": " + s1.Name + " " + p1.String() + " overlaps " + s2.Name + " " + p2.String(),
						Level:  statusConflict.Label(),
					})
				}
			}
		}
	}

	groupV6 := map[key][]int64{}
	for _, s := range segs {
		if !prefixOKV6[s.ID] {
			continue
		}
		groupV6[key{s.Site, s.VRF}] = append(groupV6[key{s.Site, s.VRF}], s.ID)
	}
	for k, ids := range groupV6 {
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				a := ids[i]
				b := ids[j]
				p1 := prefixByIDV6[a]
				p2 := prefixByIDV6[b]
				if prefixesOverlap(p1, p2) {
					s1 := segByID[a]
					s2 := segByID[b]
					addStatus(statuses, s1.ID, statusConflict, "v6 overlap with "+s2.Name)
					addStatus(statuses, s2.ID, statusConflict, "v6 overlap with "+s1.Name)
					conflicts = append(conflicts, Conflict{
						Kind:   "OVERLAP_V6",
						Detail: "site=" + k.site + " vrf=" + k.vrf + ": " + s1.Name + " " + p1.String() + " overlaps " + s2.Name + " " + p2.String(),
						Level:  statusConflict.Label(),
					})
				}
			}
		}
	}

	seenVLAN := map[string]int64{}
	for _, s := range segs {
		k := vlanKey(s, rules)
		if firstID, ok := seenVLAN[k]; ok {
			first := segByID[firstID]
			addStatus(statuses, s.ID, statusConflict, "duplicate VLAN")
			addStatus(statuses, firstID, statusConflict, "duplicate VLAN")
			conflicts = append(conflicts, Conflict{
				Kind:   "VLAN_DUP",
				Detail: "site=" + s.Site + " vrf=" + s.VRF + " vlan=" + itoa(s.VLAN) + " duplicated: " + first.Name + ", " + s.Name,
				Level:  statusConflict.Label(),
			})
		} else {
			seenVLAN[k] = s.ID
		}
	}

	out := make(map[int64]SegmentStatus, len(segs))
	for _, s := range segs {
		st, ok := statuses[s.ID]
		if !ok {
			out[s.ID] = SegmentStatus{Level: statusOK}
			continue
		}
		out[s.ID] = *st
	}
	return out, conflicts
}

func buildSegmentViews(segs []Segment, statuses map[int64]SegmentStatus, pools []Pool) []SegmentView {
	poolIndex := buildPoolRefs(pools)
	out := make([]SegmentView, 0, len(segs))
	for _, s := range segs {
		view := SegmentView{Segment: s}
		view.Request = formatRequest(s)
		view.RequestV6 = formatRequestV6(s)

		status := statuses[s.ID]
		view.StatusLabel = status.Level.Label()
		view.StatusClass = status.Level.Class()
		view.StatusDetail = strings.Join(status.Details, "; ")

		view.CIDR = cidrString(s.CIDR)
		view.CIDRV6 = cidrString(s.CIDRV6)
		if s.CIDR.Valid {
			if p, err := netip.ParsePrefix(s.CIDR.String); err == nil {
				if details, ok := prefixDetailsIPv4(p); ok {
					view.Mask = details.Mask
					view.Network = details.Network
					view.Broadcast = details.Broadcast
					view.Gateway = segmentGateway(s, details)
					view.DhcpRange = segmentDhcpRange(s, details, view.Gateway)
				}
				view.PoolLabel = poolLabelForPrefix(p, poolIndex[s.SiteID])
			}
		}
		if s.CIDRV6.Valid {
			if p, err := netip.ParsePrefix(s.CIDRV6.String); err == nil {
				view.GatewayV6 = segmentGatewayV6(s, p)
				view.PoolLabelV6 = poolLabelForPrefix(p, poolIndex[s.SiteID])
			}
		}
		if view.Gateway == "" && s.Gateway.Valid {
			view.Gateway = s.Gateway.String
		}
		if view.GatewayV6 == "" && s.GatewayV6.Valid {
			view.GatewayV6 = s.GatewayV6.String
		}
		if view.DhcpRange == "" {
			if s.DhcpEnabled {
				view.DhcpRange = "auto"
			} else {
				view.DhcpRange = "Off"
			}
		}
		if s.DhcpReservations.Valid {
			view.Reservations = strings.TrimSpace(s.DhcpReservations.String)
		}
		out = append(out, view)
	}
	return out
}

func formatRequest(s Segment) string {
	if s.Prefix.Valid {
		return "/" + itoa64(s.Prefix.Int64)
	}
	if s.Hosts.Valid {
		return itoa64(s.Hosts.Int64) + " hosts"
	}
	return "-"
}

func formatRequestV6(s Segment) string {
	if s.PrefixV6.Valid {
		return "/" + itoa64(s.PrefixV6.Int64)
	}
	return "-"
}

func cidrString(cidr sql.NullString) string {
	if cidr.Valid {
		return cidr.String
	}
	return ""
}

func prefixDetailsIPv4(p netip.Prefix) (prefixDetails, bool) {
	if !p.Addr().Is4() {
		return prefixDetails{}, false
	}
	bits := p.Bits()
	network := p.Masked().Addr()
	start := uint64(ipv4ToU32(network))
	size := uint64(1) << uint(32-bits)
	broadcast := u32ToIPv4(uint32(start + size - 1))
	mask := net.IP(net.CIDRMask(bits, 32)).String()
	details := prefixDetails{
		Network:   network.String(),
		Broadcast: broadcast.String(),
		Mask:      mask,
	}
	if bits <= 30 {
		details.FirstUsable = u32ToIPv4(uint32(start + 1)).String()
		details.LastUsable = u32ToIPv4(uint32(start + size - 2)).String()
	}
	return details, true
}

func segmentGateway(s Segment, details prefixDetails) string {
	if s.Gateway.Valid && strings.TrimSpace(s.Gateway.String) != "" {
		return strings.TrimSpace(s.Gateway.String)
	}
	return details.FirstUsable
}

func segmentGatewayV6(s Segment, prefix netip.Prefix) string {
	if s.GatewayV6.Valid && strings.TrimSpace(s.GatewayV6.String) != "" {
		return strings.TrimSpace(s.GatewayV6.String)
	}
	masked := prefix.Masked()
	size := prefixSize(masked)
	if size.Cmp(big.NewInt(2)) < 0 {
		return ""
	}
	start := addrToBig(masked.Addr())
	first := new(big.Int).Add(start, big.NewInt(1))
	addr, ok := bigToAddr(first, addrBitLen(masked.Addr()))
	if !ok {
		return ""
	}
	return addr.String()
}

func segmentDhcpRange(s Segment, details prefixDetails, gateway string) string {
	if !s.DhcpEnabled {
		return "Off"
	}
	if s.DhcpRange.Valid && strings.TrimSpace(s.DhcpRange.String) != "" {
		return strings.TrimSpace(s.DhcpRange.String)
	}
	if details.FirstUsable == "" || details.LastUsable == "" {
		return "auto"
	}
	startAddr, err1 := netip.ParseAddr(details.FirstUsable)
	endAddr, err2 := netip.ParseAddr(details.LastUsable)
	if err1 != nil || err2 != nil || !startAddr.Is4() || !endAddr.Is4() {
		return "auto"
	}
	start := ipv4ToU32(startAddr)
	end := ipv4ToU32(endAddr)
	if gateway != "" {
		if gw, err := netip.ParseAddr(gateway); err == nil && gw.Is4() {
			gwU := ipv4ToU32(gw)
			if gwU >= start && gwU <= end && gwU == start {
				start++
			}
		}
	}
	if start > end {
		return "auto"
	}
	return u32ToIPv4(start).String() + " - " + u32ToIPv4(end).String() + " (auto)"
}

func prefixInAnyPool(p netip.Prefix, pools []netip.Prefix) bool {
	for _, pool := range pools {
		if prefixWithin(pool, p) {
			return true
		}
	}
	return false
}

func joinPrefixes(pools []netip.Prefix) string {
	out := make([]string, 0, len(pools))
	for _, p := range pools {
		out = append(out, p.String())
	}
	return strings.Join(out, ", ")
}

func addStatus(statuses map[int64]*SegmentStatus, id int64, level statusLevel, detail string) {
	st, ok := statuses[id]
	if !ok {
		st = &SegmentStatus{Level: level}
		statuses[id] = st
	}
	if level > st.Level {
		st.Level = level
	}
	if detail != "" {
		st.Details = append(st.Details, detail)
	}
}

func analyzeAll(segs []Segment, pools []Pool, sites []Site, rules ProjectRules) (map[int64]SegmentStatus, []Conflict) {
	poolsBySiteV4, poolsBySiteV6 := buildPoolIndex(pools)
	reservedV4, reservedV6, reservedConflicts := buildReservedIndex(sites)
	statuses, conflicts := analyzeSegments(segs, poolsBySiteV4, poolsBySiteV6, reservedV4, reservedV6, rules)
	hints := analyzeEfficiency(segs, poolsBySiteV4, poolsBySiteV6, reservedV4, reservedV6, rules)
	conflicts = append(reservedConflicts, conflicts...)
	conflicts = append(conflicts, hints...)
	return statuses, conflicts
}

type ipv4Range struct {
	start uint32
	end   uint32
}

func analyzeEfficiency(segs []Segment, poolsBySiteV4 map[int64][]netip.Prefix, poolsBySiteV6 map[int64][]netip.Prefix, reservedV4 map[int64][]netip.Prefix, reservedV6 map[int64][]netip.Prefix, rules ProjectRules) []Conflict {
	var out []Conflict

	segmentsBySite := map[int64][]Segment{}
	for _, s := range segs {
		segmentsBySite[s.SiteID] = append(segmentsBySite[s.SiteID], s)
	}

	for siteID, pools := range poolsBySiteV4 {
		segments := segmentsBySite[siteID]
		for _, pool := range pools {
			if !pool.Addr().Is4() {
				continue
			}
			used := buildUsedRanges(pool, segments, reservedV4[siteID])
			gaps := freeRanges(pool, used)
			if len(gaps) == 0 {
				continue
			}
			totalFree := uint64(0)
			largest := uint64(0)
			for _, g := range gaps {
				size := uint64(g.end-g.start) + 1
				totalFree += size
				if size > largest {
					largest = size
				}
			}
			fragScore := fragmentationScore(totalFree, largest)
			out = append(out, Conflict{
				Kind:   "POOL_FRAGMENTATION",
				Detail: "pool " + pool.String() + ": free " + itoa64(int64(totalFree)) + " addrs, gaps=" + itoa(len(gaps)) + ", fragmentation=" + itoa(fragScore) + "%",
				Level:  statusWarning.Label(),
			})
			limit := 3
			for _, g := range gaps {
				if limit == 0 {
					break
				}
				for _, p := range rangeToPrefixes(g) {
					if limit == 0 {
						break
					}
					out = append(out, Conflict{
						Kind:   "POOL_GAP",
						Detail: "pool " + pool.String() + " free block " + p.String(),
						Level:  statusWarning.Label(),
					})
					limit--
				}
			}
		}
	}

	for siteID, pools := range poolsBySiteV6 {
		segments := segmentsBySite[siteID]
		for _, pool := range pools {
			if !pool.Addr().Is6() {
				continue
			}
			usedPrefixes := collectUsedPrefixesV6(segments, reservedV6[siteID])
			used := buildUsedRangesBig(pool, usedPrefixes)
			gaps := freeRangesBig(pool, used)
			if len(gaps) == 0 {
				continue
			}
			totalFree := big.NewInt(0)
			largest := big.NewInt(0)
			for _, g := range gaps {
				size := bigRangeSize(g)
				totalFree.Add(totalFree, size)
				if size.Cmp(largest) > 0 {
					largest = size
				}
			}
			unitPrefix := 64
			if pool.Bits() > unitPrefix {
				unitPrefix = pool.Bits()
			}
			unitSize := new(big.Int).Lsh(big.NewInt(1), uint(128-unitPrefix))
			totalUnits := new(big.Int).Div(totalFree, unitSize)
			largestUnits := new(big.Int).Div(largest, unitSize)
			fragScore := fragmentationScoreBig(totalUnits, largestUnits)
			out = append(out, Conflict{
				Kind:   "POOL_FRAGMENTATION_V6",
				Detail: "pool " + pool.String() + ": free " + formatBigInt(totalUnits) + " /" + itoa(unitPrefix) + " blocks, gaps=" + itoa(len(gaps)) + ", fragmentation=" + itoa(fragScore) + "%",
				Level:  statusWarning.Label(),
			})
			limit := 3
			for _, g := range gaps {
				if limit == 0 {
					break
				}
				for _, p := range bigRangeToPrefixes(g, unitPrefix, limit) {
					if limit == 0 {
						break
					}
					out = append(out, Conflict{
						Kind:   "POOL_GAP_V6",
						Detail: "pool " + pool.String() + " free block " + p.String(),
						Level:  statusWarning.Label(),
					})
					limit--
				}
			}
		}
	}

	for _, s := range segs {
		if !s.Hosts.Valid || !s.CIDR.Valid {
			continue
		}
		prefix, err := netip.ParsePrefix(s.CIDR.String)
		if err != nil || !prefix.Addr().Is4() {
			continue
		}
		required := hostsToPrefixIPv4(int(s.Hosts.Int64))
		if required <= 0 {
			continue
		}
		if prefix.Bits() >= required {
			continue
		}
		actualSize := uint64(1) << uint(32-prefix.Bits())
		requiredSize := uint64(1) << uint(32-required)
		unusedPct := int((actualSize - requiredSize) * 100 / actualSize)
		if unusedPct >= rules.OversizeThreshold {
			out = append(out, Conflict{
				Kind:   "OVERSIZED",
				Detail: "segment " + s.Name + " site=" + s.Site + " " + prefix.String() + " exceeds hosts by " + itoa(unusedPct) + "% (need /" + itoa(required) + ")",
				Level:  statusWarning.Label(),
			})
		}
	}

	for _, s := range segs {
		if !s.PrefixV6.Valid || !s.CIDRV6.Valid {
			continue
		}
		prefix, err := netip.ParsePrefix(s.CIDRV6.String)
		if err != nil || !prefix.Addr().Is6() {
			continue
		}
		requested := int(s.PrefixV6.Int64)
		if requested <= 0 || requested > 128 {
			continue
		}
		if prefix.Bits() >= requested {
			continue
		}
		actualSize := prefixSize(prefix)
		requestedSize := new(big.Int).Lsh(big.NewInt(1), uint(128-requested))
		unusedPct := percentBig(new(big.Int).Sub(actualSize, requestedSize), actualSize)
		if unusedPct >= rules.OversizeThreshold {
			out = append(out, Conflict{
				Kind:   "OVERSIZED_V6",
				Detail: "segment " + s.Name + " site=" + s.Site + " " + prefix.String() + " exceeds v6 request by " + itoa(unusedPct) + "% (need /" + itoa(requested) + ")",
				Level:  statusWarning.Label(),
			})
		}
	}

	return out
}

func buildUsedRanges(pool netip.Prefix, segs []Segment, reserved []netip.Prefix) []ipv4Range {
	var ranges []ipv4Range
	for _, s := range segs {
		if !s.CIDR.Valid {
			continue
		}
		p, err := netip.ParsePrefix(s.CIDR.String)
		if err != nil || !p.Addr().Is4() {
			continue
		}
		if r, ok := prefixRangeWithin(pool, p); ok {
			ranges = append(ranges, r)
		}
	}
	for _, r := range reserved {
		if !r.Addr().Is4() {
			continue
		}
		if rr, ok := prefixRangeWithin(pool, r); ok {
			ranges = append(ranges, rr)
		}
	}
	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].start == ranges[j].start {
			return ranges[i].end < ranges[j].end
		}
		return ranges[i].start < ranges[j].start
	})
	return mergeRanges(ranges)
}

func prefixRangeWithin(pool, p netip.Prefix) (ipv4Range, bool) {
	poolStart := ipv4ToU32(pool.Masked().Addr())
	poolEnd := poolStart + uint32((1<<(32-pool.Bits()))-1)
	pStart := ipv4ToU32(p.Masked().Addr())
	pEnd := pStart + uint32((1<<(32-p.Bits()))-1)
	if pEnd < poolStart || pStart > poolEnd {
		return ipv4Range{}, false
	}
	if pStart < poolStart {
		pStart = poolStart
	}
	if pEnd > poolEnd {
		pEnd = poolEnd
	}
	return ipv4Range{start: pStart, end: pEnd}, true
}

func mergeRanges(ranges []ipv4Range) []ipv4Range {
	if len(ranges) == 0 {
		return nil
	}
	out := []ipv4Range{ranges[0]}
	for _, r := range ranges[1:] {
		last := &out[len(out)-1]
		if r.start <= last.end+1 {
			if r.end > last.end {
				last.end = r.end
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

func freeRanges(pool netip.Prefix, used []ipv4Range) []ipv4Range {
	poolStart := ipv4ToU32(pool.Masked().Addr())
	poolEnd := poolStart + uint32((1<<(32-pool.Bits()))-1)
	if len(used) == 0 {
		return []ipv4Range{{start: poolStart, end: poolEnd}}
	}
	var gaps []ipv4Range
	cur := poolStart
	for _, r := range used {
		if r.start > cur {
			gaps = append(gaps, ipv4Range{start: cur, end: r.start - 1})
		}
		if r.end+1 > cur {
			cur = r.end + 1
		}
		if cur > poolEnd {
			break
		}
	}
	if cur <= poolEnd {
		gaps = append(gaps, ipv4Range{start: cur, end: poolEnd})
	}
	return gaps
}

func freeRangesBig(pool netip.Prefix, used []bigRange) []bigRange {
	masked := pool.Masked()
	poolStart := addrToBig(masked.Addr())
	poolSize := prefixSize(masked)
	poolEnd := new(big.Int).Sub(new(big.Int).Add(poolStart, poolSize), big.NewInt(1))
	if len(used) == 0 {
		return []bigRange{{start: poolStart, end: poolEnd}}
	}
	var gaps []bigRange
	cur := new(big.Int).Set(poolStart)
	for _, r := range used {
		if r.start.Cmp(cur) > 0 {
			gaps = append(gaps, bigRange{start: new(big.Int).Set(cur), end: new(big.Int).Sub(r.start, big.NewInt(1))})
		}
		next := new(big.Int).Add(r.end, big.NewInt(1))
		if next.Cmp(cur) > 0 {
			cur = next
		}
		if cur.Cmp(poolEnd) > 0 {
			break
		}
	}
	if cur.Cmp(poolEnd) <= 0 {
		gaps = append(gaps, bigRange{start: new(big.Int).Set(cur), end: new(big.Int).Set(poolEnd)})
	}
	return gaps
}

func rangeToPrefixes(r ipv4Range) []netip.Prefix {
	var out []netip.Prefix
	start := r.start
	end := r.end
	for start <= end {
		maxPref := 32 - bits.TrailingZeros32(start)
		remaining := uint64(end-start) + 1
		for maxPref < 32 {
			block := uint64(1) << uint(32-maxPref)
			if block <= remaining {
				break
			}
			maxPref++
		}
		p := netip.PrefixFrom(u32ToIPv4(start), int(maxPref)).Masked()
		out = append(out, p)
		blockSize := uint64(1) << uint(32-maxPref)
		if maxPref == 0 {
			break
		}
		start += uint32(blockSize)
	}
	return out
}

func bigRangeToPrefixes(r bigRange, unitPrefix int, limit int) []netip.Prefix {
	if unitPrefix <= 0 || unitPrefix > 128 || limit <= 0 {
		return nil
	}
	step := new(big.Int).Lsh(big.NewInt(1), uint(128-unitPrefix))
	start := alignUp(new(big.Int).Set(r.start), step)
	end := r.end
	var out []netip.Prefix
	for len(out) < limit {
		blockEnd := new(big.Int).Sub(new(big.Int).Add(start, step), big.NewInt(1))
		if blockEnd.Cmp(end) > 0 {
			break
		}
		addr, ok := bigToAddr(start, 128)
		if !ok {
			break
		}
		out = append(out, netip.PrefixFrom(addr, unitPrefix).Masked())
		start = new(big.Int).Add(start, step)
	}
	return out
}

func fragmentationScore(total, largest uint64) int {
	if total == 0 {
		return 0
	}
	frag := int((total - largest) * 100 / total)
	if frag < 0 {
		return 0
	}
	if frag > 100 {
		return 100
	}
	return frag
}

func fragmentationScoreBig(total, largest *big.Int) int {
	if total == nil || total.Sign() == 0 {
		return 0
	}
	remaining := new(big.Int).Sub(new(big.Int).Set(total), largest)
	if remaining.Sign() < 0 {
		return 0
	}
	rat := new(big.Rat).SetFrac(remaining, total)
	f, _ := rat.Float64()
	frag := int(f * 100)
	if frag < 0 {
		return 0
	}
	if frag > 100 {
		return 100
	}
	return frag
}

func percentBig(num, denom *big.Int) int {
	if denom == nil || denom.Sign() == 0 {
		return 0
	}
	rat := new(big.Rat).SetFrac(num, denom)
	f, _ := rat.Float64()
	pct := int(f * 100)
	if pct < 0 {
		return 0
	}
	if pct > 100 {
		return 100
	}
	return pct
}

func bigRangeSize(r bigRange) *big.Int {
	return new(big.Int).Add(new(big.Int).Sub(r.end, r.start), big.NewInt(1))
}
