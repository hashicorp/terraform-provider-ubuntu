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

## Architecture and Design
The ubuntu provider is built on top of the Terraform Plugin Framework and uses the Go SSH library to connect to the target host. It executes commands and manages files over SSH to enforce the desired state declared in Terraform configurations. 



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
		user             = "terraform"
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

If you use Vault's SSH secrets engine, the best current pattern is to let Vault issue a short-lived keypair and signed certificate at runtime, then pass both into the provider as ephemeral values so they stay out of Terraform state and plan files. The target host still trusts the signer through `TrustedUserCAKeys`, and SSH certificate auth still uses a private key, but Vault can issue that keypair just for the run instead of requiring you to store a long-lived PEM in Terraform variables or KV.

```hcl
provider "vault" {}

ephemeral "vault_generic_endpoint" "runner_ssh" {
	path      = "ssh-client-signer/issue/terraform"
	data_json = jsonencode({
		valid_principals = "terraform"
	})
	write_fields = ["private_key", "signed_key"]
}

provider "ubuntu" {
	ssh {
		user             = "terraform"
		private_key      = ephemeral.vault_generic_endpoint.runner_ssh.write_data["private_key"]
		certificate      = ephemeral.vault_generic_endpoint.runner_ssh.write_data["signed_key"]
		known_hosts_file = var.ssh_known_hosts_path
	}
}
```

In HCP Terraform, that usually means authenticating the Vault provider with workspace variables or an `auth_login` block, letting Vault issue the SSH material just in time, and giving the Linux provider the returned `private_key` and `signed_key` as ephemeral values. That keeps the SSH credential out of Terraform state while still using the standard OpenSSH signed-certificate flow.

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

### Example 3: Reboot a host on demand

```hcl
action "ubuntu_restart_host" "maintenance_reboot" {
	config {
		name            = "maintenance_reboot"
		reason          = "Apply maintenance changes that require a reboot"
		timeout_seconds = 600
		settle_seconds  = 15
	}
}
```

Invoke it as a one-shot action when you actually want the reboot:

```bash
terraform apply -invoke='action.ubuntu_restart_host.maintenance_reboot'
```

Terraform plans and runs only that action when `-invoke` is set, which keeps reboots separate from the normal converge loop.

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