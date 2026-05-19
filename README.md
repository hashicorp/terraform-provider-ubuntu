# Terraform Provider: Ubuntu

This repository contains the ubuntu provider.

**Status:** This provider is in alpha and is not officially supported.

Issues, pull requests, behavior changes, docs generation changes, and release logic changes all belong upstream.

## What This Provider Does

The ubuntu provider lets Terraform manage operating-system state on a remote ubuntu host over SSH. You can declare the host configuration you want Terraform to maintain and then plan and apply those changes the same way you do with the rest of your infrastructure.

It is primarily aimed at systems configuration on the host itself: packages, service configuration, files, trust material, scheduled jobs, users, firewall rules, and other long-lived operating-system settings.

Typical use cases include:

- installing and removing packages
- writing or reconciling files on the host
- enabling and restarting services
- managing users, groups, cron, certificates, firewall rules, and other host-level configuration

Repeated applies converge the machine back toward the state declared in code, while `terraform plan` gives you a preview of the changes before they are made.

## How To Use It

1. Configure SSH access for the target host in the provider.
2. Set a `default_target` or pass `target` on each resource.
3. Declare the host state you want to manage.
4. Run `terraform plan` and `terraform apply` to reconcile the host.

## Simple Examples

### Example 1: Connect to a host and install a package

```hcl
terraform {
	required_providers {
		ubuntu = {
			source  = "hashicorp/ubuntu"
			version = "~> 0.1"
		}
	}
}

provider "ubuntu" {
	ssh {
		user             = "ubuntu"
		private_key      = var.ssh_private_key_pem
		known_hosts_file = var.ssh_known_hosts_path
	}

	default_target {
		target = var.host_address
		port   = 22
	}
}

resource "ubuntu_package" "nginx" {
	name         = "nginx"
	update_cache = true
}
```

If you use Vault's SSH secrets engine, the provider fits the Signed SSH Certificates flow described in the Vault docs: the runner supplies the keypair, Vault signs the public key, and the target host trusts the signer through `TrustedUserCAKeys`.

```hcl
data "vault_ssh_secret_backend_sign" "runner_ssh" {
	path             = "ssh-client-signer"
	name             = "ubuntu"
	public_key       = var.ssh_public_key_openssh
	valid_principals = "ubuntu"
}

provider "ubuntu" {
	ssh {
		user             = "ubuntu"
		private_key      = var.ssh_private_key_pem
		certificate      = data.vault_ssh_secret_backend_sign.runner_ssh.signed_key
		known_hosts_file = var.ssh_known_hosts_path
	}
}
```

In HCP Terraform, `ssh_private_key_pem` is typically a sensitive workspace variable or a sensitive `TF_VAR_ssh_private_key_pem` environment variable, while `ssh_public_key_openssh` can come from a non-sensitive workspace variable or an upstream workspace output. Vault returns the signed SSH certificate through `signed_key`, and the provider uses that certificate together with the matching private key during authentication.

### Example 2: Configure nginx as a routing service

```hcl
resource "ubuntu_user" "nginx_runtime" {
	name    = "edge-router"
	system  = true
	home    = "/nonexistent"
	create_home = false
	shell   = "/usr/sbin/nologin"
	comment = "nginx routing service account"
}

resource "ubuntu_file" "nginx_main" {
	path    = "/etc/nginx/nginx.conf"
	content = <<-EOT
		user edge-router;
		worker_processes auto;
		pid /run/nginx.pid;

		events {
		  worker_connections 1024;
		}

		http {
		  include /etc/nginx/mime.types;
		  default_type application/octet-stream;
		  sendfile on;
		  keepalive_timeout 65;

		  include /etc/nginx/conf.d/*.conf;
		}
	EOT
	owner   = "root"
	group   = "root"
	mode    = "0644"

	depends_on = [
		ubuntu_package.nginx,
		ubuntu_user.nginx_runtime,
	]
}

resource "ubuntu_file" "nginx_route" {
	path    = "/etc/nginx/conf.d/internal-api.conf"
	content = <<-EOT
		server {
		  listen 80;
		  server_name _;

		  location /api/ {
		    proxy_pass         http://10.0.2.15:8080;
		    proxy_http_version 1.1;
		    proxy_set_header   Host $host;
		    proxy_set_header   X-Real-IP $remote_addr;
		    proxy_set_header   X-Forwarded-For $proxy_add_x_forwarded_for;
		    proxy_set_header   X-Forwarded-Proto $scheme;
		  }
		}
	EOT
	owner   = "root"
	group   = "root"
	mode    = "0644"

	depends_on = [ubuntu_file.nginx_main]
}

resource "ubuntu_systemd_unit" "nginx" {
	name             = "nginx"
	enabled          = true
	state            = "running"
	reload_on_change = true
	reload_triggers  = [
		ubuntu_file.nginx_main.digest,
		ubuntu_file.nginx_route.digest,
	]
}
```

### Example 3: Reboot a host when a change needs a fresh boot

```hcl
action "ubuntu_restart_host" "routing_host_reboot" {
	config {
		name            = "routing_host_reboot"
		reason          = "Apply routing host changes that require a reboot"
		timeout_seconds = 600
		settle_seconds  = 15
	}
}

resource "terraform_data" "routing_host_reboot" {
	input = join(":", [
		ubuntu_file.nginx_main.digest,
		ubuntu_file.nginx_route.digest,
	])

	lifecycle {
		action_trigger {
			events  = [after_create, after_update]
			actions = [action.ubuntu_restart_host.routing_host_reboot]
		}
	}
}
```

## Start Here

- Read [docs/index.md](docs/index.md) for the provider docs.
- Use the examples above as a starting point for your first configuration.
- Review [RELEASE_NOTES.md](RELEASE_NOTES.md) for the staged release notes that accompany this version.

## Build

```sh
go build .
```

## Install

```hcl
terraform {
  required_providers {
    ubuntu = {
      source  = "hashicorp/ubuntu"
      version = "~> 0.1"
    }
  }
}
```

## Contents

- Provider documentation: [docs/](docs/)
- Staged release notes: [RELEASE_NOTES.md](RELEASE_NOTES.md)