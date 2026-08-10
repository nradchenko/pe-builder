/* Copyright 2026 Nikita Radchenko
 * SPDX-License-Identifier: Apache-2.0
 *
 * ramdrive — the RAM-drive kernel driver: a WDM PnP function driver that presents a
 * RAM-backed FAT16 scratch volume, lettered via the Mount Manager, so a standalone
 * boot needs no attached disk. See README.md for the design, parameters and build;
 * the [src: …] tags below cite the public docs listed in README § Sources.
 */
/* Target Windows XP (literals: the symbolic NTDDI_* names aren't defined until
 * sdkddkver.h consumes these — 0x0501 = _WIN32_WINNT_WINXP, 0x05010000 = NTDDI_WINXP). */
#define _WIN32_WINNT  0x0501
#define NTDDI_VERSION 0x05010000
#include <sdkddkver.h>

/* mingw-w64's ddk wdm.h guards a Vista-only field on NTDDI_WINVISTA — a typo for
 * NTDDI_VISTA (defined nowhere), so at a sub-Vista target the field compiles in
 * against an undefined struct. Alias the typo to the real macro. */
#ifndef NTDDI_WINVISTA
#define NTDDI_WINVISTA NTDDI_VISTA
#endif

/* INITGUID before ntddk.h switches DEFINE_GUID to its instantiating form, so
 * MOUNTDEV_MOUNTED_DEVICE_GUID (declared in mountmgr.h) gets storage in this TU —
 * without it, a link error at IoRegisterDeviceInterface. */
#define INITGUID
#include <ntddk.h>
#include <mountmgr.h>
#include <mountdev.h>

/* Opt-in debug tracing (build with -DRAMDRIVE_DEBUG); off by default. See README § Build. */
#if defined(RAMDRIVE_DEBUG)
#define RAM_DBG(fmt, ...) DbgPrint("ramdrive: " fmt, ##__VA_ARGS__)
#else
#define RAM_DBG(fmt, ...) ((void)0)
#endif

/* Disk/storage IOCTL contract — hand-defined because MinGW omits ntdddisk.h /
 * ntddstor.h; the codes and layouts are stable public ABI. See README.md
 * § Disk and Mount Manager IOCTLs. [src: ntddk] */
#ifndef IOCTL_DISK_BASE
#define IOCTL_DISK_BASE     FILE_DEVICE_DISK          /* 0x00000007 */
#define IOCTL_STORAGE_BASE  0x0000002D
#define IOCTL_VOLUME_BASE   ((ULONG)'V')              /* 0x00000056 */

#define IOCTL_DISK_GET_DRIVE_GEOMETRY    CTL_CODE(IOCTL_DISK_BASE, 0x0000, METHOD_BUFFERED, FILE_ANY_ACCESS)
#define IOCTL_DISK_GET_PARTITION_INFO    CTL_CODE(IOCTL_DISK_BASE, 0x0001, METHOD_BUFFERED, FILE_READ_ACCESS)
#define IOCTL_DISK_GET_DRIVE_GEOMETRY_EX CTL_CODE(IOCTL_DISK_BASE, 0x0028, METHOD_BUFFERED, FILE_ANY_ACCESS)
#define IOCTL_DISK_GET_PARTITION_INFO_EX CTL_CODE(IOCTL_DISK_BASE, 0x0012, METHOD_BUFFERED, FILE_ANY_ACCESS)
#define IOCTL_DISK_IS_WRITABLE           CTL_CODE(IOCTL_DISK_BASE, 0x0009, METHOD_BUFFERED, FILE_ANY_ACCESS)
#define IOCTL_DISK_GET_LENGTH_INFO       CTL_CODE(IOCTL_DISK_BASE, 0x0017, METHOD_BUFFERED, FILE_READ_ACCESS)
#define IOCTL_DISK_CHECK_VERIFY          CTL_CODE(IOCTL_DISK_BASE, 0x0200, METHOD_BUFFERED, FILE_READ_ACCESS)
#define IOCTL_DISK_MEDIA_REMOVAL         CTL_CODE(IOCTL_DISK_BASE, 0x0201, METHOD_BUFFERED, FILE_READ_ACCESS)

#define IOCTL_STORAGE_CHECK_VERIFY       CTL_CODE(IOCTL_STORAGE_BASE, 0x0200, METHOD_BUFFERED, FILE_READ_ACCESS)
#define IOCTL_STORAGE_CHECK_VERIFY2      CTL_CODE(IOCTL_STORAGE_BASE, 0x0200, METHOD_NEITHER, FILE_ANY_ACCESS)
#define IOCTL_STORAGE_MEDIA_REMOVAL      CTL_CODE(IOCTL_STORAGE_BASE, 0x0201, METHOD_BUFFERED, FILE_READ_ACCESS)
#define IOCTL_STORAGE_GET_HOTPLUG_INFO   CTL_CODE(IOCTL_STORAGE_BASE, 0x0305, METHOD_BUFFERED, FILE_ANY_ACCESS)
#define IOCTL_STORAGE_GET_DEVICE_NUMBER  CTL_CODE(IOCTL_STORAGE_BASE, 0x0420, METHOD_BUFFERED, FILE_ANY_ACCESS)

#define IOCTL_VOLUME_GET_VOLUME_DISK_EXTENTS CTL_CODE(IOCTL_VOLUME_BASE, 0, METHOD_BUFFERED, FILE_ANY_ACCESS)

typedef enum _MEDIA_TYPE { FixedMedia = 12 } MEDIA_TYPE;

typedef struct _DISK_GEOMETRY {
    LARGE_INTEGER Cylinders;
    MEDIA_TYPE    MediaType;
    ULONG         TracksPerCylinder;
    ULONG         SectorsPerTrack;
    ULONG         BytesPerSector;
} DISK_GEOMETRY, *PDISK_GEOMETRY;

typedef struct _DISK_GEOMETRY_EX {
    DISK_GEOMETRY Geometry;
    LARGE_INTEGER DiskSize;
    UCHAR         Data[1];
} DISK_GEOMETRY_EX, *PDISK_GEOMETRY_EX;

