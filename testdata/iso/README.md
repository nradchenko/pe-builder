# Local Windows test sources

Some tests build from a real Windows installation source instead of a synthetic one,
and read it from this directory. No such media are distributed with the project, so
this directory ships empty apart from this file — put your own copy here and the tests
will find it.

Everything in this directory is ignored by git except this README, so whatever you add
here stays local. Never commit installation media.

## What to put here

Most of these tests name the source they want, so the filename has to match. Today that
is one medium:

```
ru_windows_xp_professional_with_service_pack_3_x86_cd_vl_x14-74146.iso
```

Either drop the `.iso` in, or symlink it from wherever you keep your media:

```sh
ln -s /path/to/your/media/winxp-sp3-ru.iso \
      testdata/iso/ru_windows_xp_professional_with_service_pack_3_x86_cd_vl_x14-74146.iso
```

Some of them compare against golden output produced from that exact medium, so a
differently-localized source under the same name will not necessarily match.

## Bringing your own source

The boot-closure regression in `internal/base/` takes any XP source, from
`PEBUILD_TEST_SOURCE`:

```sh
PEBUILD_TEST_SOURCE=/absolute/path/to/en_windows_xp_sp3.iso go test ./internal/base/
```

Give it an **absolute** path: a test runs with its own package directory as the working
directory, so a relative one is resolved against `internal/base/` and the source is not
found.

It falls back to the symlink above when the variable is unset. It is currently the only
test that reads the variable — the rest resolve the name above — so an English or other
source exercises that one and leaves the others skipped.

A test that needs a source it cannot find **skips** rather than fails, so the suite
still runs green with this directory empty — it just covers less. If you are changing
anything that reads a source, populate this directory first, or the tests that would
have caught you will quietly sit out. `go test ./... -v` names each skip.

In a `git worktree`, symlink the media into this directory the same way; do not replace
the directory itself with a symlink, which would put the whole thing outside what the
ignore rules can reach.
