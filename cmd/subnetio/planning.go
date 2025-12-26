package main

import (
	"math"
	"math/big"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

type CapacityReport struct {
	Pools      []CapacityPool
	SummaryV4  CapacitySummary
	SummaryV6  CapacitySummary
	GrowthRate float64
	Months     int
	V6Unit     int
}

type CapacityPool struct {
	Site        string
	Family      string
	Tier        string
	Priority    int
	CIDR        string
	Total       string
	Used        string
	Free        string
	Utilization string
	Units       string
	Forecast    string
}

type CapacitySummary struct {
	Total       string
	Used        string
	Free        string
	Utilization string
}

func buildCapacityReport(segs []Segment, pools []Pool, sites []Site, growthRate float64, months int, v6Unit int) CapacityReport {
	reservedV4, reservedV6, _ := buildReservedIndex(sites)
	segmentsBySite := map[int64][]Segment{}
	for _, s := range segs {
		segmentsBySite[s.SiteID] = append(segmentsBySite[s.SiteID], s)
	}

	report := CapacityReport{GrowthRate: growthRate, Months: months, V6Unit: v6Unit}
	var sumV4Total, sumV4Used *big.Int
	var sumV6Total, sumV6Used *big.Int
	sumV4Total = big.NewInt(0)
	sumV4Used = big.NewInt(0)
	sumV6Total = big.NewInt(0)
	sumV6Used = big.NewInt(0)

	for _, p := range pools {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(p.CIDR))
		if err != nil {
			continue
		}
		family := normalizePoolFamily(p.Family)
		if family == "ipv4" && !prefix.Addr().Is4() {
			continue
		}
		if family == "ipv6" && !prefix.Addr().Is6() {
			continue
		}
		segments := segmentsBySite[p.SiteID]
		poolReport := CapacityPool{
			Site:     p.Site,
			Family:   family,
			Tier:     poolTierValue(p),
			Priority: p.Priority,
			CIDR:     prefix.String(),
		}

		var usedCount *big.Int
		var totalCount *big.Int
		if family == "ipv4" {
			usedRanges := buildUsedRanges(prefix, segments, reservedV4[p.SiteID])
			usedCount = sumIPv4Ranges(usedRanges)
			totalCount = prefixSize(prefix)
			sumV4Total.Add(sumV4Total, totalCount)
			sumV4Used.Add(sumV4Used, usedCount)
		} else {
			usedPrefixes := collectUsedPrefixesV6(segments, reservedV6[p.SiteID])
			usedRanges := buildUsedRangesBig(prefix, usedPrefixes)
			usedCount = sumBigRanges(usedRanges)
			totalCount = prefixSize(prefix)
			sumV6Total.Add(sumV6Total, totalCount)
			sumV6Used.Add(sumV6Used, usedCount)
			poolReport.Units = formatUnits(totalCount, usedCount, v6Unit, prefix.Bits())
		}

		freeCount := new(big.Int).Sub(new(big.Int).Set(totalCount), usedCount)
		poolReport.Total = formatBigInt(totalCount)
		poolReport.Used = formatBigInt(usedCount)
		poolReport.Free = formatBigInt(freeCount)
		poolReport.Utilization = ratioPercent(usedCount, totalCount)
		poolReport.Forecast = forecastSummary(usedCount, totalCount, growthRate, months)
		report.Pools = append(report.Pools, poolReport)
	}

	sort.SliceStable(report.Pools, func(i, j int) bool {
		if report.Pools[i].Site != report.Pools[j].Site {
			return report.Pools[i].Site < report.Pools[j].Site
		}
		if report.Pools[i].Family != report.Pools[j].Family {
			return report.Pools[i].Family < report.Pools[j].Family
		}
		if report.Pools[i].Priority != report.Pools[j].Priority {
			return report.Pools[i].Priority < report.Pools[j].Priority
		}
		return report.Pools[i].CIDR < report.Pools[j].CIDR
	})

	report.SummaryV4 = buildSummary(sumV4Used, sumV4Total)
	report.SummaryV6 = buildSummary(sumV6Used, sumV6Total)
	return report
}

func buildSummary(used, total *big.Int) CapacitySummary {
	if total == nil || total.Sign() == 0 {
		return CapacitySummary{Total: "0", Used: "0", Free: "0", Utilization: "0%"}
	}
	free := new(big.Int).Sub(new(big.Int).Set(total), used)
	return CapacitySummary{
		Total:       formatBigInt(total),
		Used:        formatBigInt(used),
		Free:        formatBigInt(free),
		Utilization: ratioPercent(used, total),
	}
}