typedef struct _GET_LENGTH_INFORMATION {
    LARGE_INTEGER Length;
} GET_LENGTH_INFORMATION, *PGET_LENGTH_INFORMATION;

typedef struct _PARTITION_INFORMATION {
    LARGE_INTEGER StartingOffset;
    LARGE_INTEGER PartitionLength;
    ULONG         HiddenSectors;
    ULONG         PartitionNumber;
    UCHAR         PartitionType;
    BOOLEAN       BootIndicator;
    BOOLEAN       RecognizedPartition;
    BOOLEAN       RewritePartition;
} PARTITION_INFORMATION, *PPARTITION_INFORMATION;

typedef enum _PARTITION_STYLE {
    PARTITION_STYLE_MBR = 0,
    PARTITION_STYLE_GPT = 1,
    PARTITION_STYLE_RAW = 2
} PARTITION_STYLE;

typedef struct _PARTITION_INFORMATION_MBR {
    UCHAR   PartitionType;
    BOOLEAN BootIndicator;
    BOOLEAN RecognizedPartition;
    ULONG   HiddenSectors;
} PARTITION_INFORMATION_MBR;

typedef struct _PARTITION_INFORMATION_GPT {
    GUID    PartitionType;
    GUID    PartitionId;
    ULONG64 Attributes;
    WCHAR   Name[36];
} PARTITION_INFORMATION_GPT;

typedef struct _PARTITION_INFORMATION_EX {
    PARTITION_STYLE PartitionStyle;
    LARGE_INTEGER   StartingOffset;
    LARGE_INTEGER   PartitionLength;
    ULONG           PartitionNumber;
    BOOLEAN         RewritePartition;
    union {
        PARTITION_INFORMATION_MBR Mbr;
        PARTITION_INFORMATION_GPT Gpt;
    };
} PARTITION_INFORMATION_EX, *PPARTITION_INFORMATION_EX;

typedef struct _STORAGE_HOTPLUG_INFO {
    ULONG   Size;
    BOOLEAN MediaRemovable;
    BOOLEAN MediaHotplug;
    BOOLEAN DeviceHotplug;
    BOOLEAN WriteCacheEnableOverride;
} STORAGE_HOTPLUG_INFO, *PSTORAGE_HOTPLUG_INFO;

typedef struct _STORAGE_DEVICE_NUMBER {
    ULONG DeviceType;
    ULONG DeviceNumber;
    ULONG PartitionNumber;
} STORAGE_DEVICE_NUMBER, *PSTORAGE_DEVICE_NUMBER;

typedef struct _DISK_EXTENT {
    ULONG         DiskNumber;
    LARGE_INTEGER StartingOffset;
    LARGE_INTEGER ExtentLength;
} DISK_EXTENT, *PDISK_EXTENT;

typedef struct _VOLUME_DISK_EXTENTS {
    ULONG       NumberOfDiskExtents;
    DISK_EXTENT Extents[1];
} VOLUME_DISK_EXTENTS, *PVOLUME_DISK_EXTENTS;
#endif /* IOCTL_DISK_BASE */

#define RAM_TAG        'drEP'          /* pool tag "PErd" — pe-builder RAM drive */
#define SECTOR_SIZE    512
#define ROOT_ENTRIES   512

/* Flat NonPagedPool backing store; the default is conservative because non-paged
 * pool is limited on 32-bit XP, and the lower bound keeps the volume in the FAT16
 * cluster range. See README.md § Backing store and size. [src: ntddk, FATspec] */
#define DEFAULT_SIZE   (8u * 1024 * 1024)
#define MIN_SIZE       (4u * 1024 * 1024)
/* Ceiling for the flat NonPagedPool allocation. Tens of MB are not safe from a
 * flat pool block early in boot (they need the MmAllocatePagesForMdl strategy,
 * out of scope here), so cap DiskSize rather than let a large value fail START. */
#define MAX_SIZE       (16u * 1024 * 1024)

#define DEVICE_NAME    L"\\Device\\RamDrive"

/* A stable unique id for the volume — mountmgr keys its letter assignment on it.
 * Fixed bytes: this driver only ever exposes one volume. [src: MountMgr] */
static const UCHAR RamUniqueId[] = { 'P','E','-','R','A','M','D','R','I','V','E' };

typedef struct _RAM_EXT {
    PDEVICE_OBJECT Fdo;                /* our function device object */
    PDEVICE_OBJECT Pdo;               /* the PnP-created physical device object */
    PDEVICE_OBJECT Lower;             /* the device below us in the stack */
    IO_REMOVE_LOCK RemoveLock;
    PUCHAR         Image;             /* the backing store (NonPagedPool) */
    ULONG          Size;              /* bytes */
    ULONG          SectorsPerCluster; /* 0 = auto (chosen by the formatter) */
    WCHAR          Letter;            /* pinned DOS letter, 0 = let mountmgr pick */
    BOOLEAN        Started;
    BOOLEAN        InterfaceRegistered;
    UNICODE_STRING InterfaceName;     /* the registered device-interface link */
    UNICODE_STRING DeviceName;        /* \Device\RamDrive (points at DeviceNameBuf) */
    WCHAR          DeviceNameBuf[32];
} RAM_EXT, *PRAM_EXT;

/* The driver's registry path (\Registry\Machine\...\Services\ramdrive), captured
 * in DriverEntry so START_DEVICE can open its Parameters subkey. */
static UNICODE_STRING gRegPath;
static WCHAR          gRegPathBuf[256];

DRIVER_INITIALIZE DriverEntry;
DRIVER_UNLOAD     RamUnload;
DRIVER_ADD_DEVICE RamAddDevice;
DRIVER_DISPATCH   RamCreateClose, RamReadWrite, RamDeviceControl, RamPnp,
                  RamPower, RamSystemControl, RamInvalid;

/* ---- FAT16 super-floppy formatter ------------------------------------- */

static VOID put16(PUCHAR p, ULONG v) { p[0] = (UCHAR)v; p[1] = (UCHAR)(v >> 8); }
static VOID put32(PUCHAR p, ULONG v) {
    p[0] = (UCHAR)v; p[1] = (UCHAR)(v >> 8); p[2] = (UCHAR)(v >> 16); p[3] = (UCHAR)(v >> 24);
}

