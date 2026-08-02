# serialdebug

Boots the PE with the Windows kernel debugger enabled on COM1, so a build that fails to start
can be observed instead of guessed at.

A PE that will not boot offers very little: no event log, no user mode, often not even a
bugcheck screen. With the debugger on, the boot narrates itself over a serial port — every
`DbgPrint` a driver emits, plus the bugcheck data if it stops — which is the difference
between "it hangs" and knowing which driver failed and why.

Disabled by default. It is a diagnostic, not something to ship: it costs a serial port and
slows the boot.

## Using it

Enable the plugin in the manifest:

```hcl
plugin "serialdebug" { enabled = true }
```

Then boot the image with a serial port attached and put a KD client on the other end:

```
pebuild run out/pe.iso --serial unix:/tmp/kd.sock,server,nowait
```

`--serial` takes any QEMU chardev specification and wires it to COM1. The loader flags this
plugin sets are `/debug /debugport=com1 /baudrate=115200`, which is what a KD client expects by
default.

Always boot a serialdebug image with a serial port and a client attached; the next section
describes what happens if you do not.

## The trap: /debug with nothing attached is worse than nothing

If `/debug` is set but **no debugger is connected**, the kernel sets `KdDebuggerNotPresent` and
**suppresses `DbgPrint` entirely** — only module-load and state-change packets flow. Worse, a
bugcheck breaks into the debugger and waits rather than painting a blue screen.

So with no client attached you get an *empty* capture and a boot that looks frozen, which reads
as "the plugin did nothing" or "the image got worse". A plain `-serial file:…` capture is not
enough; something has to speak the KD protocol on the other end, acknowledge packets, and
un-suppress the output.

## Why the option string is duplicated

The loader options are applied with `SetValue`, which **sets** the whole value rather than
appending to it. There is no append-to-value operation in the INF grammar, so this plugin has
to restate the base layer's entire `osloadoptions` string and add the debug flags — separately
per architecture, because the HAL differs (`halaacpi.dll` on i386, `hal.dll` on amd64).

That duplication is deliberate but fragile: if the base layer's loader options ever change —
a different HAL, a new switch — this plugin would keep setting the old string and silently
produce an image that boots differently from every other build, with a debugger that may not
come up at all.

`TestSerialDebugMatchesBase` guards exactly that. It reads both this `.inf` and `base.inf` and
asserts each architecture's value here equals the base value plus the debug suffix, so a change
to the base options fails the test instead of producing a quietly wrong image.

The cleaner fix is an append-to-value operation in the INF grammar, which would let this plugin
carry only ` /debug /debugport=com1 /baudrate=115200` and never restate anything. It is not
free: the value in `txtsetup.sif` is quoted, so appending has to happen *inside* the quotes,
and getting that wrong produces a subtly malformed loader line — the kind of mistake that costs
a boot to find. Worth doing, but as its own change with its own tests.