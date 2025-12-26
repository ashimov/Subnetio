-- Copyright (c) 2025 Berik Ashimov

ALTER TABLE site_meta ADD COLUMN dhcp_search TEXT;
ALTER TABLE site_meta ADD COLUMN dhcp_lease_time INTEGER;
ALTER TABLE site_meta ADD COLUMN dhcp_renew_time INTEGER;
ALTER TABLE site_meta ADD COLUMN dhcp_rebind_time INTEGER;
ALTER TABLE site_meta ADD COLUMN dhcp_boot_file TEXT;
ALTER TABLE site_meta ADD COLUMN dhcp_next_server TEXT;
ALTER TABLE site_meta ADD COLUMN dhcp_vendor_options TEXT;

ALTER TABLE project_meta ADD COLUMN domain_name TEXT;
ALTER TABLE project_meta ADD COLUMN dns TEXT;
ALTER TABLE project_meta ADD COLUMN ntp TEXT;
ALTER TABLE project_meta ADD COLUMN gateway_policy TEXT;
ALTER TABLE project_meta ADD COLUMN dhcp_search TEXT;
ALTER TABLE project_meta ADD COLUMN dhcp_lease_time INTEGER;
ALTER TABLE project_meta ADD COLUMN dhcp_renew_time INTEGER;
ALTER TABLE project_meta ADD COLUMN dhcp_rebind_time INTEGER;
ALTER TABLE project_meta ADD COLUMN dhcp_boot_file TEXT;
ALTER TABLE project_meta ADD COLUMN dhcp_next_server TEXT;
ALTER TABLE project_meta ADD COLUMN dhcp_vendor_options TEXT;