/* Format the zeroed image as a blank FAT16 super-floppy (filesystem at sector 0,
 * no partition table). spcHint pins the sectors-per-cluster if non-zero, else it
 * grows to keep the cluster count in the FAT16 range. See README.md § FAT16
 * super-floppy format. [src: FATspec] */
static VOID RamFormatFat16(PUCHAR img, ULONG bytes, ULONG spcHint)
{
    ULONG totalSectors = bytes / SECTOR_SIZE;
    ULONG rootDirSectors = (ROOT_ENTRIES * 32 + SECTOR_SIZE - 1) / SECTOR_SIZE;
    ULONG spc, clusters, fatSize, dataSectors, fatStart, i;
    PUCHAR bs = img;

    /* Keep the cluster count inside the FAT16 range (< 65525) by growing the
     * cluster size for larger disks. */
    spc = (spcHint && (spcHint & (spcHint - 1)) == 0) ? spcHint : 1;
    while (totalSectors / spc > 60000) spc <<= 1;
    /* Don't let an oversized spcHint drop the cluster count below the FAT16 floor
     * (~4085); FastFAT would then read the volume as FAT12, contradicting the
     * FAT16 BPB/label. Shrink spc until it clears the floor (with margin for the
     * reserved/root/FAT overhead). [src: FATspec] */
    while (spc > 1 && totalSectors / spc < 4200) spc >>= 1;

    /* Iterate the FAT size to a fixed point: its size depends on the cluster
     * count, which depends on how many sectors the FAT itself consumes. */
    fatSize = 1;
    for (;;) {
        ULONG need;
        dataSectors = totalSectors - 1 /*reserved*/ - rootDirSectors - 2 * fatSize;
        clusters = dataSectors / spc;
        need = ((clusters + 2) * 2 + SECTOR_SIZE - 1) / SECTOR_SIZE;
        if (need <= fatSize) break;
        fatSize = need;
    }

    /* BIOS Parameter Block */
    bs[0] = 0xEB; bs[1] = 0x3C; bs[2] = 0x90;          /* jmp + nop */
    RtlCopyMemory(bs + 3, "MSDOS5.0", 8);
    put16(bs + 11, SECTOR_SIZE);
    bs[13] = (UCHAR)spc;
    put16(bs + 14, 1);                                  /* reserved sectors */
    bs[16] = 2;                                          /* number of FATs */
    put16(bs + 17, ROOT_ENTRIES);
    if (totalSectors < 0x10000) put16(bs + 19, totalSectors);
    bs[21] = 0xF8;                                       /* fixed media */
    put16(bs + 22, fatSize);
    put16(bs + 24, 64);                                  /* sectors per track */
    put16(bs + 26, 16);                                  /* heads */
    put32(bs + 28, 0);                                   /* hidden sectors */
    if (totalSectors >= 0x10000) put32(bs + 32, totalSectors);
    bs[36] = 0x80;                                       /* drive number */
    bs[38] = 0x29;                                       /* extended boot sig */
    put32(bs + 39, 0x50454252);                          /* volume id */
    RtlCopyMemory(bs + 43, "PE RAMDISK ", 11);
    RtlCopyMemory(bs + 54, "FAT16   ", 8);
    /* bs, fat and root all alias img — the caller's disk buffer — so their final writes are not
       dead; cppcheck cannot see the aliasing. See README.md § FAT16 super-floppy format. */
    /* cppcheck-suppress unreadVariable */
    bs[510] = 0x55; bs[511] = 0xAA;

    /* FAT[0] = media | 0xFF00, FAT[1] = EOC — in both FAT copies. */
    fatStart = 1 * SECTOR_SIZE;
    for (i = 0; i < 2; i++) {
        PUCHAR fat = img + fatStart + i * fatSize * SECTOR_SIZE;
        /* cppcheck-suppress unreadVariable */
        fat[0] = 0xF8; fat[1] = 0xFF; fat[2] = 0xFF; fat[3] = 0xFF;
    }

    /* Root-dir volume-label entry (ATTR_VOLUME_ID 0x08) so the volume shows as
     * "PE RAMDISK" — FastFAT reads the label here, not from the BPB. [src: FATspec] */
    {
        PUCHAR root = img + (1 + 2 * fatSize) * SECTOR_SIZE;
        RtlCopyMemory(root, "PE RAMDISK ", 11);
        /* cppcheck-suppress unreadVariable */
        root[11] = 0x08;
    }
}

/* ---- registry parameters --------------------------------------------- */

/* Read a REG_DWORD; returns TRUE and *out on success. Uses Zw* rather than
 * RtlQueryRegistryValues, whose RTL_QUERY_REGISTRY_DIRECT path AVs on a missing
 * REG_SZ with a NULL default. [src: ntddk] */
static BOOLEAN RamQueryDword(HANDLE key, PCWSTR name, PULONG out)
{
    UCHAR buf[sizeof(KEY_VALUE_PARTIAL_INFORMATION) + sizeof(ULONG)];
    PKEY_VALUE_PARTIAL_INFORMATION info = (PKEY_VALUE_PARTIAL_INFORMATION)buf;
    UNICODE_STRING vn;
    ULONG got = 0;
    RtlInitUnicodeString(&vn, name);
    if (!NT_SUCCESS(ZwQueryValueKey(key, &vn, KeyValuePartialInformation, buf, sizeof(buf), &got)))
        return FALSE;
    if (info->Type != REG_DWORD || info->DataLength < sizeof(ULONG))
        return FALSE;
    *out = *(PULONG)info->Data;
    return TRUE;
}

