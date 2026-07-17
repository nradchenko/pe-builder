# pe-builder manifest — a self-contained PE ISO that boots to a Windows Explorer desktop with the
# XP "Luna" visual style. Set source.path, then:  pebuild build examples/luna.hcl
#
# Boots from CD alone (no attached disk); enabling luna pulls in everything it needs. Needs at
# least 128 MB RAM. Screen resolution is hardcoded at 1024x768.

source {
  path = "/path/to/winxp-sp3.iso"
}

output {
  target       = "iso"
  path         = "./out/pe-theme.iso"
  volume_label = "PETHEME"
}

build {
  allow_plugin_builds = true
}

plugin "luna" {
  enabled = true
}