/* Copyright 2026 Nikita Radchenko
 * SPDX-License-Identifier: Apache-2.0
 *
 * perun.exe — the command-line front end to the startup run-list walker.
 *
 * It exists for iteration, not for console builds: tasks are separate processes so they
 * can be re-run by hand within one boot, and this is what re-runs them. That is wanted
 * most on a *desktop* build, where the shell has already walked the list once — open a
 * command prompt, `perun --list`, `perun --task net`, repeat, no reboot. Console builds
 * get their boot-time walk from the same binary for free. See README.md.
 */

#include <windows.h>
#include <shellapi.h>
#include <stdlib.h>

#include "perun.h"

typedef struct {
    int ok, failed, timedOut, skipped;
} TALLY;

/* Console output without the CRT's wide-stream behaviour, which mangles non-ASCII on a
 * PE console. WriteConsoleW when we are attached to a console; a code-page conversion
 * when output is redirected to a file or a pipe, where WriteConsoleW writes nothing. */
static void outraw(const wchar_t *s, int cch)
{
    HANDLE h = GetStdHandle(STD_OUTPUT_HANDLE);
    DWORD mode, written;

    if (cch <= 0)
        return;
    if (GetConsoleMode(h, &mode)) {
        WriteConsoleW(h, s, (DWORD)cch, &written, NULL);
    } else {
        char *narrow;
        int n = WideCharToMultiByte(CP_OEMCP, 0, s, cch, NULL, 0, NULL, NULL);

        if (n <= 0)
            return;
        narrow = (char *)malloc((size_t)n);
        if (!narrow)
            return;
        if (WideCharToMultiByte(CP_OEMCP, 0, s, cch, narrow, n, NULL, NULL) > 0)
            WriteFile(h, narrow, (DWORD)n, &written, NULL);
        free(narrow);
    }
}

/* Write a string of any length, unformatted. wvsprintfW below caps at 1024 characters, and a
 * Command has no useful bound — formatting one through it truncated the line ending and ran
 * the next entry onto the same line. */
static void outs(const wchar_t *s)
{
    outraw(s, lstrlenW(s));
}

static void outw(const wchar_t *fmt, ...)
{
    wchar_t buf[1024];
    va_list ap;
    int cch;

    va_start(ap, fmt);
    cch = wvsprintfW(buf, fmt, ap);
    va_end(ap);
    outraw(buf, cch);
}

static const wchar_t *stateWord(int state)
{
    switch (state) {
    case PERUN_OK:      return L"ok";
    case PERUN_FAILED:  return L"FAILED";
    case PERUN_TIMEOUT: return L"TIMED OUT";
    case PERUN_SKIPPED: return L"skipped";
    default:            return L"?";
    }
}

static void onProgress(void *ctx, const wchar_t *task, const wchar_t *label,
                       int index, int count, int state, DWORD err)
{
    TALLY *t = (TALLY *)ctx;

    if (state == PERUN_BEGIN) {
        outw(L"[%d/%d] %s: %s\r\n", index + 1, count, task, label);
        return;
    }
    switch (state) {
    case PERUN_OK:      t->ok++;       break;
    case PERUN_FAILED:  t->failed++;   break;
    case PERUN_TIMEOUT: t->timedOut++; break;
    default:            t->skipped++;  break;
    }
    if (state == PERUN_OK)
        outw(L"        %s\r\n", stateWord(state));
    else
        outw(L"        %s (error %u)\r\n", stateWord(state), err);
}

static void onEnum(void *ctx, const wchar_t *task, const wchar_t *stage, DWORD order,
                   const wchar_t *label, const wchar_t *command, DWORD required,
                   DWORD timeout, int index, int count)
{
    (void)ctx; (void)label; (void)index; (void)count; (void)timeout;

    /* The command is streamed separately — it is the one unbounded field here. */
    outw(L"0x%04x  %-12s  %-10s  %s", order, stage, task, required ? L"[required] " : L"");
    outs(command);
    outs(L"\r\n");
}

static void usage(void)
{
    outw(L"perun - run the pe-builder startup task list\r\n\r\n"
         L"  perun --stage <pre-shell|post-shell>   run one stage's tasks, in order\r\n"
         L"  perun --task <name>                    re-run a single task by name\r\n"
         L"  perun --list [stage]                   show the resolved list, run nothing\r\n\r\n"
         L"Tasks are declared under HKLM\\SOFTWARE\\pe-builder\\Startup and each task's\r\n"
         L"verdict is written back to its own key.\r\n");
}

int main(void)
{
    wchar_t **argv;
    int argc = 0, failedRequired = 0;
    TALLY tally = {0, 0, 0, 0};
    DWORD rc;

    argv = CommandLineToArgvW(GetCommandLineW(), &argc);
    if (!argv)
        return 2;

    if (argc < 2) {
        usage();
        return 2;
    }

    if (lstrcmpiW(argv[1], L"--list") == 0) {
        /* A bare --list spans every stage; naming one narrows it. */
        rc = PerunEnumTasks(argc > 2 ? argv[2] : NULL, onEnum, NULL);
        if (rc != ERROR_SUCCESS) {
            outw(L"perun: cannot read the task list (error %u)\r\n", rc);
            return 2;
        }
        return 0;
    }

    if (argc >= 3 && lstrcmpiW(argv[1], L"--stage") == 0) {
        rc = PerunRunStage(argv[2], onProgress, &tally, &failedRequired);
        /* An empty stage is legitimate — post-shell has no tasks until some plugin declares
         * one — so this is not an error, unlike an unmatched --task name. But say so out
         * loud, or a mistyped stage is indistinguishable from a stage with nothing in it. */
        if (rc == ERROR_SUCCESS
            && tally.ok + tally.failed + tally.timedOut + tally.skipped == 0) {
            outw(L"perun: no tasks are declared for stage \"%s\"\r\n", argv[2]);
            return 0;
        }
    } else if (argc >= 3 && lstrcmpiW(argv[1], L"--task") == 0) {
        rc = PerunRunTask(argv[2], onProgress, &tally, &failedRequired);
        if (rc == (DWORD)ERROR_FILE_NOT_FOUND) {
            outw(L"perun: no task named \"%s\" is declared\r\n", argv[2]);
            return 2;
        }
    } else {
        usage();
        return 2;
    }

    if (rc != ERROR_SUCCESS) {
        outw(L"perun: cannot read the task list (error %u)\r\n", rc);
        return 2;
    }

    outw(L"\r\n%d ok, %d failed, %d timed out, %d skipped\r\n",
         tally.ok, tally.failed, tally.timedOut, tally.skipped);

    /* Only a *required* failure is worth a non-zero exit: a best-effort task that failed
     * is reported, but the caller carries on — as the boot does. */
    return failedRequired > 0 ? 1 : 0;
}
