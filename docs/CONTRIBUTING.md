# Contributing

Thanks for your interest in pe-builder. This is a small project; the notes below are enough to
get a change in.

## Building and testing

You need Go (see `go.mod` for the version) and, for changes that build a desktop image, the
MinGW-w64 cross-compiler (`gcc-mingw-w64-i686`, and `gcc-mingw-w64-x86-64` for 64-bit).

```sh
go build ./...
go test ./...
gofmt -l .   # should print nothing
go vet ./...
```

Testing against a real Windows source needs media you supply; those tests skip when it is
absent (see the test files for how they locate it). pe-builder is developed on Linux — see the
platform note in the [README](../README.md#quick-start).

### Editing a built-in plugin or the base layer

The built-in plugins and the base layer are compiled *into* the binary (`plugins/embed.go`
and `internal/base/base.go` embed them with `go:embed`), so a `pebuild` you built earlier keeps
building from the copy it was compiled with. Edit `plugins/<name>/<name>.inf` or
`internal/base/base.inf`, run a stale binary, and the build quietly uses the old file — nothing
reports it, and the resulting image looks like your change simply had no effect.

Rebuild after every such edit, or iterate with `go run`, which recompiles and re-embeds
each time and so cannot go stale:

```sh
go run ./cmd/pebuild build pebuild.hcl
```

## Ground rules

- **Clean-room.** pe-builder is an independent reimplementation of the ideas behind Bart's PE
  Builder. Do not contribute code, binaries, or text taken from the original tool.
- **No Microsoft files, ever.** Never add Windows/Microsoft files, or any third-party
  installation media, to the repository or to a pull request. The tool operates only on a
  source the *user* supplies at build time.
- **Keep commits scoped.** A commit should contain only the files that belong to the change it
  describes, and its message should say what the change does.
- **A non-trivial plugin gets a `README.md`** next to its `.inf`, describing what it does and
  the decisions behind it.
- **Keep docs in step with code.** A change that alters what the tool does or how it is used
  should update the README and, where relevant, `docs/`.

## AI-assisted contributions

AI tools are welcome here — pe-builder itself is built with them. If you used one, just **say
so plainly in the pull request**: which tool, and how you used it (say, "drafted the parser with
X", or "used Y to review the diff"). The bar for the change is the same either way — you are
responsible for understanding what you submit, it must meet the rules above, and the clean-room
and no-Microsoft-files rules apply to anything a tool produces just as they do to hand-written
code.

## Licensing

pe-builder is licensed under Apache-2.0 (see [`LICENSE`](../LICENSE)). By submitting a
contribution, you agree it is provided under the same license.

There is no CLA and no copyright assignment: **your contribution stays yours**. New files you
write should carry your own name and the year you wrote them, above the SPDX identifier —

```
// Copyright 2026 Jane Doe
// SPDX-License-Identifier: Apache-2.0
```

— in whatever comment syntax the file uses. When you change a file someone else wrote, leave its
existing header as it is, and please don't add yourself to `NOTICE`: that file lists the
attributions everyone redistributing pe-builder is obliged to reproduce, not the people who have
contributed. Plugin authors: [`docs/plugins/conventions.md`](plugins/conventions.md#copyright-headers)
says the same in more detail.

## Reporting bugs and asking for features

Open an issue. For security problems, follow [`SECURITY.md`](SECURITY.md) instead of filing a
public issue.