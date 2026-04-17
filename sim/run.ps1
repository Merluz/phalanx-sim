param(
  [ValidateSet("build", "dashboard-dev", "dashboard-quiet", "sim-smoke", "sim-load")]
  [string]$Profile = "dashboard-dev",
  [int]$Nodes = 500,
  [int]$Port = 8090
)

$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
Push-Location $root
try {
  switch ($Profile) {
    "build" {
      go build ./cmd/sim ./cmd/dashboard
      break
    }
    "dashboard-dev" {
      go run ./cmd/dashboard -listen ":$Port" -log-mode progress
      break
    }
    "dashboard-quiet" {
      go run ./cmd/dashboard -listen ":$Port" -log-mode quiet
      break
    }
    "sim-smoke" {
      go run ./cmd/sim `
        -n $Nodes `
        -async-spawn=true `
        -spawn-parallelism=0 `
        -stats-every=2s `
        -udp-demo=true `
        -udp-sender=random `
        -udp-interval=1s
      break
    }
    "sim-load" {
      go run ./cmd/sim `
        -n $Nodes `
        -async-spawn=true `
        -spawn-parallelism=0 `
        -stats-every=5s `
        -udp-demo=false `
        -run-for=45s
      break
    }
  }
}
finally {
  Pop-Location
}
