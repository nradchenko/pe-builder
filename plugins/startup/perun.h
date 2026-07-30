/* Copyright 2026 Nikita Radchenko
 * SPDX-License-Identifier: Apache-2.0
 *
 * perun — the startup run-list walker. Reads the registry contract at
 * HKLM\SOFTWARE\pe-builder\Startup, orders the tasks of one stage, runs each as a
 * separate process, and records a verdict for every one. See README.md.
 *
 * This header is the interface peshell binds to; perun.c is the only implementation,
 * compiled into both perun.dll and perun.exe.
 */

#ifndef PERUN_H
#define PERUN_H

#include <windows.h>

/* Task outcomes. The terminal four are the contract's Result codes verbatim, so a
 * progress callback and a `hive dump` of the status key speak the same language. */
#define PERUN_BEGIN   (-1) /* about to run; not a Result */
#define PERUN_OK        0
#define PERUN_FAILED    1
#define PERUN_TIMEOUT   2
#define PERUN_SKIPPED   3

/* Progress, reported once with PERUN_BEGIN and once with the outcome, per task.
 * index is 0-based; err is the task's exit code (or a Win32 error when the process
 * could not be started at all). */
typedef void (*PERUN_PROGRESS)(void *ctx, const wchar_t *task, const wchar_t *label,
                               int index, int count, int state, DWORD err);

/* Declaration enumeration, for front ends that want to show or check the resolved list
 * without running it. Carries the whole declaration, which PERUN_PROGRESS deliberately
 * does not — progress is for a splash screen, this is for `--list`. */
typedef void (*PERUN_ENUM)(void *ctx, const wchar_t *task, const wchar_t *stage,
                           DWORD order, const wchar_t *label, const wchar_t *command,
                           DWORD required, DWORD timeout, int index, int count);

#ifdef PERUN_EXPORTS
#define PERUN_API __declspec(dllexport)
#else
#define PERUN_API
#endif

/* Run every task of one stage, in order. Returns ERROR_SUCCESS when the walk itself ran
 * — including when tasks failed, since a failed task must never cost the user their boot
 * (see README.md § A required task that fails). A non-zero return means the contract
 * could not be read at all. An absent contract key is not an error: it is an empty list.
 *
 * If pFailedRequired is non-NULL it receives the number of tasks that failed *and* were
 * declared Required, which is the only failure a caller normally reacts to. */
PERUN_API DWORD PerunRunStage(const wchar_t *stage, PERUN_PROGRESS cb, void *ctx,
                              int *pFailedRequired);

/* Run one task by name, whatever stage it declares. The debugging entry point: it is what
 * makes a task re-runnable by hand in a single boot. Returns ERROR_FILE_NOT_FOUND when no
 * task of that name is declared, so a typo cannot look like a clean run. */
PERUN_API DWORD PerunRunTask(const wchar_t *task, PERUN_PROGRESS cb, void *ctx,
                             int *pFailedRequired);

/* Report the resolved declarations of one stage (NULL = every stage) without running
 * anything. The cheap way to check a plugin's declaration landed. */
PERUN_API DWORD PerunEnumTasks(const wchar_t *stage, PERUN_ENUM cb, void *ctx);

#endif /* PERUN_H */
