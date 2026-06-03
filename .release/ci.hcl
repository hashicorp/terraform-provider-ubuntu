schema = "1"

project "terraform-provider-ubuntu" {
  team = "terraform-provider-linux"

  github {
    organization = "hashicorp"
    repository = "terraform-provider-ubuntu"
    release_branches = [
      "main",
      "release/**",
    ]
  }
}

event "merge" {
  # Entry point used by crt-orchestrator when the downstream source PR merges.
}

event "build" {
  depends = ["merge"]

  action "build" {
    organization = "hashicorp"
    repository = "terraform-provider-ubuntu"
    workflow = "build"
  }
}

event "upload-dev" {
  depends = ["build"]

  action "upload-dev" {
    organization = "hashicorp"
    repository = "crt-workflows-common"
    workflow = "upload-dev"
    depends = ["build"]
  }

  notification {
    on = "fail"
  }
}

event "security-scan-binaries" {
  depends = ["upload-dev"]

  action "security-scan-binaries" {
    organization = "hashicorp"
    repository = "crt-workflows-common"
    workflow = "security-scan-binaries"
    config = "security-scan.hcl"
  }

  notification {
    on = "fail"
  }
}

event "sign" {
  depends = ["security-scan-binaries"]

  action "sign" {
    organization = "hashicorp"
    repository = "crt-workflows-common"
    workflow = "sign"
  }

  notification {
    on = "fail"
  }
}

event "verify" {
  depends = ["sign"]

  action "verify" {
    organization = "hashicorp"
    repository = "crt-workflows-common"
    workflow = "verify"
  }

  notification {
    on = "fail"
  }
}

event "trigger-staging" {
  # Dispatched by bob trigger-promotion; required by CRT promotion workflows.
}

event "promote-staging" {
  depends = ["trigger-staging"]

  action "promote-staging" {
    organization = "hashicorp"
    repository = "crt-workflows-common"
    workflow = "promote-staging"
    config = "release-metadata.hcl"
  }

  notification {
    on = "always"
  }
}

event "trigger-production" {
  # Dispatched by bob trigger-promotion; required by CRT promotion workflows.
}

event "promote-production" {
  depends = ["trigger-production"]

  action "promote-production" {
    organization = "hashicorp"
    repository = "crt-workflows-common"
    workflow = "promote-production"
  }

  notification {
    on = "always"
  }
}