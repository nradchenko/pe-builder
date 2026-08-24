# Security policy

## Reporting a vulnerability

Please report security vulnerabilities **privately**, using GitHub's private reporting: on the
repository's **Security** tab, choose **Report a vulnerability**. That keeps the report private
until a fix is available.

Please do not open a public issue for a security problem.

Include enough to reproduce it — what you did, what happened, the `pebuild` version
(`pebuild version`), and the Windows source involved if it is relevant. This is a small
project, so please be patient with the response time.

## Scope

pe-builder reads a Windows installation source **you** supply and assembles files from it. It
ships no Microsoft files, and a built image is unpatched Windows XP, not a hardened environment
— see [`DISCLAIMER.md`](DISCLAIMER.md). Reports are most useful when they concern the **build
tool itself**: for example, a crafted source or manifest that makes `pebuild` write outside its
output tree, run an unexpected command, or mishandle its input.

## Supported versions

Fixes land on the latest release; there is no long-term-support branch.