func collectUsedPrefixesV6(segs []Segment, reserved []netip.Prefix) []netip.Prefix {
	var out []netip.Prefix
	for _, s := range segs {
		if !s.CIDRV6.Valid {
			continue
		}
		p, err := netip.ParsePrefix(s.CIDRV6.String)
		if err != nil || !p.Addr().Is6() {
			continue
		}
		out = append(out, p)
	}
	for _, r := range reserved {
		if r.Addr().Is6() {
			out = append(out, r)
		}
	}
	return out
}

func sumIPv4Ranges(ranges []ipv4Range) *big.Int {
	out := big.NewInt(0)
	for _, r := range ranges {
		size := uint64(r.end - r.start + 1)
		out.Add(out, new(big.Int).SetUint64(size))
	}
	return out
}

func sumBigRanges(ranges []bigRange) *big.Int {
	out := big.NewInt(0)
	for _, r := range ranges {
		size := new(big.Int).Add(new(big.Int).Sub(r.end, r.start), big.NewInt(1))
		out.Add(out, size)
	}
	return out
}

func formatBigInt(val *big.Int) string {
	if val == nil {
		return "0"
	}
	text := val.String()
	if len(text) <= 3 {
		return text
	}
	var parts []string
	for len(text) > 3 {
		parts = append(parts, text[len(text)-3:])
		text = text[:len(text)-3]
	}
	parts = append(parts, text)
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, "_")
}

func ratioPercent(used, total *big.Int) string {
	if total == nil || total.Sign() == 0 {
		return "0%"
	}
	rat := new(big.Rat).SetFrac(used, total)
	f, _ := rat.Float64()
	if f < 0 {
		f = 0
	}
	return strconv.FormatFloat(f*100, 'f', 1, 64) + "%"
}

func forecastSummary(used, total *big.Int, rate float64, months int) string {
	if rate <= 0 || total == nil || total.Sign() == 0 {
		return "n/a"
	}
	rat := new(big.Rat).SetFrac(used, total)
	f, _ := rat.Float64()
	if f <= 0 {
		return "n/a"
	}
	growth := math.Pow(1+(rate/100), float64(months))
	future := f * growth
	if future > 1 {
		future = 1
	}
	exhaust := math.Log(1/f) / math.Log(1+(rate/100))
	if math.IsNaN(exhaust) || math.IsInf(exhaust, 0) {
		return strconv.Itoa(months) + "m: " + strconv.FormatFloat(future*100, 'f', 1, 64) + "% used"
	}
	return strconv.Itoa(months) + "m: " + strconv.FormatFloat(future*100, 'f', 1, 64) + "% used, exhaust ~" + strconv.FormatFloat(exhaust, 'f', 0, 64) + "m"
}

func formatUnits(total, used *big.Int, unitPrefix int, poolBits int) string {
	if unitPrefix <= 0 || unitPrefix > 128 {
		return ""
	}
	if unitPrefix < poolBits {
		return ""
	}
	unitSize := new(big.Int).Lsh(big.NewInt(1), uint(128-unitPrefix))
	unitsTotal := new(big.Int).Div(total, unitSize)
	unitsUsed := divCeil(used, unitSize)
	unitsFree := new(big.Int).Sub(new(big.Int).Set(unitsTotal), unitsUsed)
	if unitsTotal.Sign() <= 0 {
		return ""
	}
	return formatBigInt(unitsUsed) + "/" + formatBigInt(unitsTotal) + " free " + formatBigInt(unitsFree) + " (/" + strconv.Itoa(unitPrefix) + ")"
}

func divCeil(a, b *big.Int) *big.Int {
	if b.Sign() == 0 {
		return big.NewInt(0)
	}
	q, r := new(big.Int), new(big.Int)
	q.QuoRem(a, b, r)
	if r.Sign() != 0 {
		q.Add(q, big.NewInt(1))
	}
	return q
}

func parseQueryFloat(raw string, def float64) float64 {
	if strings.TrimSpace(raw) == "" {
		return def
	}
	val, err := strconv.ParseFloat(raw, 64)
	if err != nil || val < 0 {
		return def
	}
	return val
}

func parseQueryInt(raw string, def int) int {
	if strings.TrimSpace(raw) == "" {
		return def
	}
	val, err := strconv.Atoi(raw)
	if err != nil || val < 0 {
		return def
	}
	return val
}