/* Read a REG_SZ drive spec ("R:" or "R") and hand back its first character. */
static BOOLEAN RamQueryLetter(HANDLE key, PCWSTR name, PWCHAR out)
{
    UCHAR buf[sizeof(KEY_VALUE_PARTIAL_INFORMATION) + 16 * sizeof(WCHAR)];
    PKEY_VALUE_PARTIAL_INFORMATION info = (PKEY_VALUE_PARTIAL_INFORMATION)buf;
    UNICODE_STRING vn;
    ULONG got = 0;
    WCHAR c;
    RtlInitUnicodeString(&vn, name);
    if (!NT_SUCCESS(ZwQueryValueKey(key, &vn, KeyValuePartialInformation, buf, sizeof(buf), &got)))
        return FALSE;
    if (info->Type != REG_SZ || info->DataLength < sizeof(WCHAR))
        return FALSE;
    c = ((PWCHAR)info->Data)[0];
    if (c >= L'a' && c <= L'z') c = (WCHAR)(c - L'a' + L'A');
    if (c < L'A' || c > L'Z') return FALSE;
    *out = c;
    return TRUE;
}

/* Load DiskSize / DriveLetter / SectorsPerCluster from Services\ramdrive\Parameters. */
static VOID RamReadParameters(PRAM_EXT ext)
{
    OBJECT_ATTRIBUTES oa;
    UNICODE_STRING path;
    HANDLE key;
    WCHAR buf[288];
    ULONG n, dw;
    WCHAR letter;

    static const WCHAR suffix[] = L"\\Parameters";
    n = gRegPath.Length / sizeof(WCHAR);
    if (gRegPath.Length == 0 || n + RTL_NUMBER_OF(suffix) >= RTL_NUMBER_OF(buf))
        return;
    RtlCopyMemory(buf, gRegPath.Buffer, gRegPath.Length);
    RtlCopyMemory(buf + n, suffix, sizeof(suffix)); /* includes the terminating NUL */
    RtlInitUnicodeString(&path, buf);

    InitializeObjectAttributes(&oa, &path, OBJ_KERNEL_HANDLE | OBJ_CASE_INSENSITIVE, NULL, NULL);
    if (!NT_SUCCESS(ZwOpenKey(&key, KEY_READ, &oa)))
        return;

    if (RamQueryDword(key, L"DiskSize", &dw)) {
        if (dw < MIN_SIZE) dw = MIN_SIZE;
        if (dw > MAX_SIZE) dw = MAX_SIZE;
        ext->Size = dw & ~(SECTOR_SIZE - 1u);
    }
    if (RamQueryDword(key, L"SectorsPerCluster", &dw) && dw <= 128)
        ext->SectorsPerCluster = dw;   /* >128 would overflow the 1-byte BPB field */
    if (RamQueryLetter(key, L"DriveLetter", &letter))
        ext->Letter = letter;

    ZwClose(key);
}

/* ---- I/O dispatch ----------------------------------------------------- */

NTSTATUS NTAPI RamInvalid(PDEVICE_OBJECT dev, PIRP irp)
{
    UNREFERENCED_PARAMETER(dev);
    irp->IoStatus.Status = STATUS_INVALID_DEVICE_REQUEST;
    irp->IoStatus.Information = 0;
    IoCompleteRequest(irp, IO_NO_INCREMENT);
    return STATUS_INVALID_DEVICE_REQUEST;
}

NTSTATUS NTAPI RamCreateClose(PDEVICE_OBJECT dev, PIRP irp)
{
    UNREFERENCED_PARAMETER(dev);
    irp->IoStatus.Status = STATUS_SUCCESS;
    irp->IoStatus.Information = 0;
    IoCompleteRequest(irp, IO_NO_INCREMENT);
    return STATUS_SUCCESS;
}

NTSTATUS NTAPI RamReadWrite(PDEVICE_OBJECT dev, PIRP irp)
{
    PRAM_EXT ext = dev->DeviceExtension;
    PIO_STACK_LOCATION sp = IoGetCurrentIrpStackLocation(irp);
    ULONG len = sp->Parameters.Read.Length;
    ULONGLONG off = (ULONGLONG)sp->Parameters.Read.ByteOffset.QuadPart;
    NTSTATUS status;
    ULONG done = 0;

    status = IoAcquireRemoveLock(&ext->RemoveLock, irp);
    if (!NT_SUCCESS(status)) {
        irp->IoStatus.Status = status;
        irp->IoStatus.Information = 0;
        IoCompleteRequest(irp, IO_NO_INCREMENT);
        return status;
    }

    status = STATUS_SUCCESS;
    if (!ext->Started || !ext->Image) { status = STATUS_DEVICE_NOT_READY; goto complete; }
    if (len == 0) goto complete;
    if (off >= ext->Size) { status = STATUS_END_OF_FILE; goto complete; }
    if (off + len > ext->Size) len = ext->Size - (ULONG)off;

    {
        PVOID buf = MmGetSystemAddressForMdlSafe(irp->MdlAddress, NormalPagePriority);
        if (!buf) { status = STATUS_INSUFFICIENT_RESOURCES; goto complete; }
        if (sp->MajorFunction == IRP_MJ_READ)
            RtlCopyMemory(buf, ext->Image + off, len);
        else
            RtlCopyMemory(ext->Image + off, buf, len);
        done = len;
    }

complete:
    irp->IoStatus.Status = status;
    irp->IoStatus.Information = done;
    IoCompleteRequest(irp, IO_NO_INCREMENT);
    IoReleaseRemoveLock(&ext->RemoveLock, irp);
    return status;
}

