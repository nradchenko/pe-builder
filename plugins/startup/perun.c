/* Copyright 2026 Nikita Radchenko
 * SPDX-License-Identifier: Apache-2.0
 *
 * perun — the startup run-list walker. Reads the registry contract, orders one stage's
 * tasks, runs each as a separate process, and records a verdict for every one.
 *
 * The only implementation of the list semantics: perun.dll and perun.exe are both this
 * file plus a front end, so the two cannot drift. See README.md.
 */

#include <windows.h>
#include <stdlib.h>
#include <wchar.h>

#include "perun.h"

#define CONTRACT_KEY L"SOFTWARE\\pe-builder\\Startup"

/* Where a malformed entry that never declared a Stage gets reported. Both front ends walk
 * pre-shell, so it surfaces exactly once per boot. */
#define REPORT_STAGE L"pre-shell"

/* A registry key name runs to 255 characters. Sizing for the maximum means a long task name
 * is never a partial read — which RegEnumKeyExW reports as ERROR_MORE_DATA, and which used to
 * be indistinguishable from the end of the list. */
#define NAME_CCH     256
#define STAGE_CCH     32
#define LABEL_CCH    128
#define MSG_CB       256 /* a Message is a status line, not a log */

#define DEFAULT_TIMEOUT 120
/* A day. Timeout is a registry DWORD an author controls, and it gets multiplied into
 * milliseconds in 32-bit arithmetic: without a clamp a value near a multiple of 4294967
 * wraps to a tiny limit, so declaring a very long timeout would kill the task almost at
 * once — the exact opposite of what it asks for. */
#define MAX_TIMEOUT (24 * 60 * 60)

typedef struct {
    wchar_t  name[NAME_CCH];
    wchar_t  stage[STAGE_CCH];
    wchar_t  label[LABEL_CCH];
    wchar_t *command; /* heap; a command line has no useful bound */
    DWORD    order;
    DWORD    required;
    DWORD    timeout; /* seconds */
    int      valid;   /* 0 = malformed declaration; runs as PERUN_SKIPPED */
} TASK;

/* ---------------------------------------------------------------- registry helpers */

/* Read a REG_SZ of unbounded length onto the heap. Caller frees. */
static wchar_t *regStrAlloc(HKEY k, const wchar_t *name)
{
    DWORD type = 0, cb = 0;
    wchar_t *p;

    if (RegQueryValueExW(k, name, NULL, &type, NULL, &cb) != ERROR_SUCCESS || cb == 0)
        return NULL;
    if (type != REG_SZ && type != REG_EXPAND_SZ)
        return NULL;

    p = (wchar_t *)calloc(cb + sizeof(wchar_t), 1);
    if (!p)
        return NULL;
    if (RegQueryValueExW(k, name, NULL, &type, (BYTE *)p, &cb) != ERROR_SUCCESS) {
        free(p);
        return NULL;
    }
    p[cb / sizeof(wchar_t)] = 0;
    if (!p[0]) {
        free(p);
        return NULL;
    }
    return p;
}

/* Read a REG_SZ into a fixed buffer, truncating anything longer.
 *
 * Read through the heap rather than straight into the caller's buffer: RegQueryValueExW
 * leaves the buffer's contents *undefined* when it returns ERROR_MORE_DATA, so a too-small
 * read cannot simply be accepted as a partial one. Discarding the value instead would be
 * worse than truncating — an over-long Stage would make a fully-declared task report
 * "malformed declaration (needs Stage and Command)", which is an actively wrong diagnosis. */
static BOOL regStr(HKEY k, const wchar_t *name, wchar_t *buf, DWORD cch)
{
    wchar_t *p = regStrAlloc(k, name);

    buf[0] = 0;
    if (!p)
        return FALSE;
    lstrcpynW(buf, p, (int)cch);
    free(p);
    return buf[0] != 0;
}

/* Expand %SystemRoot% and friends, taking ownership of s and freeing it on replacement.
 * Every command-path contract in this project is authored as REG_EXPAND_SZ with a
 * %SystemRoot% path, so a Command left unexpanded fails with ERROR_FILE_NOT_FOUND and
 * records the literal string as its own epitaph. See README.md § Command. */
