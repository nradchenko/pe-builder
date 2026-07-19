# pe-builder manifest — a self-contained PE ISO that boots to a Windows Explorer desktop with the
# XP "Luna" visual style. Set source.path, then:  pebuild build examples/luna.hcl
#
# Boots from CD alone (no attached disk); enabling luna pulls in everything it needs. Needs at
# least 128 MB RAM. The desktop resolution defaults to 1024x768x32; set it per build with an
# explorer-shell options block (uncommented below) — resolution lives on that plugin, which luna
# pulls in.

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

  # Colour scheme and font size. These are the defaults; color also takes "HomeStead" (olive)
  # or "Metallic" (silver), and size "LargeFonts" or "ExtraLargeFonts".
  # options {
  #   color = "NormalColor"
  #   size  = "NormalSize"
  # }
}

# Resolution is an explorer-shell option (luna depends on explorer-shell). Uncomment to change
# the desktop mode; stick to modes the boot VGA driver supports.
# plugin "explorer-shell" {
#   options {
#     width  = 1280
#     height = 1024
#     bpp    = 16
#   }
# }