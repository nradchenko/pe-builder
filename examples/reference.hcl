# pe-builder manifest — every block and field, annotated.
#
# This one is a reference to read, not a starting point to run: it sets things most builds
# should leave at their defaults, and its plugin block edits a plugin you may not have
# enabled. For a manifest to actually build from, start with minimal.hcl (a bare cmd.exe
# image) or luna.hcl (the themed desktop) and add only what you need.
#
# Optional fields are shown with their default. Everything except source.path and
# output.path can be omitted.

# --- The Windows installation source ------------------------------------------------------
source {
  # Required. A directory or an .iso; which one is detected automatically.
  path = "/path/to/winxp-sp3.iso"

  # Source flavor. "auto" probes the media, which is almost always what you want; the
  # explicit values exist to override a misdetection.
  type = "auto" # auto | winxp | win2003
}

# --- What the build produces ---------------------------------------------------------------
output {
  # Required. Where the image is written.
  path = "./out/pe.iso"

  # Output target. "iso" masters a bootable ISO; "none" assembles the file tree and stops,
  # which is useful when you want to inspect what a build produces.
  target = "iso" # iso | none

  # The ISO's volume label. Keep it short and uppercase — ISO 9660 is strict about both.
  volume_label = "PEBUILDER"
}

# --- How the build runs --------------------------------------------------------------------
build {
  # Scratch directory for the assembled tree.
  work_dir = "./.work"

  # Keep the work directory after a successful build, for inspection. Note that with this on,
  # the whole directory is mastered as-is: a file left behind by an earlier build is shipped
  # too, so prefer a fresh work_dir when it matters what the image contains.
  keep_work = false

  # Allow plugins that ship source to compile it at build time. Built-in plugins always
  # build; this opts in for plugins you supply yourself, which means running their toolchain,
  # so it is off unless you ask for it.
  allow_plugin_builds = false
}

# Where to look for plugins beyond the built-in ones. Each plugin is a directory containing
# an .inf.
plugins_dir = "./plugins"

# --- Plugins --------------------------------------------------------------------------------
# A plugin block selects a plugin and, optionally, adjusts what it contributes. A plugin the
# manifest never mentions is simply not enabled — and enabling one automatically enables the
# plugins it depends on.

# Enabling a plugin, and setting the options it declares. A plugin documents its options in
# its own .inf and README; `pebuild plugins info <name>` lists them. An option the plugin does
# not declare, or a value outside the set it accepts, is a build error.
plugin "explorer-shell" {
  enabled = true

  options {
    width  = 1280
    height = 1024
    bpp    = 16
  }
}

# Declared but switched off. Useful to keep a plugin's configuration in the manifest while
# leaving it out of this build.
plugin "games" {
  enabled = false
}

# The override layers. These edit what an enabled plugin contributes, without modifying the
# plugin itself — an escape hatch for adjusting a plugin you did not write.
plugin "accessories" {
  enabled = true

  files {
    # Extra files to take from the Windows source, by their path in the output tree.
    include = ["i386/system32/charmap.exe"]

    # Files of the plugin's own to leave out, by the path they would be placed at.
    exclude = ["i386/system32/mspaint.exe"]

    # Files to add from outside the source. `from` is a path on the build host, relative to
    # the manifest; `to` is a path in the output tree.
    inject = [
      { from = "./local/notes.txt", to = "i386/system32/notes.txt" },
    ]
  }

  registry {
    # Registry values to write. The hive prefix (HKLM\ or HKU\) selects which hive, and the
    # last backslash-separated component is the value name. Values written here are strings;
    # a plugin's own .inf is the place for typed values.
    set = {
      "HKLM\\SOFTWARE\\pe-builder\\StartMenu\\Character Map" = "%SystemRoot%\\system32\\charmap.exe"
    }

    # Registry values or keys to remove, by the same path form.
    delete = [
      "HKLM\\SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\Explorer\\Advanced\\EnableBalloonTips",
    ]
  }
}