static wchar_t *expandAlloc(wchar_t *s)
{
    wchar_t *p;
    DWORD n;

    if (!s)
        return NULL;
    n = ExpandEnvironmentStringsW(s, NULL, 0); /* characters, including the terminator */
    if (n == 0)
        return s; /* nothing we can do; run it as written */
    p = (wchar_t *)calloc(n + 1, sizeof(wchar_t));
    if (!p)
        return s;
    if (ExpandEnvironmentStringsW(s, p, n) == 0) {
        free(p);
        return s;
    }
    free(s);
    return p;
}

static DWORD regDword(HKEY k, const wchar_t *name, DWORD dflt)
{
    DWORD type = 0, v = 0, cb = sizeof(v);

    if (RegQueryValueExW(k, name, NULL, &type, (BYTE *)&v, &cb) != ERROR_SUCCESS)
        return dflt;
    if (type != REG_DWORD || cb != sizeof(v))
        return dflt;
    return v;
}

/* ------------------------------------------------------------------------- loading */

static int byOrderThenName(const void *a, const void *b)
{
    const TASK *x = (const TASK *)a, *y = (const TASK *)b;

    if (x->order < y->order)
        return -1;
    if (x->order > y->order)
        return 1;
    /* Ties break on name so the order is total and stable, not enumeration order. */
    return lstrcmpiW(x->name, y->name);
}

/* Load the declarations matching stage (NULL = any) and only (NULL = any), sorted. On success
 * returns the count and stores a heap array in *out, which the caller releases with freeTasks.
 * Returns -1 with *err set when the contract key could not be opened — *err is
 * ERROR_FILE_NOT_FOUND when it simply does not exist, which callers read as an empty list.
 *
 * The array grows rather than filling a fixed one: a run list that outgrew a cap used to lose
 * its tail silently, which is the one failure a task list must never have. */
static int loadTasks(const wchar_t *stage, const wchar_t *only, TASK **out, DWORD *err)
{
    HKEY root;
    TASK *v = NULL;
    DWORD i;
    int n = 0, cap = 0;
    LONG rc;

    *out = NULL;
    *err = ERROR_SUCCESS;

    rc = RegOpenKeyExW(HKEY_LOCAL_MACHINE, CONTRACT_KEY, 0, KEY_READ, &root);
    if (rc != ERROR_SUCCESS) {
        *err = (DWORD)rc;
        return -1;
    }

    for (i = 0;; i++) {
        wchar_t sub[NAME_CCH];
        DWORD cch = NAME_CCH;
        HKEY k;
        TASK t;

        rc = RegEnumKeyExW(root, i, sub, &cch, NULL, NULL, NULL, NULL);
        if (rc == ERROR_NO_MORE_ITEMS)
            break;
        /* Skip only the entry we could not read. Breaking here would drop every task after
         * it as well, turning one malformed key into an empty list. */
        if (rc != ERROR_SUCCESS)
            continue;
        if (only && lstrcmpiW(sub, only) != 0)
            continue;
        if (RegOpenKeyExW(root, sub, 0, KEY_READ, &k) != ERROR_SUCCESS)
            continue;

        ZeroMemory(&t, sizeof(t));
        lstrcpynW(t.name, sub, NAME_CCH);
        t.valid = 1;

        /* Stage and Command are the declaration; neither is defaulted. Guessing a stage
         * would run hardware configuration at the wrong moment, and a task with no command
         * is a typo in some plugin's AddReg — both must surface, not vanish. */
        if (!regStr(k, L"Stage", t.stage, STAGE_CCH))
            t.valid = 0;
        t.command = expandAlloc(regStrAlloc(k, L"Command"));
        if (!t.command)
            t.valid = 0;

        if (!regStr(k, L"Label", t.label, LABEL_CCH))
            lstrcpynW(t.label, t.name, LABEL_CCH);
        t.order    = regDword(k, L"Order", 0);
        t.required = regDword(k, L"Required", 0);
        t.timeout  = regDword(k, L"Timeout", DEFAULT_TIMEOUT);
        if (t.timeout == 0)
            t.timeout = DEFAULT_TIMEOUT;
        if (t.timeout > MAX_TIMEOUT)
            t.timeout = MAX_TIMEOUT;

        RegCloseKey(k);

        /* A malformed entry is kept, so it can be reported as skipped rather than being
         * silently absent — but it can only be stage-filtered if it declared a stage. */
        if (stage && t.valid && lstrcmpiW(t.stage, stage) != 0) {
            free(t.command);
            continue;
        }
        if (stage && !t.valid) {
            /* A malformed entry still has to surface — but once per boot, not once per stage.
             * One that omitted Stage matches nothing, so it is reported against REPORT_STAGE,
             * the stage both front ends always walk; otherwise it would be announced, and have
             * its status rewritten, on every stage of the same boot. */
            const wchar_t *home = t.stage[0] ? t.stage : REPORT_STAGE;

            if (lstrcmpiW(home, stage) != 0) {
                free(t.command);
                continue;
            }
        }

        if (n == cap) {
            int ncap = cap ? cap * 2 : 16;
            TASK *nv = (TASK *)realloc(v, (size_t)ncap * sizeof(TASK));

            if (!nv) {
                free(t.command);
                break;
            }
            v = nv;
            cap = ncap;
        }
        v[n++] = t;
    }
    RegCloseKey(root);

    /* Guarded: with no matching task v is still NULL, and passing a null pointer to qsort is
     * undefined even for a count of zero. That is the ordinary case, not a corner one — a
     * stage a build declares nothing for reaches here on every boot. */
    if (n > 0)
        qsort(v, (size_t)n, sizeof(TASK), byOrderThenName);
    *out = v;
    return n;
}

