# Example pe-builder build manifest.
#
# Builds a minimal bootable rescue environment from a Windows installation
# source. Copy this to pebuild.hcl and adjust the paths to your source.

source {
  path = "/isos/winxp-sp3.iso" # a directory or an .iso (auto-detected)
  type = "auto"                # auto | winxp | win2003
}

output {
  target       = "iso"
  path         = "./out/rescue.iso"
  volume_label = "RESCUE"
}

build {
  work_dir  = "./.work"
  keep_work = false
}

plugins_dir = "./plugins"

# A plugin declared but disabled: carried, not built.
plugin "example" {
  enabled = false

  options {
    note = "options are plugin-specific; carried but not yet acted on"
  }
}

# A minimal enabled plugin that injects a startup script and a registry value.
plugin "shell" {
  enabled = true

  files {
    include = ["i386/cmd.exe"]
    inject  = [{ from = "./local/startnet.cmd", to = "i386/system32/startnet.cmd" }]
  }

  registry {
    set = {
      "HKLM\\SYSTEM\\Setup\\CmdLine" = "cmd.exe /k startnet.cmd"
    }
  }
}
