// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

/* startmenu_testmain.c — host test harness for the pure Start-menu planner (startmenu.c).
 * It reads a fixture from stdin and prints the folder/shortcut operations the planner emits,
 * so startmenu_test.go can golden-compare the layout without a Windows target. Not embedded
 * and not part of the plugin build — the Go test compiles it with the host C compiler.
 *
 * Fixture protocol (tab-separated, '#' comments and blank lines ignored):
 *   FD <target> <description>   register a stub FileDescription for an exact raw target
 *   E  <name> <data>            plan one contract entry (data optional)
 * Output:
 *   MKDIR <rel_folder>
 *   LINK  <rel_folder> <label> <target> */
#include "startmenu.c"
#include <stdio.h>
#include <string.h>

#define MAXFD 64
static struct { char target[SM_MAXPATH]; char desc[SM_MAXLABEL]; } g_fd[MAXFD];
static int g_nfd = 0;

/* Stand-in for the version-resource lookup: return a registered description by exact target. */
static int fd_stub(void *ctx, const char *target, char *out, int outsz) {
    (void)ctx;
    for (int i = 0; i < g_nfd; i++) {
        if (strcmp(g_fd[i].target, target) == 0) {
            int n = outsz - 1, j = 0;
            for (; j < n && g_fd[i].desc[j]; j++) out[j] = g_fd[i].desc[j];
            out[j] = 0;
            return out[0] != 0;
        }
    }
    return 0;
}

static void ensure_stub(void *ctx, int at_root, const char *rel) {
    (void)ctx;
    printf("MKDIR\t%s\t%s\n", at_root ? "root" : "prog", rel);
}
static void link_stub(void *ctx, int at_root, const char *rel, const char *label, const char *target) {
    (void)ctx;
    printf("LINK\t%s\t%s\t%s\t%s\n", at_root ? "root" : "prog", rel, label, target);
}

static int readline(char *buf, int n) {
    if (!fgets(buf, n, stdin)) return 0;
    int L = (int)strlen(buf);
    while (L > 0 && (buf[L - 1] == '\n' || buf[L - 1] == '\r')) buf[--L] = 0;
    return 1;
}

static void copyfield(char *dst, const char *src, int n) {
    int i = 0;
    for (; i < n - 1 && src[i]; i++) dst[i] = src[i];
    dst[i] = 0;
}

int main(void) {
    char line[1200];
    while (readline(line, sizeof line)) {
        if (line[0] == 0 || line[0] == '#') continue;
        char *t1 = strchr(line, '\t');
        if (!t1) continue;
        *t1++ = 0;
        if (strcmp(line, "FD") == 0) {
            char *t2 = strchr(t1, '\t');
            if (!t2) continue;
            *t2++ = 0;
            if (g_nfd < MAXFD) {
                copyfield(g_fd[g_nfd].target, t1, (int)sizeof g_fd[0].target);
                copyfield(g_fd[g_nfd].desc, t2, (int)sizeof g_fd[0].desc);
                g_nfd++;
            }
        } else if (strcmp(line, "E") == 0) {
            char *t2 = strchr(t1, '\t');
            const char *data = "";
            if (t2) { *t2++ = 0; data = t2; }
            sm_plan_entry(t1, data, fd_stub, ensure_stub, link_stub, NULL);
        }
    }
    return 0;
}