static void freeTasks(TASK *t, int n)
{
    int i;

    for (i = 0; i < n; i++)
        free(t[i].command);
    free(t);
}

/* -------------------------------------------------------------------------- status */

/* Write the verdict, under the same key as the declaration. Written by the runner and not
 * the task, so a task that crashed or was killed still gets one. Only the four values we
 * own are touched: a task's own counters under this key survive. */
static void writeStatus(const wchar_t *task, int state, DWORD err, const wchar_t *msg)
{
    wchar_t path[NAME_CCH + 64], stamp[32];
    HKEY k;
    SYSTEMTIME st;
    DWORD v;

    wsprintfW(path, L"%s\\%s", CONTRACT_KEY, task);
    if (RegCreateKeyExW(HKEY_LOCAL_MACHINE, path, 0, NULL, 0, KEY_SET_VALUE, NULL, &k, NULL)
        != ERROR_SUCCESS)
        return;

    v = (DWORD)state;
    RegSetValueExW(k, L"Result", 0, REG_DWORD, (const BYTE *)&v, sizeof(v));
    RegSetValueExW(k, L"Error", 0, REG_DWORD, (const BYTE *)&err, sizeof(err));

    GetLocalTime(&st);
    wsprintfW(stamp, L"%04u-%02u-%02u %02u:%02u:%02u", st.wYear, st.wMonth, st.wDay,
              st.wHour, st.wMinute, st.wSecond);
    RegSetValueExW(k, L"LastRun", 0, REG_SZ, (const BYTE *)stamp,
                   (DWORD)((lstrlenW(stamp) + 1) * sizeof(wchar_t)));

    /* Deleted rather than left alone when there is nothing to say: a task that failed printing
     * "no adapter found", was fixed, and now succeeds quietly would otherwise read Result=0
     * beside the old complaint. */
    if (msg && msg[0])
        RegSetValueExW(k, L"Message", 0, REG_SZ, (const BYTE *)msg,
                       (DWORD)((lstrlenW(msg) + 1) * sizeof(wchar_t)));
    else
        RegDeleteValueW(k, L"Message");

    RegCloseKey(k);
}

/* ------------------------------------------------------------------------- running */

/* Keep the current line as the last line, if it held anything but blanks. */
static void commitLine(const char *cur, int *curLen, char *last, int lastCb)
{
    int i, blank = 1;

    for (i = 0; i < *curLen; i++)
        if (cur[i] != ' ' && cur[i] != '\t') {
            blank = 0;
            break;
        }
    if (!blank) {
        int n = *curLen < lastCb - 1 ? *curLen : lastCb - 1;
        CopyMemory(last, cur, (size_t)n);
        last[n] = 0;
    }
    *curLen = 0;
}