NTSTATUS NTAPI RamDeviceControl(PDEVICE_OBJECT dev, PIRP irp)
{
    PRAM_EXT ext = dev->DeviceExtension;
    PIO_STACK_LOCATION sp = IoGetCurrentIrpStackLocation(irp);
    ULONG code = sp->Parameters.DeviceIoControl.IoControlCode;
    ULONG outLen = sp->Parameters.DeviceIoControl.OutputBufferLength;
    PVOID out = irp->AssociatedIrp.SystemBuffer;
    NTSTATUS status;
    ULONG info = 0;

    status = IoAcquireRemoveLock(&ext->RemoveLock, irp);
    if (!NT_SUCCESS(status)) {
        irp->IoStatus.Status = status;
        irp->IoStatus.Information = 0;
        IoCompleteRequest(irp, IO_NO_INCREMENT);
        return status;
    }
    status = STATUS_INVALID_DEVICE_REQUEST;

    switch (code) {
    /* --- disk geometry / size -------------------------------------- [src: ntddk] */
    case IOCTL_DISK_GET_DRIVE_GEOMETRY:
        if (outLen >= sizeof(DISK_GEOMETRY)) {
            PDISK_GEOMETRY g = out;
            ULONG sectors = ext->Size / SECTOR_SIZE;
            g->MediaType = FixedMedia;
            g->TracksPerCylinder = 16;
            g->SectorsPerTrack = 64;
            g->BytesPerSector = SECTOR_SIZE;
            g->Cylinders.QuadPart = sectors / (16 * 64);
            info = sizeof(DISK_GEOMETRY);
            status = STATUS_SUCCESS;
        } else status = STATUS_BUFFER_TOO_SMALL;
        break;

    case IOCTL_DISK_GET_DRIVE_GEOMETRY_EX:
        if (outLen >= FIELD_OFFSET(DISK_GEOMETRY_EX, Data)) {
            PDISK_GEOMETRY_EX gx = out;
            ULONG sectors = ext->Size / SECTOR_SIZE;
            RtlZeroMemory(gx, FIELD_OFFSET(DISK_GEOMETRY_EX, Data));
            gx->Geometry.MediaType = FixedMedia;
            gx->Geometry.TracksPerCylinder = 16;
            gx->Geometry.SectorsPerTrack = 64;
            gx->Geometry.BytesPerSector = SECTOR_SIZE;
            gx->Geometry.Cylinders.QuadPart = sectors / (16 * 64);
            gx->DiskSize.QuadPart = ext->Size;
            info = FIELD_OFFSET(DISK_GEOMETRY_EX, Data);
            status = STATUS_SUCCESS;
        } else status = STATUS_BUFFER_TOO_SMALL;
        break;

    case IOCTL_DISK_GET_LENGTH_INFO:
        if (outLen >= sizeof(GET_LENGTH_INFORMATION)) {
            PGET_LENGTH_INFORMATION li = out;
            li->Length.QuadPart = ext->Size;
            info = sizeof(GET_LENGTH_INFORMATION);
            status = STATUS_SUCCESS;
        } else status = STATUS_BUFFER_TOO_SMALL;
        break;

    /* --- partition info: the whole disk is one recognized FAT partition ---
     * (super-floppy: offset 0, length = whole disk). [src: ntddk] */
    case IOCTL_DISK_GET_PARTITION_INFO:
        if (outLen >= sizeof(PARTITION_INFORMATION)) {
            PPARTITION_INFORMATION pi = out;
            RtlZeroMemory(pi, sizeof(*pi));
            pi->PartitionType = (ext->Size < 32u * 1024 * 1024) ? 0x04 : 0x06; /* FAT16 */
            pi->BootIndicator = FALSE;
            pi->RecognizedPartition = TRUE;
            pi->RewritePartition = FALSE;
            pi->StartingOffset.QuadPart = 0;
            pi->PartitionLength.QuadPart = ext->Size;
            pi->HiddenSectors = 0;
            info = sizeof(PARTITION_INFORMATION);
            status = STATUS_SUCCESS;
        } else status = STATUS_BUFFER_TOO_SMALL;
        break;

    case IOCTL_DISK_GET_PARTITION_INFO_EX:
        if (outLen >= sizeof(PARTITION_INFORMATION_EX)) {
            PPARTITION_INFORMATION_EX pi = out;
            RtlZeroMemory(pi, sizeof(*pi));
            pi->PartitionStyle = PARTITION_STYLE_MBR;
            pi->StartingOffset.QuadPart = 0;
            pi->PartitionLength.QuadPart = ext->Size;
            pi->PartitionNumber = 0;
            pi->RewritePartition = FALSE;
            pi->Mbr.PartitionType = (ext->Size < 32u * 1024 * 1024) ? 0x04 : 0x06;
            pi->Mbr.BootIndicator = FALSE;
            pi->Mbr.RecognizedPartition = TRUE;
            pi->Mbr.HiddenSectors = 0;
            info = sizeof(PARTITION_INFORMATION_EX);
            status = STATUS_SUCCESS;
        } else status = STATUS_BUFFER_TOO_SMALL;
        break;

    /* --- storage identity / media state ---------------------------- [src: ntddk] */
    case IOCTL_STORAGE_GET_HOTPLUG_INFO:
        if (outLen >= sizeof(STORAGE_HOTPLUG_INFO)) {
            PSTORAGE_HOTPLUG_INFO hp = out;
            RtlZeroMemory(hp, sizeof(*hp));
            hp->Size = sizeof(*hp);           /* non-removable, non-hotplug */
            info = sizeof(*hp);
            status = STATUS_SUCCESS;
        } else status = STATUS_BUFFER_TOO_SMALL;
        break;

    case IOCTL_STORAGE_GET_DEVICE_NUMBER:
        if (outLen >= sizeof(STORAGE_DEVICE_NUMBER)) {
            PSTORAGE_DEVICE_NUMBER dn = out;
            dn->DeviceType = FILE_DEVICE_DISK;
            dn->DeviceNumber = 0;
            dn->PartitionNumber = (ULONG)-1; /* whole disk / super-floppy */
            info = sizeof(*dn);
            status = STATUS_SUCCESS;
        } else status = STATUS_BUFFER_TOO_SMALL;
        break;

    case IOCTL_VOLUME_GET_VOLUME_DISK_EXTENTS:
        if (outLen >= sizeof(VOLUME_DISK_EXTENTS)) {
            PVOLUME_DISK_EXTENTS ve = out;
            ve->NumberOfDiskExtents = 1;
            ve->Extents[0].DiskNumber = 0;
            ve->Extents[0].StartingOffset.QuadPart = 0;
            ve->Extents[0].ExtentLength.QuadPart = ext->Size;
            info = sizeof(VOLUME_DISK_EXTENTS);
            status = STATUS_SUCCESS;
        } else status = STATUS_BUFFER_TOO_SMALL;
        break;

    case IOCTL_DISK_IS_WRITABLE:
    case IOCTL_DISK_CHECK_VERIFY:
    case IOCTL_STORAGE_CHECK_VERIFY:
    case IOCTL_STORAGE_CHECK_VERIFY2:
    case IOCTL_DISK_MEDIA_REMOVAL:
    case IOCTL_STORAGE_MEDIA_REMOVAL:
        status = STATUS_SUCCESS;
        break;

    /* --- Mount Manager: how the volume gets a drive letter --------- [src: MountMgr] */
    case IOCTL_MOUNTDEV_QUERY_DEVICE_NAME:
        if (outLen < sizeof(MOUNTDEV_NAME)) { status = STATUS_INVALID_PARAMETER; break; }
        {
            PMOUNTDEV_NAME name = out;
            USHORT need = ext->DeviceName.Length;
            name->NameLength = need;
            if (outLen < FIELD_OFFSET(MOUNTDEV_NAME, Name) + need) {
                info = sizeof(MOUNTDEV_NAME);
                status = STATUS_BUFFER_OVERFLOW;
                break;
            }
            RtlCopyMemory(name->Name, ext->DeviceName.Buffer, need);
            info = FIELD_OFFSET(MOUNTDEV_NAME, Name) + need;
            status = STATUS_SUCCESS;
        }
        break;

    case IOCTL_MOUNTDEV_QUERY_UNIQUE_ID:
        if (outLen < sizeof(MOUNTDEV_UNIQUE_ID)) { status = STATUS_INVALID_PARAMETER; break; }
        {
            PMOUNTDEV_UNIQUE_ID id = out;
            USHORT need = sizeof(RamUniqueId);
            id->UniqueIdLength = need;
            if (outLen < FIELD_OFFSET(MOUNTDEV_UNIQUE_ID, UniqueId) + need) {
                info = sizeof(MOUNTDEV_UNIQUE_ID);
                status = STATUS_BUFFER_OVERFLOW;
                break;
            }
            RtlCopyMemory(id->UniqueId, RamUniqueId, need);
            info = FIELD_OFFSET(MOUNTDEV_UNIQUE_ID, UniqueId) + need;
            status = STATUS_SUCCESS;
        }
        break;

    case IOCTL_MOUNTDEV_QUERY_SUGGESTED_LINK_NAME:
        /* Suggest \DosDevices\<letter>: only if a letter was pinned; otherwise let
         * mountmgr auto-assign the next free letter. [src: MountMgr] */
        if (!ext->Letter) { status = STATUS_NOT_SUPPORTED; break; }
        if (outLen < FIELD_OFFSET(MOUNTDEV_SUGGESTED_LINK_NAME, Name)) {
            status = STATUS_INVALID_PARAMETER;
            break;
        }
        {
            PMOUNTDEV_SUGGESTED_LINK_NAME s = out;
            WCHAR link[14];
            USHORT bytes = sizeof(link);      /* "\DosDevices\R:" = 14 WCHARs, no NUL */
            RtlCopyMemory(link, L"\\DosDevices\\", 12 * sizeof(WCHAR));
            link[12] = ext->Letter;
            link[13] = L':';
            s->UseOnlyIfThereAreNoOtherLinks = TRUE;
            s->NameLength = bytes;
            if (outLen < FIELD_OFFSET(MOUNTDEV_SUGGESTED_LINK_NAME, Name) + bytes) {
                info = sizeof(MOUNTDEV_SUGGESTED_LINK_NAME);
                status = STATUS_BUFFER_OVERFLOW;
                break;
            }
            RtlCopyMemory(s->Name, link, bytes);
            info = FIELD_OFFSET(MOUNTDEV_SUGGESTED_LINK_NAME, Name) + bytes;
            status = STATUS_SUCCESS;
        }
        break;

    default:
        /* Discovery aid: FastFAT/volume/mountmgr issue more IOCTLs than are
         * handled above; log the unhandled code so the set can be grown from a
         * kernel-debug trace rather than guessed. [src: ntddk] */
        RAM_DBG("unhandled IOCTL 0x%08x (func 0x%03x)\n",
                 code, (code >> 2) & 0xFFF);
        status = STATUS_INVALID_DEVICE_REQUEST;
        break;
    }

    irp->IoStatus.Status = status;
    irp->IoStatus.Information = info;
    IoCompleteRequest(irp, IO_NO_INCREMENT);
    IoReleaseRemoveLock(&ext->RemoveLock, irp);
    return status;
}

