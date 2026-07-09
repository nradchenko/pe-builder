# Minimal pe-builder manifest — a bootable PE ISO from a Windows source, no plugins.
# Edit source.path to your Windows XP SP3 media, then:  pebuild build examples/minimal.hcl

source {
  path = "/path/to/winxp-sp3.iso" # a directory or an .iso (auto-detected)
}

output {
  target       = "iso"
  path         = "./out/pe.iso"
  volume_label = "PEBUILDER"
}