/* Drain whatever the child has written, keeping the last non-blank line.
 *
 * This has to happen *while* waiting, not after: the pipe buffer is a few KB and a child
 * that fills it blocks forever if nobody is reading — the classic capture deadlock, which
 * a chatty task would hit every boot. PeekNamedPipe sizes each read so we never block
 * either.
 *
 * Bounded, and that bound is load-bearing. The deadline lives in the caller's wait loop, so
 * a task that keeps the pipe non-empty would hold an unbounded drain forever, Timeout would
 * never be evaluated, and the tree kill would never fire — for exactly the chatty task this
 * exists to serve. The post-exit drain is worse: it runs after the process wait is over, so
 * a grandchild still holding the inherited write end would wedge the walk outright. Each
 * call therefore moves at most DRAIN_ROUNDS * sizeof(buf) and returns to the caller, which
 * re-enters it after re-checking the clock. */
#define DRAIN_ROUNDS 64
static void drain(HANDLE rd, char *cur, int *curLen, char *last, int lastCb)
{
    int rounds;

    for (rounds = 0; rounds < DRAIN_ROUNDS; rounds++) {
        DWORD avail = 0, want, got = 0;
        char buf[512];
        DWORD i;

        if (!PeekNamedPipe(rd, NULL, 0, NULL, &avail, NULL) || avail == 0)
            return;
        want = avail > sizeof(buf) ? (DWORD)sizeof(buf) : avail;
        if (!ReadFile(rd, buf, want, &got, NULL) || got == 0)
            return;

        for (i = 0; i < got; i++) {
            char c = buf[i];

            if (c == '\r')
                continue;
            if (c == '\n') {
                commitLine(cur, curLen, last, lastCb);
                continue;
            }
            if (*curLen < MSG_CB - 1)
                cur[(*curLen)++] = c;
        }
    }
}

/* Run one task to completion, a timeout, or a failure to start. Returns a PERUN_* outcome
 * and fills msg with the last line the task printed. */
