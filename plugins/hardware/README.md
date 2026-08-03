<!--
Copyright 2026 Nikita Radchenko
SPDX-License-Identifier: Apache-2.0
-->
# hardware — device and network configuration at run time

Runs the two configuration passes a PE never gets, at boot, against the real device tree: the
PnP driver install and the network component install. Ships `pehw.exe` and declares both as
tasks in the [startup run list](../startup/README.md).

## Why it exists

Windows configures hardware during setup. A `/minint` boot skips setup entirely, so an
enumerated device never gets a driver installed and a network component never gets configured.

The tempting shortcut is to write the finished registry state into the image instead. It does
not hold: a NIC prepared that way brings its miniport up and drops it again, because what an
install actually writes is richer and more self-consistent than what can be predicted at build
time. **Registry state here is a result, not an ingredient.**

Running the passes for real also makes support general rather than per-device. Without them,
each new NIC needs hand-written driver keys, a `NetCfgInstanceId`, `Linkage` values and a
hardcoded PCI instance path. With them, any device whose INF is reachable configures itself.

## The order of the two passes

The network pass runs **first**, and this is the single most load-bearing decision here.

The binding between a protocol and a miniport is made by NDIS at the moment one of them
registers, and of the two possible moments only one works in a PE: **the adapter arriving into a
protocol that is already installed and running.** So `pehw net` installs TCP/IP and starts it,
and only then does `pehw devices` introduce the NIC.

Run the other way round, every layer reports success and nothing binds — the driver installs,
the devnode starts, INetCfg writes the linkage and even answers `IsBoundTo` = BOUND, and the IP
layer has no interfaces at all. Restarting the adapter afterwards does not rescue it: a restart
is not a first registration. Measured both ways round; no registry key records which happened,
which is why this is worth writing down rather than rediscovering.

## The device pass — `pehw devices`

Walks everything the PnP manager reports and leaves working devices alone, so the pass is
idempotent and a warm re-run costs about a second:

1. Set `DI_QUIETINSTALL | DI_NOFILECOPY` in the install params.
2. `SetupDiBuildDriverInfoList(SPDIT_COMPATDRIVER)` then `DIF_SELECTBESTCOMPATDRV` — **setupapi
   chooses the driver**, we never name an INF. That is what makes the pass general.
3. `SetupDiCallClassInstaller(DIF_INSTALLDEVICE)` — bind the driver that was just selected.
4. `SetupDiCallClassInstaller(DIF_PROPERTYCHANGE)` with `DICS_PROPCHANGE` — restart the devnode.

Three of those four steps exist because of the read-only CD or because setupapi does less than
its name suggests, and `pehw.c` documents each at the call it guards. The consequences worth
knowing before adding hardware:

- **Nothing is ever copied during an install**, so every binary an INF mentions has to be staged
  by this plugin already. Adding an INF is half of adding hardware support; the driver files are
  the other half.
- **Binding a driver does not start the device.** Step 4 is what starts it, and without that step
  a device looks broken in a way that points at the driver rather than at the missing restart.
- **`newdev.dll` cannot be used at all**, which is why the pass calls setupapi directly: it
  copies the INF into the store on its way through, and that write lands on the CD.

## The network stack — `pehw net`

