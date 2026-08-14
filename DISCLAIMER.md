# Disclaimer

## Not affiliated

pe-builder is **not affiliated with, endorsed by, or derived from the code of** Bart
Lagerweij or the original PE Builder — the source of which was never released. It is an
independent, clean-room reimplementation of that tool's ideas.

Windows, Windows XP, and Windows Server are trademarks of Microsoft Corporation.
pe-builder is not affiliated with or endorsed by Microsoft, and ships no Microsoft files:
it only reads and repackages the installation source you supply. "BartPE" refers to the
environment produced by the original tool and is used here only for description and
compatibility.

## Windows XP is end-of-life — treat what you build as untrusted

Windows XP left extended support in **April 2014**. It receives no security updates, and
many publicly known vulnerabilities in it will never be fixed. An image built with
pe-builder is a stripped-down XP with no updates, no firewall and no anti-malware, and it
runs with full privileges and no restriction mechanism by default.

**Never use it as a working system**, and never put it on a network you care about. Run it
in an isolated environment — ideally an offline virtual machine. Keep it off the public
internet, don't use it to handle credentials or data that matter, and assume anything it
touches is exposed.

## Bring your own Windows — and your own licence

pe-builder ships no Microsoft files and provides no way to obtain Windows. Building an
image requires a Windows installation source that **you** supply and are licensed to use,
and whatever you build from it remains subject to Microsoft's licence terms. This project
does not condone or support piracy.

The same principle applies to everything else here: we have no wish to take anyone's work,
whatever it is. If you believe this project infringes a copyright, or fails to credit an
author properly, please get in touch and we will put it right.

## Redistribution

pe-builder is provided for **personal and informational use only**. An image you build is
assembled on your machine from the Windows source you supply and the plugins you install; it
is meant for your own use.

If you pass one on, **you** become its distributor and take on any obligations that follow —
not this project. The image contains Microsoft binaries from your source, which Microsoft's
licence generally does not permit redistributing; and any plugin components it embeds may be
under their own licences, some copyleft, which then must be accompanied by their licence and
corresponding source — provided by each plugin's own project. pe-builder ships no Microsoft
files and bundles no plugin binaries; it only operates on inputs you supply.

## An experiment, for enthusiasts

pe-builder is built on and inspired by software that is end-of-life and long abandoned. It
exists as an experiment: bringing a historical Windows build process to a modern Go stack.
It is a hobby project for enthusiasts and retrocomputing — not a supported product, with no
warranty and no guarantee of fitness for any purpose (see [LICENSE](LICENSE)).