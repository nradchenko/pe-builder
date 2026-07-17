// Copyright 2026 Nikita Radchenko
// SPDX-License-Identifier: Apache-2.0

/* startmenu.h — the pure Start-menu layout planner for peshell: one `pe-builder\StartMenu`
 * contract entry in, folder-create and shortcut-create callbacks out. No platform headers, so
 * it builds and unit-tests on the host (startmenu_test.go). Target paths are passed through
 * raw, environment variables unexpanded; the Windows implementation expands them.
 * See README.md § Start-menu items for the contract. */
#ifndef PESHELL_STARTMENU_H
#define PESHELL_STARTMENU_H

/* Look up the target's version-resource FileDescription. On success write the (possibly
 * localized) description into out and return 1; return 0 if the target has none. */
typedef int (*sm_filedesc_fn)(void *ctx, const char *target, char *out, int outsz);

/* Ensure one folder level exists, given its path relative to the anchor (e.g. "Games", then
 * "Games\\Board"). Called parent-first, so the implementation only ever has to create the last
 * component. `at_root` selects the anchor: the Start Menu root when non-zero, else Start
 * Menu\Programs (All Programs). */
typedef void (*sm_ensure_fn)(void *ctx, int at_root, const char *rel_folder);

/* Create the shortcut `label`.lnk inside rel_folder — a path relative to the anchor, or "" for
 * the anchor itself. `at_root` selects the anchor as for sm_ensure_fn. */
typedef void (*sm_link_fn)(void *ctx, int at_root, const char *rel_folder, const char *label, const char *target);

/* Plan one contract entry into folder-create + shortcut-create callbacks. `name` is the menu
 * path (backslashes separate submenus; one leading backslash anchors at the Start Menu root
 * rather than Programs), `data` the target, optionally "target|Explicit Label" — see
 * README.md § Start-menu items.
 *
 * Label precedence: explicit override → the target's FileDescription → the value-name leaf.
 * `ensure` runs per folder level parent-first, then `link` once for the leaf. An entry that
 * yields no leaf is silently skipped. */
void sm_plan_entry(const char *name, const char *data,
                   sm_filedesc_fn filedesc, sm_ensure_fn ensure, sm_link_fn link, void *ctx);

#endif