static int runTask(const TASK *t, DWORD *exitCode, wchar_t *msg, int msgCch)
{
    SECURITY_ATTRIBUTES sa;
    STARTUPINFOW si;
    PROCESS_INFORMATION pi;
    HANDLE rd = NULL, wr = NULL, job = NULL, nul = INVALID_HANDLE_VALUE;
    wchar_t *cmd;
    char cur[MSG_CB], last[MSG_CB];
    int curLen = 0, state = PERUN_OK;
    DWORD start, limit;

    *exitCode = 0;
    msg[0] = 0;
    last[0] = 0;

    ZeroMemory(&sa, sizeof(sa));
    sa.nLength = sizeof(sa);
    sa.bInheritHandle = TRUE;
    if (!CreatePipe(&rd, &wr, &sa, 0)) {
        *exitCode = GetLastError();
        return PERUN_FAILED;
    }
    /* Only the write end crosses into the child; an inheritable read end would keep the
     * pipe open after the child exits and the drain would never see EOF. */
    SetHandleInformation(rd, HANDLE_FLAG_INHERIT, 0);

    /* A job object, so a timeout can sweep the task's whole process tree rather than just
     * the process we started. TerminateProcess kills one process; a task shaped like
     * "cmd.exe /c ..." is a tree, and killing the shell would leave the wedged grandchild
     * running — still holding this pipe, still doing whatever wedged it, while the rest of
     * the list runs and the status key claims the task is over.
     *
     * A job is a best effort: if it cannot be created or assigned — on a pre-Windows-8 kernel
     * a process belongs to at most one job and there are no nested jobs — we still run the
     * task and fall back to TerminateProcess, because losing the tree kill is far better than
     * losing the task. */
    job = CreateJobObjectW(NULL, NULL);

    /* CreateProcessW may modify its command line, so it cannot be the registry buffer. */
    cmd = _wcsdup(t->command);
    if (!cmd) {
        if (job)
            CloseHandle(job);
        CloseHandle(rd);
        CloseHandle(wr);
        *exitCode = (DWORD)ERROR_OUTOFMEMORY;
        return PERUN_FAILED;
    }

    ZeroMemory(&si, sizeof(si));
    si.cb = sizeof(si);
    si.dwFlags = STARTF_USESTDHANDLES | STARTF_USESHOWWINDOW;
    si.wShowWindow = SW_HIDE;
    si.hStdOutput = wr;
    si.hStdError = wr;
    /* NUL, not our own stdin. Inside peshell there is no console, so STD_INPUT_HANDLE is
     * NULL and a task that prompts would block until its timeout expires; against NUL it
     * reads EOF at once and fails immediately, which is the answer we want. */
    nul = CreateFileW(L"NUL", GENERIC_READ, FILE_SHARE_READ | FILE_SHARE_WRITE, &sa,
                      OPEN_EXISTING, 0, NULL);
    si.hStdInput = nul;
    ZeroMemory(&pi, sizeof(pi));

    /* Suspended, so the task is inside the job before it can run — and therefore before it
     * can spawn anything the job would otherwise miss. */
    if (!CreateProcessW(NULL, cmd, NULL, NULL, TRUE, CREATE_SUSPENDED | CREATE_NO_WINDOW,
                        NULL, NULL, &si, &pi)) {
        *exitCode = GetLastError();
        free(cmd);
        if (job)
            CloseHandle(job);
        if (nul != INVALID_HANDLE_VALUE)
            CloseHandle(nul);
        CloseHandle(rd);
        CloseHandle(wr);
        return PERUN_FAILED;
    }
    free(cmd);
    if (job && !AssignProcessToJobObject(job, pi.hProcess)) {
        /* Already in a job on a pre-Windows-8 kernel is the usual cause; nested jobs do not
         * exist there. Drop the job and run anyway. */
        CloseHandle(job);
        job = NULL;
    }
    ResumeThread(pi.hThread);
    CloseHandle(wr); /* our copy; the child holds the only remaining write end */
    if (nul != INVALID_HANDLE_VALUE)
        CloseHandle(nul); /* likewise: the child holds its own */

    start = GetTickCount();
    limit = t->timeout * 1000;
    for (;;) {
        DWORD w;

        drain(rd, cur, &curLen, last, MSG_CB);
        w = WaitForSingleObject(pi.hProcess, 50);
        if (w == WAIT_OBJECT_0) {
            drain(rd, cur, &curLen, last, MSG_CB);
            break;
        }
        /* Unsigned subtraction, so the 49.7-day GetTickCount wrap is still a correct
         * elapsed time. */
        if (GetTickCount() - start >= limit) {
            /* A hung task must never cost the user their boot — and must not outlive its
             * own verdict either, so take the whole tree where we can. */
            if (job)
                TerminateJobObject(job, (UINT)-1);
            else
                TerminateProcess(pi.hProcess, (UINT)-1);
            WaitForSingleObject(pi.hProcess, 5000);
            drain(rd, cur, &curLen, last, MSG_CB);
            state = PERUN_TIMEOUT;
            break;
        }
    }
    commitLine(cur, &curLen, last, MSG_CB); /* a final line with no newline still counts */

    if (state == PERUN_OK) {
        if (!GetExitCodeProcess(pi.hProcess, exitCode))
            *exitCode = 0;
        if (*exitCode != 0)
            state = PERUN_FAILED;
    }

    CloseHandle(pi.hThread);
    CloseHandle(pi.hProcess);
    CloseHandle(rd);
    /* Closed without KILL_ON_JOB_CLOSE: a task that exited normally may legitimately have
     * left something behind, and only a *timeout* justifies sweeping the tree. */
    if (job)
        CloseHandle(job);

    if (last[0])
        MultiByteToWideChar(CP_OEMCP, 0, last, -1, msg, msgCch);
    return state;
}

/* ---------------------------------------------------------------------------- walks */