/* ---- PnP -------------------------------------------------------------- */

static NTSTATUS NTAPI RamSignalCompletion(PDEVICE_OBJECT dev, PIRP irp, PVOID ctx)
{
    UNREFERENCED_PARAMETER(dev);
    UNREFERENCED_PARAMETER(irp);
    KeSetEvent((PKEVENT)ctx, IO_NO_INCREMENT, FALSE);
    return STATUS_MORE_PROCESSING_REQUIRED;
}

/* Forward an IRP to the lower device and wait for it to complete. [src: WDM-PnP] */
static NTSTATUS RamForwardWait(PRAM_EXT ext, PIRP irp)
{
    KEVENT ev;
    NTSTATUS status;
    KeInitializeEvent(&ev, NotificationEvent, FALSE);
    IoCopyCurrentIrpStackLocationToNext(irp);
    IoSetCompletionRoutine(irp, RamSignalCompletion, &ev, TRUE, TRUE, TRUE);
    status = IoCallDriver(ext->Lower, irp);
    if (status == STATUS_PENDING) {
        KeWaitForSingleObject(&ev, Executive, KernelMode, FALSE, NULL);
        /* IRP pinned by the completion routine's STATUS_MORE_PROCESSING_REQUIRED — IoCallDriver
           did not free it, so reading IoStatus after the wait is the WDM synchronous-forward pattern. */
        /* cppcheck-suppress deallocuse */
        status = irp->IoStatus.Status;
    }
    return status;
}

/* Bring the volume up: allocate the backing store, format it, and publish the
 * Mount Manager interface so mountmgr assigns a drive letter. [src: WDM-PnP, MountMgr] */
