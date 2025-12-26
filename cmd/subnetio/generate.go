package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/gin-gonic/gin"
)

const customTemplateDir = "data/templates"

//go:embed templates/*.tmpl
var genTemplateFS embed.FS

var defaultTemplateVersions = map[string]string{
	"vyos":     "v1",
	"cisco":    "v1",
	"juniper":  "v1",
	"mikrotik": "v1",
}

var templateCommentPrefixes = map[string]string{
	"vyos":     "#",
	"cisco":    "!",
	"juniper":  "#",
	"mikrotik": "#",
}

var templateExamples = map[string]string{
	"vyos":     "# Example (VyOS v1)\nset vrf name PROD\nset interfaces vlan vlan10 address 10.30.10.1/24\nset service dhcp-server shared-network-name prod-10 subnet 10.30.10.0/24 default-router 10.30.10.1\n",
	"cisco":    "! Example (Cisco v1)\nvlan 10\n name users\ninterface Vlan10\n description users\n ip address 10.30.10.1 255.255.255.0\n no shutdown\n",
	"juniper":  "# Example (JunOS v1)\nset vlans vlan10 vlan-id 10\nset interfaces irb unit 10 family inet address 10.30.10.1/24\n",
	"mikrotik": "# Example (Mikrotik v1)\n/interface vlan add name=vlan10 vlan-id=10 interface=bridge1\n/ip address add address=10.30.10.1/24 interface=vlan10\n",
}

type DHCPOptions struct {
	Search           []string
	SearchRaw        string
	LeaseTime        int
	RenewTime        int
	RebindTime       int
	BootFile         string
	NextServer       string
	VendorOptions    []string
	VendorOptionsRaw string
}

type GenerateOptions struct {
	Template       string
	IncludeVRF     bool
	IncludeVLAN    bool
	IncludeDHCP    bool
	SiteFilter     string
	VRFFilter      string
	SegmentFilter  string
	DomainOverride string
	ShowDiff       bool
}

type TemplateInfo struct {
	Name    string
	Version string
	Source  string
}

func parseGenerateOptions(c *gin.Context) GenerateOptions {
	opts := GenerateOptions{
		IncludeVRF:  true,
		IncludeVLAN: true,
		IncludeDHCP: true,
	}
	opts.Template = strings.ToLower(strings.TrimSpace(c.Query("template")))
	opts.SiteFilter = strings.TrimSpace(c.Query("filter_site"))
	opts.VRFFilter = strings.TrimSpace(c.Query("filter_vrf"))
	opts.SegmentFilter = strings.TrimSpace(c.Query("filter_segment"))
	opts.DomainOverride = strings.TrimSpace(c.Query("domain_name"))
	opts.ShowDiff = c.Query("show_diff") != ""
	if opts.Template != "" {
		opts.IncludeVRF = c.Query("include_vrf") != ""
		opts.IncludeVLAN = c.Query("include_vlan") != ""
		opts.IncludeDHCP = c.Query("include_dhcp") != ""
	}
	return opts
}

func (o GenerateOptions) QueryString(projectID int64) string {
	v := url.Values{}
	if projectID > 0 {
		v.Set("project_id", itoa64(projectID))
	}
	if o.Template != "" {
		v.Set("template", o.Template)
	}
	if o.IncludeVRF {
		v.Set("include_vrf", "on")
	}
	if o.IncludeVLAN {
		v.Set("include_vlan", "on")
	}
	if o.IncludeDHCP {
		v.Set("include_dhcp", "on")
	}
	if o.SiteFilter != "" {
		v.Set("filter_site", o.SiteFilter)
	}
	if o.VRFFilter != "" {
		v.Set("filter_vrf", o.VRFFilter)
	}
	if o.SegmentFilter != "" {
		v.Set("filter_segment", o.SegmentFilter)
	}
	if o.DomainOverride != "" {
		v.Set("domain_name", o.DomainOverride)
	}
	if o.ShowDiff {
		v.Set("show_diff", "on")
	}
	return v.Encode()
}

type renderSegment struct {
	Site        string
	VRF         string
	VLAN        int
	Name        string
	Prefix      netip.Prefix
	PrefixBits  int
	Network     string
	Mask        string
	Gateway     string
	DhcpEnabled bool
	DhcpStart   string
	DhcpEnd     string
	DNS         []string
	NTP         []string
	Domain      string
	DHCP        DHCPOptions
}

