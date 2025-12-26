# 🌐 Subnetio - IP Plan, VLSM, and Config Generator

[![Go Version](https://img.shields.io/badge/Go-1.21+-blue.svg)](https://golang.org/)
[![Docker](https://img.shields.io/badge/Docker-Ready-blue.svg)](https://www.docker.com/)
[![License: GPLv3](https://img.shields.io/badge/License-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)
[![GitHub stars](https://img.shields.io/github/stars/ashimov/Subnetio?style=social)](https://github.com/ashimov/Subnetio)
[![GitHub issues](https://img.shields.io/github/issues/ashimov/Subnetio)](https://github.com/ashimov/Subnetio/issues)
[![GitHub forks](https://img.shields.io/github/forks/ashimov/Subnetio?style=social)](https://github.com/ashimov/Subnetio)

> A lightweight Go web application for IP network planning with VLSM allocation, IPv4/IPv6 requests, and deterministic config generation for network platforms. Built with ❤️ for the community.

## 📋 Table of Contents

- [✨ Features](#-features)
- [🚀 Quick Start (Docker)](#-quick-start-docker)
- [🐳 Docker Hub Images](#-docker-hub-images)
- [💻 Local Installation](#-local-installation)
- [🖥️ Usage (Web UI)](#️-usage-web-ui)
- [📥 Import and Export](#-import-and-export)
- [📊 Audit Trail](#-audit-trail)
- [🎨 Templates and Customization](#-templates-and-customization)
- [🧪 Testing](#-testing)
- [🤝 Contributing](#-contributing)
- [📈 Roadmap](#-roadmap)
- [📸 Screenshots](#-screenshots)
- [🙏 Acknowledgments](#-acknowledgments)
- [📄 License](#-license)

## ✨ Features

| Feature | Description |
|---------|-------------|
| 🏗️ **Project Management** | Organize network planning into projects with sites, VRF/VLAN, pools, and segments for IPv4/IPv6. |
| 🔄 **VLSM Auto-Allocation** | Automatically allocate subnets with locked subnets and reserved ranges support. |
| ✅ **Validation & Hints** | Detect overlaps, out-of-pool, VLAN duplications, oversized requests, and fragmentation. |
| 🔮 **What-If Simulation** | Preview changes with diff views without database commits. |
| 📈 **Capacity Planning** | Dashboard with growth forecasts and IPv6 unit sizing. |
| 📝 **Template Generation** | Generate configs for VyOS, Cisco, JunOS, Mikrotik with grouping, ordering, filters, and diff options. |
| 📦 **Deployed Baselines** | Snapshot deployed states and compare against generated outputs. |
| ⬇️ **Download Bundles** | Export configs as ZIP with metadata and checksums. |
| 🌐 **DHCP Options** | Configure router, DNS, NTP, domain, search, lease times, PXE/boot, vendor options. |
| 🔄 **Plan Import/Export** | Support CSV/YAML/JSON with stable IDs for clean diffs. |
| 📋 **Audit Trail Export** | Export change logs in CSV/JSON with before/after snapshots. |
| 📊 **XLSX Export** | Export data for Sites, Segments, DHCP, and Conflicts.

## Quick Start (Docker)

1. Create a data directory:
   ```bash
   mkdir -p data
   ```

2. Start the application using Docker Compose:
   ```bash
   docker compose up --build
   ```

3. Open your browser and navigate to:
   - [http://localhost:8080/projects](http://localhost:8080/projects)

Data is persisted to `./data/subnetio.sqlite` via a Docker volume.

## Docker Hub Images

Prebuilt images are published to Docker Hub:

- `docker.io/<your-dockerhub-username>/subnetio`
- Tags: `latest`, `vX.Y.Z`

Example:

```bash
docker pull docker.io/<your-dockerhub-username>/subnetio:latest
docker run --rm -p 8080:8080 \
  -e DB_PATH=/data/subnetio.sqlite \
  -e LISTEN_ADDR=0.0.0.0:8080 \
  -v "$(pwd)/data:/data" \
  docker.io/<your-dockerhub-username>/subnetio:latest
```

## Local Installation

1. Ensure you have Go 1.21+ installed.

2. Clone the repository and navigate to the project directory.

3. Install dependencies:
   ```bash
   go mod tidy
   ```

4. Run the application:
   ```bash
   go run ./cmd/subnetio
   ```

### Environment Variables

- `DB_PATH`: Path to the SQLite database file (default: `./subnetio.sqlite`)
- `LISTEN_ADDR`: Address and port to listen on (default: `0.0.0.0:8080`)

## Usage (Web UI)

1. **Create or Select a Project**: Start on the Projects page to create a new project or select an existing one.

2. **Add Sites and Pools**: On the Sites page, add sites and define IPv4 or IPv6 pools with optional tier/priority settings.

3. **Define Segments**: On the Segments page, create segments by specifying the number of hosts or prefix lengths for IPv4 and IPv6.
   - Use the "Locked" option for subnets that are already deployed and should not be moved.

4. **Auto-Allocate Subnets**: Click the "Auto-allocate (VLSM)" button to assign CIDR blocks to segments.

5. **Review Conflicts**: Check for any conflicts and adjust project rules as necessary.

6. **Generate Configurations**: Use the Generate page to preview configurations, apply filters by Site/VRF/Segment, and download outputs.
   - Save a deployed baseline to enable diff comparisons against the current deployed state.
   - Download bundles (ZIP) containing configurations and metadata.json files.

7. **Capacity Planning**: Visit the Planning page for capacity forecasts and growth projections.

8. **Export Audit History**: On the Export page, export audit logs for a complete change history.

## Import and Export

- **Export Plan**: Use the Export page to export plans in CSV, YAML, or JSON formats.
- **Export Audit**: Export audit trails in CSV or JSON formats from the Export page.
- **Import Plan**: Import plans via the Projects page using CSV, YAML, or JSON files.
- **Sample Dataset**: Refer to `data/sample.csv` for an example import file.

Plan bundles are designed to be deterministic, ensuring clean diffs through stable IDs and ordered rows.

## Audit Trail

Audit entries are automatically created for key actions such as create, update, delete operations, allocations, and imports. To tag the actor performing actions, include the `X-Actor` header or an `actor` query parameter in requests.

## Templates and Customization

- **Built-in Templates**: Located in `cmd/subnetio/templates/*.tmpl`.
- **Custom Overrides**: Place custom templates in `data/templates/<name>.tmpl` to override built-in ones.
- **Upload Templates**: Use the Templates page to upload or paste custom template content.
- **Documentation**: See `docs/templates.md` for detailed information on template helpers, context, and examples.

## Testing

Run the test suite to ensure everything is working correctly:

```bash
go test ./...
```

## 🤝 Contributing

We love contributions! Here's how you can help:

1. 🍴 Fork the repository
2. 🌿 Create a feature branch (`git checkout -b feature/amazing-feature`)
3. 💻 Make your changes and add tests
4. ✅ Commit your changes (`git commit -m 'Add amazing feature'`)
5. 📤 Push to the branch (`git push origin feature/amazing-feature`)
6. 🔄 Open a Pull Request

### Development Setup

```bash
git clone https://github.com/ashimov/Subnetio.git
cd Subnetio
go mod tidy
go run ./cmd/subnetio
```

For more details, see our [Contributing Guide](CONTRIBUTING.md).

## 📈 Roadmap

- [ ] 🌍 Multi-language support (i18n)
- [ ] 📱 Mobile-responsive UI improvements
- [ ] 🔐 User authentication and role-based access
- [ ] 📊 Advanced analytics and reporting
- [ ] 🔗 API endpoints for integrations
- [ ] ☁️ Cloud deployment options
- [ ] 🤖 AI-assisted subnet optimization

## 📸 Screenshots

*Coming soon! Screenshots will showcase the intuitive web interface for project management, subnet allocation, and configuration generation.*

## 🙏 Acknowledgments

- Built with [Go](https://golang.org/) - The awesome programming language
- UI powered by [HTMX](https://htmx.org/) and [Bootstrap](https://getbootstrap.com/)
- Inspired by network engineers worldwide

## 📄 License

This project is licensed under the GNU GPL v3.0. See the [LICENSE](LICENSE) file for more details.

---

Made with ❤️ for Community
