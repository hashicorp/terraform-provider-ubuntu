# Terraform Ubuntu Provider

This repository contains the ubuntu provider.

> [!WARNING]
> This provider is in alpha and is not officially supported.

Issues, pull requests, behavior changes, docs generation changes, and release logic changes all belong upstream.

## What This Provider Does

The Ubuntu provider lets Terraform manage operating-system state on a remote Ubuntu host over SSH. You can declare the host configuration you want Terraform to maintain and then plan and apply those changes the same way you do with the rest of your infrastructure.

It is primarily aimed at systems configuration on the host itself: packages, service configuration, files, trust material, scheduled jobs, users, firewall rules, and other long-lived operating-system settings.

Typical use cases include:

- installing and removing packages
- writing or reconciling files on the host
- enabling and restarting services
- managing users, groups, cron, certificates, firewall rules, and other host-level configuration

## Architecture and Design
The Ubuntu provider is built on top of the Terraform Plugin Framework and uses SSH to connect to the target host(s). It executes commands and manages files to enforce the desired state declared in Terraform configurations. 

It pushes a small, machine-compiled binary to the target host, which then executes operations necessary to enforce the desired state. The executor is removed as soon as the operations are complete. No host tools or agents are required beyond SSH.

The provider can manage many host connections in parallel, and it is designed to be resilient to transient SSH failures and host reboots.

It provides post-quantum safety capabilities, even if the underlying SSH connection cannot. See provider configuration for details.

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
    user        = "terraform"
    private_key = var.ssh_private_key_pem
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

If you use Vault's SSH secrets engine, let Vault issue a short-lived keypair and signed certificate at runtime, then pass both into the provider as ephemeral values so they stay out of Terraform state and plan files.

```hcl
provider "vault" {}

ephemeral "vault_generic_endpoint" "runner_ssh" {
  path         = "ssh-client-signer/issue/terraform"
  write_fields = ["private_key", "signed_key"]
}

provider "ubuntu" {
  ssh {
    user        = "terraform"
    private_key = ephemeral.vault_generic_endpoint.runner_ssh.write_data["private_key"]
    certificate = ephemeral.vault_generic_endpoint.runner_ssh.write_data["signed_key"]
  }
}
```

### Example 2: Configure nginx as a routing service

```hcl
resource "ubuntu_user" "nginx_runtime" {
  name        = "edge-router"
  system      = true
  home        = "/nonexistent"
  create_home = false
  shell       = "/usr/sbin/nologin"
  comment     = "nginx routing service account"
}

resource "ubuntu_file" "nginx_main" {
  path    = "/etc/nginx/nginx.conf"
  content = templatefile("${path.module}/templates/nginx.conf.tftpl", {
    runtime_user = ubuntu_user.nginx_runtime.name
  })
  owner   = "root"
  group   = "root"
  mode    = "0644"

  depends_on = [ubuntu_package.nginx]
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
}

resource "ubuntu_systemd_unit" "nginx" {
  name             = ubuntu_package.nginx.name
  enabled          = true
  state            = "running"
  reload_on_change = true
  reload_triggers = [
    ubuntu_file.nginx_main.digest,
    ubuntu_file.nginx_route.digest,
  ]
}
```

Template file at `templates/nginx.conf.tftpl`:

```nginx
user ${runtime_user};
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