type SiteDefaults struct {
	DNS           []string
	NTP           []string
	GatewayPolicy string
}

type renderVLAN struct {
	VLAN       int
	Name       string
	Gateway    string
	Mask       string
	PrefixBits int
}

type GenerateMetadata struct {
	GeneratedAt     string            `json:"generated_at" yaml:"generated_at"`
	ProjectID       int64             `json:"project_id" yaml:"project_id"`
	ProjectName     string            `json:"project_name" yaml:"project_name"`
	Template        string            `json:"template" yaml:"template"`
	TemplateVersion string            `json:"template_version,omitempty" yaml:"template_version,omitempty"`
	TemplateSource  string            `json:"template_source,omitempty" yaml:"template_source,omitempty"`
	Options         map[string]string `json:"options" yaml:"options"`
	Filters         map[string]string `json:"filters" yaml:"filters"`
	DomainName      string            `json:"domain_name,omitempty" yaml:"domain_name,omitempty"`
	SegmentCount    int               `json:"segment_count" yaml:"segment_count"`
	SiteCount       int               `json:"site_count" yaml:"site_count"`
	VRFCount        int               `json:"vrf_count" yaml:"vrf_count"`
	VLANCount       int               `json:"vlan_count" yaml:"vlan_count"`
	DHCPCount       int               `json:"dhcp_count" yaml:"dhcp_count"`
	Checksum        string            `json:"checksum,omitempty" yaml:"checksum,omitempty"`
}

type segmentGroup struct {
	Site     string
	VRF      string
	Segments []renderSegment
	VLANs    []renderVLAN
}

type TemplateContext struct {
	Meta     GenerateMetadata
	Header   string
	Options  GenerateOptions
	Groups   []segmentGroup
	Segments []renderSegment
	Defaults DHCPOptions
}

type GenerateResult struct {
	Output         string
	Metadata       GenerateMetadata
	TemplateSource string
}

type templateSource struct {
	Content string
	Version string
	Source  string
}

func generateConfig(opts GenerateOptions, views []SegmentView, sites []Site, project Project, meta ProjectMeta) (GenerateResult, error) {
	if strings.TrimSpace(opts.Template) == "" {
		return GenerateResult{}, nil
	}

	name, err := normalizeTemplateName(opts.Template)
	if err != nil {
		return GenerateResult{}, err
	}
	source, err := loadTemplateSource(name)
	if err != nil {
		return GenerateResult{}, err
	}
	opts.Template = name

	domain := resolveDomain(opts, meta)
	defaults := projectDHCPDefaults(meta, domain)
	siteDefaults := buildSiteDefaults(sites, meta)
	dhcpBySite := buildDHCPBySite(sites, defaults, domain)
	segments := buildRenderSegments(opts, views, sites, domain, dhcpBySite, siteDefaults)
	metadata := buildMetadata(opts, project, domain, segments, defaults, source.Version, source.Source)
	prefix := templateCommentPrefix(name)
	header := metadataHeader(metadata, prefix)

	if len(segments) == 0 {
		msg := prefix + " no allocated segments"
		output := strings.TrimSpace(header + msg)
		return GenerateResult{Output: output, Metadata: metadata, TemplateSource: source.Source}, nil
	}

	ctx := TemplateContext{
		Meta:     metadata,
		Header:   header,
		Options:  opts,
		Groups:   groupSegments(segments),
		Segments: segments,
		Defaults: defaults,
	}
	out, err := renderTemplate(name, source.Content, ctx)
	if err != nil {
		return GenerateResult{}, err
	}
	return GenerateResult{Output: out, Metadata: metadata, TemplateSource: source.Source}, nil
}

func resolveDomain(opts GenerateOptions, meta ProjectMeta) string {
	domain := strings.TrimSpace(opts.DomainOverride)
	if domain != "" {
		return domain
	}
	if meta.DomainName.Valid {
		return strings.TrimSpace(meta.DomainName.String)
	}
	return ""
}