static NTSTATUS RamStartDevice(PRAM_EXT ext)
{
    NTSTATUS status;

    /* Idempotent across a STOP->START cycle: STOP keeps the backing store (there
     * are no hardware resources to release), so a re-START must not re-allocate
     * and leak the previous buffer and interface. */
    if (ext->Image) {
        ext->Started = TRUE;
        return STATUS_SUCCESS;
    }

    RamReadParameters(ext);

    ext->Image = ExAllocatePoolWithTag(NonPagedPool, ext->Size, RAM_TAG);
    if (!ext->Image) {
        RAM_DBG("NonPagedPool alloc of %u bytes FAILED\n", ext->Size);
        return STATUS_INSUFFICIENT_RESOURCES;
    }
    RtlZeroMemory(ext->Image, ext->Size);
    RamFormatFat16(ext->Image, ext->Size, ext->SectorsPerCluster);

    /* Register the Mount Manager interface (on the PDO, which IoRegisterDeviceInterface
     * requires) and enable it so mountmgr letters the volume. Lettering is the whole
     * point of the driver, so a failure here fails the start — surfacing as a devnode
     * Problem instead of a silent debug print; REMOVE then frees the allocation. */
    status = IoRegisterDeviceInterface(ext->Pdo, &MOUNTDEV_MOUNTED_DEVICE_GUID,
                                       NULL, &ext->InterfaceName);
    if (NT_SUCCESS(status)) {
        ext->InterfaceRegistered = TRUE;
        status = IoSetDeviceInterfaceState(&ext->InterfaceName, TRUE);
    }
    if (!NT_SUCCESS(status)) {
        RAM_DBG("Mount Manager interface registration failed 0x%08x, failing start\n", status);
        return status;
    }

    ext->Started = TRUE;
    RAM_DBG("started, %u bytes, FAT16 super-floppy ready\n", ext->Size);
    return STATUS_SUCCESS;
}

/* Stop new opens: disable the lettering interface and mark the volume not-ready.
 * Safe to call before the remove lock drains — it does not touch the backing
 * store that in-flight read/write IRPs use. */
static VOID RamDisableInterface(PRAM_EXT ext)
{
    if (ext->InterfaceRegistered) {
        IoSetDeviceInterfaceState(&ext->InterfaceName, FALSE);
        ext->InterfaceRegistered = FALSE;
    }
    ext->Started = FALSE;
}

/* Free the backing store and the interface name. MUST run only after the remove
 * lock has drained, so no in-flight I/O can still touch ext->Image. */
static VOID RamFreeResources(PRAM_EXT ext)
{
    if (ext->InterfaceName.Buffer) {
        RtlFreeUnicodeString(&ext->InterfaceName);
        ext->InterfaceName.Buffer = NULL;
    }
    if (ext->Image) {
        ExFreePoolWithTag(ext->Image, RAM_TAG);
        ext->Image = NULL;
    }
}

NTSTATUS NTAPI RamPnp(PDEVICE_OBJECT dev, PIRP irp)
{
    PRAM_EXT ext = dev->DeviceExtension;
    PIO_STACK_LOCATION sp = IoGetCurrentIrpStackLocation(irp);
    NTSTATUS status;

    status = IoAcquireRemoveLock(&ext->RemoveLock, irp);
    if (!NT_SUCCESS(status)) {
        irp->IoStatus.Status = status;
        IoCompleteRequest(irp, IO_NO_INCREMENT);
        return status;
    }

    switch (sp->MinorFunction) {
    case IRP_MN_START_DEVICE:
        /* Let the stack below start first, then bring the volume up. [src: WDM-PnP] */
        status = RamForwardWait(ext, irp);
        if (NT_SUCCESS(status))
            status = RamStartDevice(ext);
        irp->IoStatus.Status = status;
        IoCompleteRequest(irp, IO_NO_INCREMENT);
        IoReleaseRemoveLock(&ext->RemoveLock, irp);
        return status;

    case IRP_MN_QUERY_CAPABILITIES:
        /* Amend (don't synthesize) the PDO's capabilities. [src: WDM-PnP] */
        status = RamForwardWait(ext, irp);
        if (NT_SUCCESS(status)) {
            PDEVICE_CAPABILITIES caps = sp->Parameters.DeviceCapabilities.Capabilities;
            caps->Removable = FALSE;
            caps->SurpriseRemovalOK = TRUE;
            caps->SilentInstall = TRUE;
            caps->UniqueID = TRUE;
        }
        irp->IoStatus.Status = status;
        IoCompleteRequest(irp, IO_NO_INCREMENT);
        IoReleaseRemoveLock(&ext->RemoveLock, irp);
        return status;

    case IRP_MN_QUERY_REMOVE_DEVICE:
    case IRP_MN_QUERY_STOP_DEVICE:
    case IRP_MN_CANCEL_REMOVE_DEVICE:
    case IRP_MN_CANCEL_STOP_DEVICE:
    case IRP_MN_STOP_DEVICE:
        /* Nothing is pinned; succeed and pass down. [src: WDM-PnP] */
        irp->IoStatus.Status = STATUS_SUCCESS;
        IoSkipCurrentIrpStackLocation(irp);
        status = IoCallDriver(ext->Lower, irp);
        IoReleaseRemoveLock(&ext->RemoveLock, irp);
        return status;

    case IRP_MN_SURPRISE_REMOVAL:
        /* Stop new opens; leave the backing store for REMOVE to free after the
         * remove lock drains (surprise-removal cannot wait for in-flight I/O). */
        RamDisableInterface(ext);
        irp->IoStatus.Status = STATUS_SUCCESS;
        IoSkipCurrentIrpStackLocation(irp);
        status = IoCallDriver(ext->Lower, irp);
        IoReleaseRemoveLock(&ext->RemoveLock, irp);
        return status;

    case IRP_MN_REMOVE_DEVICE:
        /* Stop new opens, pass the IRP down, then drain in-flight I/O before
         * freeing the backing store — otherwise a read/write still in flight
         * would touch freed pool. */
        RamDisableInterface(ext);
        irp->IoStatus.Status = STATUS_SUCCESS;
        IoSkipCurrentIrpStackLocation(irp);
        status = IoCallDriver(ext->Lower, irp);
        IoReleaseRemoveLockAndWait(&ext->RemoveLock, irp);
        RamFreeResources(ext);
        IoDetachDevice(ext->Lower);
        IoDeleteDevice(ext->Fdo);
        return status;

    default:
        IoSkipCurrentIrpStackLocation(irp);
        status = IoCallDriver(ext->Lower, irp);
        IoReleaseRemoveLock(&ext->RemoveLock, irp);
        return status;
    }
}

