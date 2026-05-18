# Terraform Provider: Ubuntu

This repository is a generated, buildable release repo for the ubuntu provider. 

Issues, pull requests, behavior changes, docs generation changes, and release logic changes all belong upstream.

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

- Generated provider entrypoint: [main.go](main.go)
- Shared provider support packages: [providers/shared/](providers/shared/)
- Generated embedded runtime assets: [generated/providers/ubuntu/runtimeassets/](generated/providers/ubuntu/runtimeassets/)
- Generated Registry docs: [docs/](docs/)
- Staged release notes: [RELEASE_NOTES.md](RELEASE_NOTES.md)
- Upstream contributor guide: [https://github.com/jeremymefford/terraform-provider-linux/blob/main/CONTRIBUTING.md](https://github.com/jeremymefford/terraform-provider-linux/blob/main/CONTRIBUTING.md)