func normalizeDHCPOptions(opts DHCPOptions, domain string) DHCPOptions {
	if len(opts.Search) == 0 && strings.TrimSpace(domain) != "" {
		opts.Search = []string{domain}
		if opts.SearchRaw == "" {
			opts.SearchRaw = domain
		}
	}
	return opts
}

func projectDHCPDefaults(meta ProjectMeta, domain string) DHCPOptions {
	var opts DHCPOptions
	if meta.DhcpSearch.Valid {
		raw := strings.TrimSpace(meta.DhcpSearch.String)
		opts.SearchRaw = raw
		opts.Search = parseCSV(raw)
	}
	if meta.DhcpLeaseTime.Valid {
		opts.LeaseTime = int(meta.DhcpLeaseTime.Int64)
	}
	if meta.DhcpRenewTime.Valid {
		opts.RenewTime = int(meta.DhcpRenewTime.Int64)
	}
	if meta.DhcpRebindTime.Valid {
		opts.RebindTime = int(meta.DhcpRebindTime.Int64)
	}
	if meta.DhcpBootFile.Valid {
		opts.BootFile = strings.TrimSpace(meta.DhcpBootFile.String)
	}
	if meta.DhcpNextServer.Valid {
		opts.NextServer = strings.TrimSpace(meta.DhcpNextServer.String)
	}
	if meta.DhcpVendorOpts.Valid {
		raw := strings.TrimSpace(meta.DhcpVendorOpts.String)
		opts.VendorOptionsRaw = raw
		opts.VendorOptions = parseLines(raw)
	}
	return normalizeDHCPOptions(opts, domain)
}

func buildDHCPBySite(sites []Site, defaults DHCPOptions, domain string) map[int64]DHCPOptions {
	out := make(map[int64]DHCPOptions, len(sites))
	for _, site := range sites {
		out[site.ID] = applySiteDHCPOverrides(defaults, site, domain)
	}
	return out
}

func buildSiteDefaults(sites []Site, meta ProjectMeta) map[int64]SiteDefaults {
	base := projectSiteDefaults(meta)
	out := make(map[int64]SiteDefaults, len(sites))
	for _, site := range sites {
		out[site.ID] = applySiteDefaults(base, site)
	}
	return out
}

func applySiteDHCPOverrides(base DHCPOptions, site Site, domain string) DHCPOptions {
	opts := base
	if site.DhcpSearch.Valid {
		raw := strings.TrimSpace(site.DhcpSearch.String)
		opts.SearchRaw = raw
		opts.Search = parseCSV(raw)
	}
	if site.DhcpLeaseTime.Valid {
		opts.LeaseTime = int(site.DhcpLeaseTime.Int64)
	}
	if site.DhcpRenewTime.Valid {
		opts.RenewTime = int(site.DhcpRenewTime.Int64)
	}
	if site.DhcpRebindTime.Valid {
		opts.RebindTime = int(site.DhcpRebindTime.Int64)
	}
	if site.DhcpBootFile.Valid {
		opts.BootFile = strings.TrimSpace(site.DhcpBootFile.String)
	}
	if site.DhcpNextServer.Valid {
		opts.NextServer = strings.TrimSpace(site.DhcpNextServer.String)
	}
	if site.DhcpVendorOpts.Valid {
		raw := strings.TrimSpace(site.DhcpVendorOpts.String)
		opts.VendorOptionsRaw = raw
		opts.VendorOptions = parseLines(raw)
	}
	return normalizeDHCPOptions(opts, domain)
}

func projectSiteDefaults(meta ProjectMeta) SiteDefaults {
	defaults := SiteDefaults{}
	if meta.DNS.Valid {
		defaults.DNS = parseList(meta.DNS)
	}
	if meta.NTP.Valid {
		defaults.NTP = parseList(meta.NTP)
	}
	if meta.GatewayPolicy.Valid {
		defaults.GatewayPolicy = strings.TrimSpace(meta.GatewayPolicy.String)
	}
	return defaults
}

func applySiteDefaults(base SiteDefaults, site Site) SiteDefaults {
	out := base
	if site.DNS.Valid {
		out.DNS = parseList(site.DNS)
	}
	if site.NTP.Valid {
		out.NTP = parseList(site.NTP)
	}
	if site.GatewayPolicy.Valid {
		out.GatewayPolicy = strings.TrimSpace(site.GatewayPolicy.String)
	}
	return out
}

