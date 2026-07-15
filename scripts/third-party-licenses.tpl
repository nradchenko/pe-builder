pe-builder — Third-Party Software Notices
=========================================

The pe-builder binaries (pebuild, pewalk) statically link the Go modules listed
below. Their licenses require that these notices be retained when the compiled
binaries are redistributed; this file reproduces each module's license text in
full, plus any NOTICE files, for that purpose.

This file is generated — do not edit it by hand. Regenerate it with
`make licenses` whenever the dependency set changes.

pe-builder itself is licensed under Apache-2.0; see the LICENSE file.
{{range .}}
================================================================================
{{.Name}}{{if .Version}}  {{.Version}}{{end}}
License: {{.LicenseName}}{{if .LicenseURL}}
Source:  {{.LicenseURL}}{{end}}
================================================================================

{{.LicenseText}}
{{end}}