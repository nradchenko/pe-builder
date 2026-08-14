# pe-builder — a WIM-boot ISO: the whole tree is packed into one compressed WIM and read on
# demand at boot through the wimmf overlay filter, so the image is smaller than the classic
# layout. Set source.path, then:  pebuild build examples/wim.hcl
#
# The `wim` layout is experimental; see docs/wim-boot.md for how it works and the plugins it needs.

source {
  path = "/path/to/winxp-sp3.iso"
}

output {
  target       = "iso"
  path         = "./out/pe-wim.iso"
  volume_label = "PEWIM"
}

layout = "wim"

build {
  allow_plugin_builds = true
}

# A themed Explorer desktop, the XP games, and the bundled GUI utilities.
plugin "luna"         { enabled = true }
plugin "games"        { enabled = true }
plugin "system-tools" { enabled = true }
plugin "accessories"  { enabled = true }