func buildRenderSegments(opts GenerateOptions, views []SegmentView, sites []Site, domain string, dhcpBySite map[int64]DHCPOptions, siteDefaults map[int64]SiteDefaults) []renderSegment {
	siteMap := map[int64]Site{}
	for _, s := range sites {
		siteMap[s.ID] = s
	}

	out := make([]renderSegment, 0, len(views))
	for _, v := range views {
		if v.CIDR == "" {
			continue
		}
		if opts.SiteFilter != "" && opts.SiteFilter != v.Site {
			continue
		}
		if opts.VRFFilter != "" && opts.VRFFilter != v.VRF {
			continue
		}
		if opts.SegmentFilter != "" && !segmentFilterMatch(opts.SegmentFilter, v) {
			continue
		}
		p, err := netip.ParsePrefix(v.CIDR)
		if err != nil || !p.Addr().Is4() {
			continue
		}
		details, ok := prefixDetailsIPv4(p)
		if !ok {
			continue
		}
		gw := strings.TrimSpace(v.Gateway)
		if gw == "" {
			gw = details.FirstUsable
		}
		dhcpStart, dhcpEnd := dhcpRangeForTemplate(v, p, gw)
		dhcp := dhcpBySite[v.SiteID]
		defaults := siteDefaults[v.SiteID]
		out = append(out, renderSegment{
			Site:        v.Site,
			VRF:         v.VRF,
			VLAN:        v.VLAN,
			Name:        v.Name,
			Prefix:      p,
			PrefixBits:  p.Bits(),
			Network:     details.Network,
			Mask:        details.Mask,
			Gateway:     gw,
			DhcpEnabled: v.DhcpEnabled,
			DhcpStart:   dhcpStart,
			DhcpEnd:     dhcpEnd,
			DNS:         defaults.DNS,
			NTP:         defaults.NTP,
			Domain:      domain,
			DHCP:        dhcp,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Site != out[j].Site {
			return out[i].Site < out[j].Site
		}
		if out[i].VRF != out[j].VRF {
			return out[i].VRF < out[j].VRF
		}
		if out[i].VLAN != out[j].VLAN {
			return out[i].VLAN < out[j].VLAN
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func segmentFilterMatch(filter string, v SegmentView) bool {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return true
	}
	if filter == itoa64(v.ID) {
		return true
	}
	if strings.EqualFold(filter, v.Name) {
		return true
	}
	if strings.Contains(strings.ToLower(v.Name), strings.ToLower(filter)) {
		return true
	}
	return false
}

func parseList(value sql.NullString) []string {
	if !value.Valid {
		return nil
	}
	raw := strings.TrimSpace(value.String)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func buildMetadata(opts GenerateOptions, project Project, domain string, segments []renderSegment, defaults DHCPOptions, version string, source string) GenerateMetadata {
	name := project.Name
	if strings.TrimSpace(name) == "" {
		name = "Default"
	}
	options := map[string]string{
		"include_vrf":  boolToString(opts.IncludeVRF),
		"include_vlan": boolToString(opts.IncludeVLAN),
		"include_dhcp": boolToString(opts.IncludeDHCP),
	}
	if len(defaults.Search) > 0 {
		options["dhcp_search"] = strings.Join(defaults.Search, ", ")
	}
	if defaults.LeaseTime > 0 {
		options["dhcp_lease_time"] = itoa(defaults.LeaseTime)
	}
	if defaults.RenewTime > 0 {
		options["dhcp_renew_time"] = itoa(defaults.RenewTime)
	}
	if defaults.RebindTime > 0 {
		options["dhcp_rebind_time"] = itoa(defaults.RebindTime)
	}
	if defaults.BootFile != "" {
		options["dhcp_boot_file"] = defaults.BootFile
	}
	if defaults.NextServer != "" {
		options["dhcp_next_server"] = defaults.NextServer
	}
	if len(defaults.VendorOptions) > 0 {
		options["dhcp_vendor_options"] = strings.Join(defaults.VendorOptions, " | ")
	}

	filters := map[string]string{}
	if opts.SiteFilter != "" {
		filters["site"] = opts.SiteFilter
	}
	if opts.VRFFilter != "" {
		filters["vrf"] = opts.VRFFilter
	}
	if opts.SegmentFilter != "" {
		filters["segment"] = opts.SegmentFilter
	}

	sites := map[string]bool{}
	vrfs := map[string]bool{}
	vlans := map[int]bool{}
	dhcpCount := 0
	for _, s := range segments {
		sites[s.Site] = true
		if strings.TrimSpace(s.VRF) != "" {
			vrfs[s.VRF] = true
		}
		if s.VLAN > 0 {
			vlans[s.VLAN] = true
		}
		if s.DhcpEnabled {
			dhcpCount++
		}
	}

	return GenerateMetadata{
		GeneratedAt:     time.Now().UTC().Format(time.RFC3339),
		ProjectID:       project.ID,
		ProjectName:     name,
		Template:        opts.Template,
		TemplateVersion: version,
		TemplateSource:  source,
		Options:         options,
		Filters:         filters,
		DomainName:      domain,
		SegmentCount:    len(segments),
		SiteCount:       len(sites),
		VRFCount:        len(vrfs),
		VLANCount:       len(vlans),
		DHCPCount:       dhcpCount,
	}
}

func groupSegments(segments []renderSegment) []segmentGroup {
	if len(segments) == 0 {
		return nil
	}
	var groups []segmentGroup
	cur := segmentGroup{Site: segments[0].Site, VRF: segments[0].VRF}
	seenVLAN := map[int]bool{}
	for _, s := range segments {
		if s.Site != cur.Site || s.VRF != cur.VRF {
			groups = append(groups, cur)
			cur = segmentGroup{Site: s.Site, VRF: s.VRF}
			seenVLAN = map[int]bool{}
		}
		cur.Segments = append(cur.Segments, s)
		if !seenVLAN[s.VLAN] {
			cur.VLANs = append(cur.VLANs, renderVLAN{
				VLAN:       s.VLAN,
				Name:       s.Name,
				Gateway:    s.Gateway,
				Mask:       s.Mask,
				PrefixBits: s.PrefixBits,
			})
			seenVLAN[s.VLAN] = true
		}
	}
	groups = append(groups, cur)
	return groups
}

func metadataHeader(meta GenerateMetadata, prefix string) string {
	lines := []string{
		fmt.Sprintf("%s subnetio bundle", prefix),
		fmt.Sprintf("%s generated_at: %s", prefix, meta.GeneratedAt),
		fmt.Sprintf("%s project: %s", prefix, meta.ProjectName),
		fmt.Sprintf("%s template: %s", prefix, meta.Template),
	}
	if meta.TemplateVersion != "" {
		lines = append(lines, fmt.Sprintf("%s template_version: %s", prefix, meta.TemplateVersion))
	}
	if meta.TemplateSource != "" {
		lines = append(lines, fmt.Sprintf("%s template_source: %s", prefix, meta.TemplateSource))
	}
	lines = append(lines, fmt.Sprintf("%s segments: %d", prefix, meta.SegmentCount))
	lines = append(lines, fmt.Sprintf("%s sites: %d", prefix, meta.SiteCount))
	lines = append(lines, fmt.Sprintf("%s vrfs: %d", prefix, meta.VRFCount))
	lines = append(lines, fmt.Sprintf("%s vlans: %d", prefix, meta.VLANCount))
	lines = append(lines, fmt.Sprintf("%s dhcp_scopes: %d", prefix, meta.DHCPCount))
	if len(meta.Options) > 0 {
		keys := make([]string, 0, len(meta.Options))
		for k := range meta.Options {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			lines = append(lines, fmt.Sprintf("%s option.%s=%s", prefix, k, meta.Options[k]))
		}
	}
	if len(meta.Filters) > 0 {
		keys := make([]string, 0, len(meta.Filters))
		for k := range meta.Filters {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			lines = append(lines, fmt.Sprintf("%s filter.%s=%s", prefix, k, meta.Filters[k]))
		}
	}
	if meta.DomainName != "" {
		lines = append(lines, fmt.Sprintf("%s domain: %s", prefix, meta.DomainName))
	}
	return strings.Join(lines, "\n") + "\n\n"
}

func encodeMetadataJSON(meta GenerateMetadata) ([]byte, error) {
	return json.MarshalIndent(meta, "", "  ")
}

func dhcpRangeForTemplate(v SegmentView, p netip.Prefix, gw string) (string, string) {
	if !v.DhcpEnabled {
		return "", ""
	}
	if v.Segment.DhcpRange.Valid {
		raw := strings.TrimSpace(v.Segment.DhcpRange.String)
		if raw != "" {
			start, end := splitRange(raw)
			if start != "" && end != "" {
				return start, end
			}
		}
	}
	start, end := autoDhcpRangeFromPrefix(p, gw)
	return start, end
}

func splitRange(raw string) (string, string) {
	clean := strings.ReplaceAll(raw, "–", "-")
	if strings.Contains(clean, "-") {
		parts := strings.SplitN(clean, "-", 2)
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	if strings.Contains(clean, ",") {
		parts := strings.SplitN(clean, ",", 2)
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	fields := strings.Fields(clean)
	if len(fields) >= 2 {
		return strings.TrimSpace(fields[0]), strings.TrimSpace(fields[1])
	}
	return "", ""
}

func autoDhcpRangeFromPrefix(p netip.Prefix, gw string) (string, string) {
	details, ok := prefixDetailsIPv4(p)
	if !ok {
		return "", ""
	}
	start := details.FirstUsable
	end := details.LastUsable
	if start == "" || end == "" {
		return "", ""
	}
	if gw != "" {
		if gw == start {
			addr, err := netip.ParseAddr(start)
			if err == nil && addr.Is4() {
				next := u32ToIPv4(ipv4ToU32(addr) + 1)
				start = next.String()
			}
		}
	}
	return start, end
}

func safeName(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, " ", "-")
	value = strings.ReplaceAll(value, "/", "-")
	value = strings.ReplaceAll(value, "_", "-")
	value = strings.ReplaceAll(value, "--", "-")
	return value
}

func groupLabel(site, vrf string) string {
	vrf = strings.TrimSpace(vrf)
	if vrf == "" {
		return site + " / VRF default"
	}
	return site + " / VRF " + vrf
}

func boolToString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func templateExtension(_ string) string {
	return "txt"
}

func templateExample(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if example, ok := templateExamples[name]; ok {
		return example
	}
	return templateExamples["vyos"]
}

func templateCommentPrefix(name string) string {
	if prefix, ok := templateCommentPrefixes[name]; ok {
		return prefix
	}
	return "#"
}

var templateNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

func normalizeTemplateName(raw string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(raw))
	if name == "" {
		return "", errors.New("template is required")
	}
	if !templateNamePattern.MatchString(name) {
		return "", errors.New("invalid template name")
	}
	return name, nil
}

func loadTemplateSource(name string) (templateSource, error) {
	customPath := filepath.Join(customTemplateDir, name+".tmpl")
	if data, err := os.ReadFile(customPath); err == nil {
		version := "custom-" + shortHash(data)
		return templateSource{Content: string(data), Version: version, Source: "override"}, nil
	} else if !os.IsNotExist(err) {
		return templateSource{}, err
	}

	data, err := genTemplateFS.ReadFile("templates/" + name + ".tmpl")
	if err != nil {
		return templateSource{}, errors.New("unknown template")
	}
	version := defaultTemplateVersions[name]
	if version == "" {
		version = "v1"
	}
	return templateSource{Content: string(data), Version: version, Source: "embedded"}, nil
}

func listTemplateCatalog() []TemplateInfo {
	names := map[string]bool{}
	for name := range defaultTemplateVersions {
		names[name] = true
	}
	if entries, err := os.ReadDir(customTemplateDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := strings.TrimSuffix(entry.Name(), ".tmpl")
			if name != entry.Name() && name != "" {
				names[name] = true
			}
		}
	}
	out := make([]TemplateInfo, 0, len(names))
	for name := range names {
		source, err := loadTemplateSource(name)
		if err != nil {
			continue
		}
		out = append(out, TemplateInfo{
			Name:    name,
			Version: source.Version,
			Source:  source.Source,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func renderTemplate(name, body string, ctx TemplateContext) (string, error) {
	tmpl, err := template.New(name).Funcs(templateFuncs()).Parse(body)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}

func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"itoa":              itoa,
		"safeName":          safeName,
		"groupLabel":        groupLabel,
		"join":              strings.Join,
		"trim":              strings.TrimSpace,
		"quoteList":         quoteList,
		"ciscoLease":        formatCiscoLease,
		"ciscoDomainSearch": formatCiscoDomainSearch,
		"firstVLAN":         firstVLAN,
		"mikrotikDhcpLine":  mikrotikDhcpLine,
	}
}

func firstVLAN(vlans []renderVLAN) int {
	if len(vlans) == 0 {
		return 0
	}
	return vlans[0].VLAN
}

func mikrotikDhcpLine(s renderSegment, opts DHCPOptions) string {
	line := fmt.Sprintf("/ip dhcp-server network add address=%s/%d gateway=%s", s.Network, s.PrefixBits, s.Gateway)
	if len(s.DNS) > 0 {
		line += " dns-server=" + strings.Join(s.DNS, ",")
	}
	if s.Domain != "" {
		line += " domain=" + s.Domain
	}
	if len(s.NTP) > 0 {
		line += " ntp-server=" + strings.Join(s.NTP, ",")
	}
	if opts.NextServer != "" {
		line += " next-server=" + opts.NextServer
	}
	if opts.BootFile != "" {
		line += " boot-file-name=" + opts.BootFile
	}
	if opts.LeaseTime > 0 {
		line += " lease-time=" + itoa(opts.LeaseTime) + "s"
	}
	return line
}

func formatCiscoLease(seconds int) string {
	if seconds <= 0 {
		return ""
	}
	minutes := (seconds + 59) / 60
	days := minutes / (24 * 60)
	minutes -= days * 24 * 60
	hours := minutes / 60
	minutes -= hours * 60
	if days == 0 && hours == 0 && minutes == 0 {
		minutes = 1
	}
	return fmt.Sprintf("%d %d %d", days, hours, minutes)
}

func formatCiscoDomainSearch(search []string) string {
	if len(search) == 0 {
		return ""
	}
	return strings.Join(search, " ")
}

func quoteList(items []string, sep string) string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		out = append(out, strconv.Quote(item))
	}
	return strings.Join(out, sep)
}

func parseCSV(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func parseLines(raw string) []string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

func shortHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:4])
}

func checksumSHA256(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func unifiedDiff(fullScope, scoped string) string {
	left := splitLines(fullScope)
	right := splitLines(scoped)
	if len(left) == 0 && len(right) == 0 {
		return ""
	}

	dp := make([][]int, len(left)+1)
	for i := range dp {
		dp[i] = make([]int, len(right)+1)
	}
	for i := len(left) - 1; i >= 0; i-- {
		for j := len(right) - 1; j >= 0; j-- {
			if left[i] == right[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	type diffLine struct {
		prefix string
		text   string
	}
	var lines []diffLine
	i, j := 0, 0
	for i < len(left) && j < len(right) {
		if left[i] == right[j] {
			lines = append(lines, diffLine{prefix: " ", text: left[i]})
			i++
			j++
			continue
		}
		if dp[i+1][j] >= dp[i][j+1] {
			lines = append(lines, diffLine{prefix: "-", text: left[i]})
			i++
		} else {
			lines = append(lines, diffLine{prefix: "+", text: right[j]})
			j++
		}
	}
	for i < len(left) {
		lines = append(lines, diffLine{prefix: "-", text: left[i]})
		i++
	}
	for j < len(right) {
		lines = append(lines, diffLine{prefix: "+", text: right[j]})
		j++
	}

	hasChanges := false
	for _, line := range lines {
		if line.prefix != " " {
			hasChanges = true
			break
		}
	}
	if !hasChanges {
		return ""
	}

	var b strings.Builder
	b.WriteString("--- full-scope\n")
	b.WriteString("+++ filtered-scope\n")
	for _, line := range lines {
		b.WriteString(line.prefix)
		b.WriteString(line.text)
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}

func splitLines(raw string) []string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	raw = strings.TrimSuffix(raw, "\n")
	if raw == "" {
		return nil
	}
	return strings.Split(raw, "\n")
}