static DWORD walk(const wchar_t *stage, const wchar_t *only, PERUN_PROGRESS cb, void *ctx,
                  int *pFailedRequired, int *pRan)
{
    TASK *tasks;
    DWORD loadErr;
    int n, i;

    if (pFailedRequired)
        *pFailedRequired = 0;
    if (pRan)
        *pRan = 0;

    n = loadTasks(stage, only, &tasks, &loadErr);
    if (n < 0) {
        /* An absent contract key is an empty list. Anything else — access denied, a corrupt
         * hive — is a real failure and must not masquerade as "nothing to do". */
        if (loadErr == (DWORD)ERROR_FILE_NOT_FOUND)
            return ERROR_SUCCESS;
        return loadErr;
    }
    if (pRan)
        *pRan = n;

    for (i = 0; i < n; i++) {
        wchar_t msg[MSG_CB];
        DWORD err = 0;
        int state;

        if (cb)
            cb(ctx, tasks[i].name, tasks[i].label, i, n, PERUN_BEGIN, 0);

        msg[0] = 0;
        if (!tasks[i].valid) {
            state = PERUN_SKIPPED;
            lstrcpynW(msg, L"malformed declaration (needs Stage and Command)", MSG_CB);
        } else {
            state = runTask(&tasks[i], &err, msg, MSG_CB);
        }

        writeStatus(tasks[i].name, state, err, msg);
        if (state != PERUN_OK && tasks[i].required && pFailedRequired)
            (*pFailedRequired)++;
        if (cb)
            cb(ctx, tasks[i].name, tasks[i].label, i, n, state, err);
    }

    freeTasks(tasks, n);
    return ERROR_SUCCESS;
}

PERUN_API DWORD PerunRunStage(const wchar_t *stage, PERUN_PROGRESS cb, void *ctx,
                              int *pFailedRequired)
{
    if (!stage || !stage[0])
        return (DWORD)ERROR_INVALID_PARAMETER;
    return walk(stage, NULL, cb, ctx, pFailedRequired, NULL);
}

PERUN_API DWORD PerunRunTask(const wchar_t *task, PERUN_PROGRESS cb, void *ctx,
                             int *pFailedRequired)
{
    DWORD rc;
    int ran = 0;

    if (!task || !task[0])
        return (DWORD)ERROR_INVALID_PARAMETER;
    rc = walk(NULL, task, cb, ctx, pFailedRequired, &ran);
    /* A name that matches nothing is a typo or a declaration that never landed, not a clean
     * run of zero tasks — and this is the entry point people reach for when debugging. */
    if (rc == ERROR_SUCCESS && ran == 0)
        return (DWORD)ERROR_FILE_NOT_FOUND;
    return rc;
}

PERUN_API DWORD PerunEnumStatus(const wchar_t *stage, PERUN_STATUS cb, void *ctx)
{
    TASK *tasks;
    DWORD err;
    int n, i;

    n = loadTasks(stage, NULL, &tasks, &err);
    if (n < 0)
        return err == (DWORD)ERROR_FILE_NOT_FOUND ? ERROR_SUCCESS : err;

    for (i = 0; i < n; i++) {
        wchar_t path[NAME_CCH + 64], lastRun[64], message[MSG_CB];
        DWORD result = 0, taskErr = 0;
        HKEY k;

        lastRun[0] = 0;
        message[0] = 0;
        wsprintfW(path, L"%s\\%s", CONTRACT_KEY, tasks[i].name);
        if (RegOpenKeyExW(HKEY_LOCAL_MACHINE, path, 0, KEY_READ, &k) == ERROR_SUCCESS) {
            result = regDword(k, L"Result", PERUN_NEVER_RUN);
            taskErr = regDword(k, L"Error", 0);
            regStr(k, L"LastRun", lastRun, 64);
            regStr(k, L"Message", message, MSG_CB);
            RegCloseKey(k);
        } else {
            result = PERUN_NEVER_RUN;   /* declared, but no key means no run */
        }
        if (cb)
            cb(ctx, tasks[i].name, result, taskErr, lastRun, message);
    }

    freeTasks(tasks, n);
    return ERROR_SUCCESS;
}

PERUN_API DWORD PerunEnumTasks(const wchar_t *stage, PERUN_ENUM cb, void *ctx)
{
    TASK *tasks;
    DWORD err;
    int n, i;

    n = loadTasks(stage, NULL, &tasks, &err);
    if (n < 0)
        return err == (DWORD)ERROR_FILE_NOT_FOUND ? ERROR_SUCCESS : err;

    for (i = 0; i < n; i++)
        if (cb)
            cb(ctx, tasks[i].name, tasks[i].stage, tasks[i].order, tasks[i].label,
               tasks[i].command ? tasks[i].command : L"", tasks[i].required,
               tasks[i].timeout, i, n);

    freeTasks(tasks, n);
    return ERROR_SUCCESS;
}
