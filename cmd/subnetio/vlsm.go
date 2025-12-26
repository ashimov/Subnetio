package main

import (
	"database/sql"
	"errors"
	"net/netip"
	"strings"
)

type Conflict struct {
	Kind   string
	Detail string
	Level  string
}

func prefixesOverlap(a, b netip.Prefix) bool {
	return a.Contains(b.Addr()) || b.Contains(a.Addr())
}

func desiredPrefix(s Segment) int {
	if s.Prefix.Valid {
		return int(s.Prefix.Int64)
	}
	if s.Hosts.Valid {
		return hostsToPrefixIPv4(int(s.Hosts.Int64))
	}
	return 0
}

func hostsToPrefixIPv4(hosts int) int {
	// сеть должна вместить hosts + 3 (network,gateway,broadcast)
	need := hosts + 3
	for p := 32; p >= 1; p-- {
		size := 1 << (32 - p)
		if size >= need {
			return p
		}
	}
	return 1
}

func allocateInPoolIPv4(pool netip.Prefix, want int, used []netip.Prefix) (netip.Prefix, bool) {
	if want < 1 || want > 32 {
		return netip.Prefix{}, false
	}
	step := uint32(1 << (32 - want))
	start := ipv4ToU32(pool.Masked().Addr())
	end := start + uint32(1<<(32-pool.Bits()))

	for cur := start; cur+step <= end; cur += step {
		cand := netip.PrefixFrom(u32ToIPv4(cur), want).Masked()
		if !pool.Contains(cand.Addr()) {
			continue
		}
		if overlapsAny(cand, used) {
			continue
		}
		return cand, true
	}
	return netip.Prefix{}, false
}

func overlapsAny(p netip.Prefix, used []netip.Prefix) bool {
	for _, u := range used {
		if prefixesOverlap(u, p) {
			return true
		}
	}
	return false
}

func poolsBySite(db *sql.DB, siteID int64) ([]Pool, error) {
	rows, err := db.Query(`
		SELECT id, site_id, '' as site, cidr,
			COALESCE(family, 'ipv4'), tier, COALESCE(priority, 0)
		FROM pools WHERE site_id=?
		ORDER BY COALESCE(priority, 0), cidr`, siteID)
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

func reservedRangesBySite(db *sql.DB, siteID int64) ([]netip.Prefix, []netip.Prefix, error) {
	var raw sql.NullString
	if err := db.QueryRow(`SELECT reserved_ranges FROM site_meta WHERE site_id=?`, siteID).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return nil, nil, nil
	}
	var outV4 []netip.Prefix
	var outV6 []netip.Prefix
	for _, part := range strings.Split(raw.String, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if p, err := netip.ParsePrefix(part); err == nil {
			if p.Addr().Is4() {
				outV4 = append(outV4, p)
			} else if p.Addr().Is6() {
				outV6 = append(outV6, p)
			}
		}
	}
	return outV4, outV6, nil
}

func segmentsBySite(db *sql.DB, siteID int64) ([]Segment, error) {
	rows, err := db.Query(`
		SELECT s.id, s.site_id, si.name, s.vrf, s.vlan, s.name, s.hosts, s.prefix, s.cidr,
			s.prefix_v6, s.cidr_v6, s.locked,
			sm.pool_tier
		FROM segments s
		JOIN sites si ON si.id = s.site_id
		LEFT JOIN segment_meta sm ON sm.segment_id = s.id
		WHERE s.site_id=?
		ORDER BY s.id`, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Segment
	for rows.Next() {
		var seg Segment
		var lockedInt int
		if err := rows.Scan(
			&seg.ID, &seg.SiteID, &seg.Site, &seg.VRF, &seg.VLAN, &seg.Name,
			&seg.Hosts, &seg.Prefix, &seg.CIDR, &seg.PrefixV6, &seg.CIDRV6, &lockedInt, &seg.PoolTier,
		); err != nil {
			return nil, err
		}
		seg.Locked = lockedInt != 0
		out = append(out, seg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// helpers
func ipv4ToU32(a netip.Addr) uint32 {
	b := a.As4()
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}
func u32ToIPv4(v uint32) netip.Addr {
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

func itoa(i int) string { return itoa64(int64(i)) }
func itoa64(i int64) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [32]byte
	n := len(buf)
	for i > 0 {
		n--
		buf[n] = byte('0' + (i % 10))
		i /= 10
	}
	if neg {
		n--
		buf[n] = '-'
	}
	return string(buf[n:])
}
