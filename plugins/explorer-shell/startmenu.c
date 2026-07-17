// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

/* startmenu.c — the pure Start-menu layout planner; see startmenu.h for the contract. */
#include "startmenu.h"
#include <string.h>

#define SM_MAXLABEL 128
#define SM_MAXPATH  512

static void sm_copy(char *dst, const char *src, int n) {
    int i = 0;
    if (n <= 0) return;
    for (; i < n - 1 && src[i]; i++) dst[i] = src[i];
    dst[i] = 0;
}

/* The "target | Label" halves are author-typed, so spaces must not leak into either. */
static void sm_trim(char *s) {
    char *b = s;
    while (*b == ' ') b++;
    if (b != s) memmove(s, b, strlen(b) + 1);
    char *e = s + strlen(s);
    while (e > s && e[-1] == ' ') *--e = 0;
}

/* A label becomes both the shortcut's on-disk .lnk name and its displayed text, and a folder
 * component becomes a directory name, so strip the characters Windows forbids in a path
 * component (\ / : * ? " < > |) and trim trailing dots/spaces. In place. */
static void sm_sanitize(char *s) {
    char *w = s, *r = s;
    for (; *r; r++) {
        char c = *r;
        if (c == '\\' || c == '/' || c == ':' || c == '*' || c == '?' ||
            c == '"' || c == '<' || c == '>' || c == '|') continue;
        *w++ = c;
    }
    *w = 0;
    while (w > s && (w[-1] == ' ' || w[-1] == '.')) *--w = 0;
}

static void sm_join(char *rel, int cap, const char *comp) {
    int L = (int)strlen(rel);
    if (rel[0]) {
        if (L + 1 + (int)strlen(comp) >= cap) return;
        rel[L] = '\\';
        sm_copy(rel + L + 1, comp, cap - L - 1);
    } else {
        sm_copy(rel, comp, cap);
    }
}

void sm_plan_entry(const char *name, const char *data,
                   sm_filedesc_fn filedesc, sm_ensure_fn ensure, sm_link_fn link, void *ctx) {
    if (!name || !name[0]) return;
    if (!data) data = "";

    char target[SM_MAXPATH], xlabel[SM_MAXLABEL];
    xlabel[0] = 0;
    const char *bar = strchr(data, '|');
    if (bar) {
        int tlen = (int)(bar - data);
        if (tlen > (int)sizeof target - 1) tlen = (int)sizeof target - 1;
        memcpy(target, data, tlen);
        target[tlen] = 0;
        sm_trim(target);
        sm_copy(xlabel, bar + 1, sizeof xlabel);
        sm_trim(xlabel);
    } else {
        sm_copy(target, data, sizeof target);
    }

    /* A single leading backslash anchors the entry at the Start Menu root rather than Programs;
     * strip it and remember the anchor, then parse the rest of the name as usual. */
    const char *p = name;
    int at_root = 0;
    if (*p == '\\') { at_root = 1; p++; }

    /* Walk the name's backslash-separated components: every component but the last is a
     * submenu folder (ensured parent-first as the running rel path grows); the last is the
     * leaf that names the shortcut. */
    char rel[SM_MAXPATH], leaf[SM_MAXLABEL];
    rel[0] = 0;
    leaf[0] = 0;
    while (*p) {
        const char *slash = p;
        while (*slash && *slash != '\\') slash++;
        char comp[SM_MAXLABEL];
        int clen = (int)(slash - p);
        if (clen > (int)sizeof comp - 1) clen = (int)sizeof comp - 1;
        memcpy(comp, p, clen);
        comp[clen] = 0;
        sm_sanitize(comp);
        if (*slash) {                       /* a folder component: more path follows */
            if (comp[0]) {                  /* skip empty components, e.g. "A\\\\B" */
                sm_join(rel, sizeof rel, comp);
                ensure(ctx, at_root, rel);
            }
            p = slash + 1;
        } else {                            /* the final component is the leaf */
            sm_copy(leaf, comp, sizeof leaf);
            break;
        }
    }
    if (!leaf[0]) return;                    /* no leaf (empty name or a trailing separator) */

    /* Precedence per startmenu.h. sanitize can empty a pathological label, hence the
     * second fallback to the (already-sanitized) leaf. */
    char label[SM_MAXLABEL];
    if (xlabel[0])
        sm_copy(label, xlabel, sizeof label);
    else if (!(filedesc && filedesc(ctx, target, label, sizeof label) && label[0]))
        sm_copy(label, leaf, sizeof label);
    sm_sanitize(label);
    if (!label[0]) sm_copy(label, leaf, sizeof label);
    if (!label[0]) return;

    link(ctx, at_root, rel, label, target);
}