A bound driver gives an adapter nothing to talk to: no per-adapter identity, no bindings, no
transport above it. Supplying those is network component configuration, done through `INetCfg`
in `netcfgx.dll` — the interface Microsoft's own `netcfg -winpe` drives:

    CoCreateInstance(CLSID_CNetCfg) -> INetCfgLock::AcquireWriteLock
      -> INetCfg::Initialize -> QueryNetCfgClass(<the component's class>, INetCfgClassSetup)
      -> INetCfgClassSetup::Install(L"ms_tcpip", OBO_USER, …) -> INetCfg::Apply

**Each component belongs to one of four setup classes** — adapters, protocols, clients and
services — and the install has to be asked for through the right one. TCP/IP is a *protocol*,
which is not where intuition puts it. Naming the wrong class produces a failure that describes
something else entirely, so `pehw` looks each component up rather than assuming, and refuses an
id it does not recognise instead of picking a default (`pehw net trans:ms_foo` names one by
hand). The lookup table and its provenance are in `pehw.c`.

The install writes the protocol's service definitions, load order and per-adapter bindings as one
consistent set, which is why this plugin defines none of them by hand. It also **starts** what it
installs, from the component's own `Ndi\CoServices`: nothing else will, and the adapter only
binds to a protocol that is already running. Co-services a minimal PE cannot run (`svchost` and
`lsass` ones) fail there harmlessly and are reported, not counted as install failures.

### Registering the COM server

`netcfgx.dll` can register its own COM classes, and ordinary Windows setup asks it to. No hive in this image carries that registration, and no PE ever performs the
pass that would create it — so the class is simply not there to be created, and everything
downstream fails for a reason that names the wrong thing.

`pehw net` therefore registers the DLL itself. Doing it here rather than through
`explorer-shell`'s COM registration contract keeps this plugin independent of the shell, so a
console build gets networking too.

**This is separate from the class installer.** setupapi finds a class installer by loading
`Control\Class\<class>\Installer32` **by name**, not through COM — so registering the COM server
does not substitute for those registry values, and the values do not substitute for it. Both are
needed, for different callers.

### Waiting before it runs

Both passes are scheduled early, before the shell, and at that point the service stack is not
necessarily up. COM in particular needs RPC, so `pehw net` blocks until the services it depends
on are running instead of assuming them.

It waits rather than being scheduled later on purpose: moving it later would put it behind the
device pass, and that is the ordering that never binds.

## Which INFs are staged

`DIF_SELECTBESTCOMPATDRV` searches the INF path, so the list in `hardware.inf` *is* the set of
hardware the pass can install, and the set it spends time on. On i386 it covers the three NICs a
VM can offer — RTL8139, AMD PCnet, NE2000-PCI — plus `nettcpip.inf` for the protocol component.
Four files, against 739 and 43 MB for the source's whole `inf` directory.

The list is scoped by architecture, because both halves of it differ. On amd64 media the INF has
another name (`netrtsnx.inf`) and so does the miniport (`rtl39a64.sys`), and the set shrinks to
one adapter: searching every `net*.inf` on XP x64 for their PCI IDs finds **no** AMD PCnet,
NE2000-PCI or e1000 at all. Left unscoped, an amd64 build fails at assembly on i386 file names —
which is the good outcome; the bad one is an arch-scoped INF section that stages nothing while
unscoped registry sections still register it.

**Networking only, deliberately.** Earlier revisions also staged the chipset, IDE, USB and PS/2
input INFs. That cost most of the pass's runtime — on the boot path, where the user is watching —
and bought nothing: the base image already drives all of it from the boot driver set, so the pass
was re-installing drivers that were already loaded and working.

Absences that look like omissions and are not:

- **No `display.inf`.** QEMU presents `1234:1111` (stdvga), which no bundled XP INF claims; the
  boot framebuffer already gives 1024×768×32.
- **No sound.** Nothing in a PE plays audio yet.
- **QEMU's `e1000` is covered by no bundled XP INF**, and there is nothing for `virtio`. RTL8139
  is not merely conventional here — it is the only emulated NIC the media can drive.

## Build

`pehw.exe` is cross-compiled with **MinGW-w64** — no MSVC, no SDK. It is a plain user-mode binary, so
the architecture selects the toolchain and nothing else: `make ARCH=i386` (the default) and
`make ARCH=amd64`, which the plugin's `[Build.<arch>]` sections pass.

## Running it by hand

Each task is a separate process, so it is re-runnable in a single boot and hardware can be
iterated on without rebooting.

    pehw net                install ms_tcpip and start what it declares
    pehw devices            run the device pass, one line per device
    pehw status             what is bound, what is running, and the PnP problem code for what is not
    pehw service <name>     an `sc qc` for an image with no sc.exe
    pehw ip                 what the IP layer has: adapters, addresses, the DHCP lease
    perun --task devices    the same pass, through the run list, recording a verdict

## Reading a run

Every step of `pehw net` prints its `HRESULT` raw as well as by name. Two of those differ only in
the severity bit and mean opposite things: `0x0004A020` is `NETCFG_S_REBOOT`, which is **success**
(a PE never reboots), while `0x8004A020` is `NETCFG_E_ALREADY_INITIALIZED`.

The last line of each task is a self-contained summary, because the run list keeps only that one
line as the task's `Message` — including, when a run never reached the install, an explicit
`ABORTED` rather than a zero count that reads like success.

A co-service the image does not define is reported as `skipped`, not as a failure: build 3790's
`nettcpip.inf` lists `Smb`, which belongs to a component this does not install.

## Known limitation: no network on build 3790

Networking works on Windows XP SP3 (32-bit). On **build 3790 — XP x64 and Server 2003, both
architectures — it does not.** The install half succeeds: the adapter binds and TCP/IP is
installed. What fails is the win32 co-services above it — `Dhcp` and `Dnscache` exit before
reporting ready (`ERROR_BROKEN_PIPE`), `Lmhosts` and `Nla` time out — so no address is acquired
and the image has a bound adapter with no connectivity.

It tracks the **codebase, not the architecture**: 32-bit Server 2003 fails identically to XP x64.
Don't re-derive it from amd64.