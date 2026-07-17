# pe-builder manifest — a self-contained PE ISO that boots to a Windows Explorer desktop.
# Set source.path, then:  pebuild build examples/explorer-shell.hcl
#
# Boots from CD alone (no attached disk) — explorer-shell pulls in a RAM-drive kernel driver
# that provides the writable profile volume. Needs at least 128 MB RAM. The desktop resolution
# defaults to 1024x768x32; set it per build with the explorer-shell options block below.

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

  # Desktop display mode. Omit for the 1024x768x32 default; stick to modes the boot VGA
  # driver supports.
  # options {
  #   width  = 1280
  #   height = 1024
  #   bpp    = 16
  # }
}