NTSTATUS NTAPI RamPower(PDEVICE_OBJECT dev, PIRP irp)
{
    PRAM_EXT ext = dev->DeviceExtension;
    NTSTATUS status = IoAcquireRemoveLock(&ext->RemoveLock, irp);
    if (!NT_SUCCESS(status)) {
        PoStartNextPowerIrp(irp);
        irp->IoStatus.Status = status;
        IoCompleteRequest(irp, IO_NO_INCREMENT);
        return status;
    }
    PoStartNextPowerIrp(irp);
    IoSkipCurrentIrpStackLocation(irp);
    status = PoCallDriver(ext->Lower, irp);
    IoReleaseRemoveLock(&ext->RemoveLock, irp);
    return status;
}

NTSTATUS NTAPI RamSystemControl(PDEVICE_OBJECT dev, PIRP irp)
{
    PRAM_EXT ext = dev->DeviceExtension;
    NTSTATUS status = IoAcquireRemoveLock(&ext->RemoveLock, irp);
    if (!NT_SUCCESS(status)) {
        irp->IoStatus.Status = status;
        IoCompleteRequest(irp, IO_NO_INCREMENT);
        return status;
    }
    IoSkipCurrentIrpStackLocation(irp);
    status = IoCallDriver(ext->Lower, irp);
    IoReleaseRemoveLock(&ext->RemoveLock, irp);
    return status;
}

/* ---- AddDevice / init ------------------------------------------------- */

NTSTATUS NTAPI RamAddDevice(PDRIVER_OBJECT drv, PDEVICE_OBJECT pdo)
{
    UNICODE_STRING name;
    PDEVICE_OBJECT fdo;
    PRAM_EXT ext;
    NTSTATUS status;

    RtlInitUnicodeString(&name, DEVICE_NAME);
    status = IoCreateDevice(drv, sizeof(RAM_EXT), &name, FILE_DEVICE_DISK,
                            FILE_DEVICE_SECURE_OPEN, FALSE, &fdo);
    if (!NT_SUCCESS(status)) {
        RAM_DBG("IoCreateDevice failed 0x%08x\n", status);
        return status;
    }

    ext = fdo->DeviceExtension;
    RtlZeroMemory(ext, sizeof(*ext));
    ext->Fdo = fdo;
    ext->Pdo = pdo;
    ext->Size = DEFAULT_SIZE;
    IoInitializeRemoveLock(&ext->RemoveLock, RAM_TAG, 0, 0);

    /* Keep our own copy of the device name for IOCTL_MOUNTDEV_QUERY_DEVICE_NAME. */
    RtlCopyMemory(ext->DeviceNameBuf, DEVICE_NAME, sizeof(DEVICE_NAME));
    ext->DeviceName.Buffer = ext->DeviceNameBuf;
    ext->DeviceName.Length = (USHORT)(sizeof(DEVICE_NAME) - sizeof(WCHAR));
    ext->DeviceName.MaximumLength = sizeof(ext->DeviceNameBuf);

    ext->Lower = IoAttachDeviceToDeviceStack(fdo, pdo);
    if (!ext->Lower) {
        RAM_DBG("IoAttachDeviceToDeviceStack failed\n");
        IoDeleteDevice(fdo);
        return STATUS_NO_SUCH_DEVICE;
    }

    fdo->Flags |= DO_DIRECT_IO | DO_POWER_PAGABLE;
    fdo->Flags &= ~DO_DEVICE_INITIALIZING;
    RAM_DBG("AddDevice complete\n");
    return STATUS_SUCCESS;
}

VOID NTAPI RamUnload(PDRIVER_OBJECT drv)
{
    UNREFERENCED_PARAMETER(drv);
    if (gRegPath.Buffer)
        gRegPath.Buffer = NULL;
}

NTSTATUS NTAPI DriverEntry(PDRIVER_OBJECT drv, PUNICODE_STRING regPath)
{
    ULONG i;

    RAM_DBG("DriverEntry\n");

    /* Capture the service registry path so START_DEVICE can read Parameters. */
    if (regPath && regPath->Buffer && regPath->Length < sizeof(gRegPathBuf)) {
        RtlCopyMemory(gRegPathBuf, regPath->Buffer, regPath->Length);
        gRegPath.Buffer = gRegPathBuf;
        gRegPath.Length = regPath->Length;
        gRegPath.MaximumLength = sizeof(gRegPathBuf);
    }

    for (i = 0; i <= IRP_MJ_MAXIMUM_FUNCTION; i++)
        drv->MajorFunction[i] = RamInvalid;
    drv->MajorFunction[IRP_MJ_CREATE]         = RamCreateClose;
    drv->MajorFunction[IRP_MJ_CLOSE]          = RamCreateClose;
    drv->MajorFunction[IRP_MJ_CLEANUP]        = RamCreateClose;
    drv->MajorFunction[IRP_MJ_FLUSH_BUFFERS]  = RamCreateClose;
    drv->MajorFunction[IRP_MJ_SHUTDOWN]       = RamCreateClose;
    drv->MajorFunction[IRP_MJ_READ]           = RamReadWrite;
    drv->MajorFunction[IRP_MJ_WRITE]          = RamReadWrite;
    drv->MajorFunction[IRP_MJ_DEVICE_CONTROL] = RamDeviceControl;
    drv->MajorFunction[IRP_MJ_PNP]            = RamPnp;
    drv->MajorFunction[IRP_MJ_POWER]          = RamPower;
    drv->MajorFunction[IRP_MJ_SYSTEM_CONTROL] = RamSystemControl;
    drv->DriverExtension->AddDevice           = RamAddDevice;
    drv->DriverUnload                         = RamUnload;

    return STATUS_SUCCESS;
}
