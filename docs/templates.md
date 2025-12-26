# Template Generation Documentation

## Template Locations

- **Built-in Templates**: Located in `cmd/subnetio/templates/*.tmpl`
- **Custom Overrides**: Place custom templates in `data/templates/<name>.tmpl` to completely replace built-in templates.

## Available Templates

- `vyos`
- `cisco`
- `juniper`
- `mikrotik`

## Template Context

- `.Header` — Pre-formatted comment with metadata (can be placed at the beginning).
- `.Meta` — Generation metadata (project/template/filters/counts).
- `.Options` — Generation flags (IncludeVRF/IncludeVLAN/IncludeDHCP + filters).
- `.Defaults` — Project DHCP defaults.
- `.Groups` — Segments grouped by Site+VRF.
- `.Segments` — Flat list of segments (filtered and sorted).

### SegmentGroup

- `.Site` (string)
- `.VRF` (string)
- `.VLANs` ([]renderVLAN)
- `.Segments` ([]renderSegment)

### renderVLAN

- `.VLAN` (int)
- `.Name` (string)
- `.Gateway` (string)
- `.Mask` (string)
- `.PrefixBits` (int)

### renderSegment

- `.Site` (string)
- `.VRF` (string)
- `.VLAN` (int)
- `.Name` (string)
- `.Prefix` (netip.Prefix)
- `.PrefixBits` (int)
- `.Network` (string)
- `.Mask` (string)
- `.Gateway` (string)
- `.DhcpEnabled` (bool)
- `.DhcpStart` (string)
- `.DhcpEnd` (string)
- `.DNS` ([]string)
- `.NTP` ([]string)
- `.Domain` (string)
- `.DHCP` (DHCPOptions, final settings for the site)

### DHCPOptions

- `.Search` ([]string)
- `.LeaseTime` (int, seconds)
- `.RenewTime` (int, seconds)
- `.RebindTime` (int, seconds)
- `.BootFile` (string)
- `.NextServer` (string)
- `.VendorOptions` ([]string, inserted as raw strings)

## Template Helpers

- `itoa` — Convert int to string
- `safeName` — Normalize string for names
- `groupLabel` — Human-readable label for Site/VRF
- `join` — `strings.Join`
- `trim` — `strings.TrimSpace`
- `quoteList` — List with quotes, e.g., `quoteList .Search " "`
- `ciscoLease` — Convert seconds to Cisco `lease` format
- `ciscoDomainSearch` — Format option 119 for Cisco
- `firstVLAN` — First VLAN in the group
- `mikrotikDhcpLine` — DHCP line for Mikrotik

## Example Template

```tmpl
{{.Header}}{{range $gi, $g := .Groups}}{{if $gi}}

{{end}}# Site {{groupLabel $g.Site $g.VRF}}
{{- if $.Options.IncludeVLAN}}
{{- range $g.VLANs}}
set interfaces vlan vlan{{.VLAN}} address {{.Gateway}}/{{.PrefixBits}}
{{- end}}
{{- end}}
{{- if $.Options.IncludeDHCP}}
{{- range $g.Segments}}
{{- if .DhcpEnabled}}
{{- $dhcp := .DHCP -}}
set service dhcp-server shared-network-name {{safeName .Name}} subnet {{.Network}}/{{.PrefixBits}} default-router {{.Gateway}}
{{- if $dhcp.Search}}
set service dhcp-server shared-network-name {{safeName .Name}} subnet {{.Network}}/{{.PrefixBits}} domain-search {{quoteList $dhcp.Search " "}}
{{- end}}
{{- end}}
{{- end}}
{{- end}}
{{end}}
```

## Notes

- DHCP defaults are taken from the project and can be overridden at the site level.
- Custom templates in `data/templates` automatically receive a version `custom-<hash>` in metadata.
