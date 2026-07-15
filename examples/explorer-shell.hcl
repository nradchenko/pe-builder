# pe-builder manifest — a self-contained PE ISO that boots to a Windows Explorer desktop.
# Set source.path, then:  pebuild build examples/explorer-shell.hcl
#
# Boots from CD alone (no attached disk) — explorer-shell pulls in a RAM-drive kernel driver
# that provides the writable profile volume. Needs at least 128 MB RAM. Screen resolution is
# hardcoded at 1024x768.

source {
  path = "/path/to/winxp-sp3.iso"
}

output {
  target       = "iso"
  path         = "./out/pe-shell.iso"
  volume_label = "PESHELL"
}

build {
  allow_plugin_builds = true
}

plugin "explorer-shell" {
  enabled = true
}