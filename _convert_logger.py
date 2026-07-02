#!/usr/bin/env python3
"""Replace slog calls with logger.Module across all Go files in the project.

For each Go source file that imports "log/slog":
  1. Ensure the package has a var log = logger.Module("name") declaration
  2. Replace slog.Info / .Warn / .Debug / .Error (and Context variants) with log.*
  3. Remove the "log/slog" import when no longer needed
"""

import os
import re

PROJECT = "/Users/scc/go/src/goagent"
LOGGER_IMPORT = '"github.com/Timwood0x10/ares/internal/logger"'
EXCLUDE_DIRS = {"internal/logger"}  # already has its own slog usage

# Nodes we've already added the var log declaration for
done_packages = set()

# Pre-built strings for files that already use logger.New (GA engine)
ALREADY_MODERN = {"internal/ares_evolution/genome", "internal/ares_evolution/promotion",
                  "internal/ares_evolution"}


def package_name_for_dir(dirpath: str) -> str:
    """Get the Go package name from files in a directory."""
    for f in sorted(os.listdir(dirpath)):
        if f.endswith(".go") and not f.endswith("_test.go"):
            path = os.path.join(dirpath, f)
            m = re.search(r'^package\s+(\w+)', open(path).read(200), re.M)
            if m:
                return m.group(1)
    return os.path.basename(dirpath)


def has_logger_declaration(content: str) -> bool:
    return bool(re.search(r'\bvar\s+log\s*=\s*logger\.(Module|New)\b', content))


def add_logger_import(content: str) -> str:
    """Add logger import inside import (...) block, before any other imports."""
    def _add(m):
        block = m.group(0)
        if LOGGER_IMPORT in block:
            return block
        # Insert after the opening paren, before any other imports
        header = block.split('\n')[0]
        rest = '\n'.join(block.split('\n')[1:])
        return f'{header}\n\t{LOGGER_IMPORT}\n{rest}'
    # Match import ( ... ) — the raw string avoids escaping issues
    pattern = 'import\\s*\\((.*?)\\)'
    return re.sub(pattern, _add, content, count=1, flags=re.DOTALL)


def add_var_declaration(content: str, pkg_name: str) -> str:
    """Add var log = logger.Module("pkgname") after package declaration."""
    if has_logger_declaration(content):
        return content
    content = re.sub(
        r'^package\s+' + re.escape(pkg_name) + r'\s*',
        f'package {pkg_name}\n\nvar log = logger.Module("{pkg_name}")\n',
        content, count=1,
    )
    return content


def replace_slog_calls(content: str) -> (str, bool):
    """Replace slog.Info/Warn/Debug/Error calls with log.* (same args).
    Returns (new_content, changed)."""
    patterns = [
        (r'\bslog\.Info\b', 'log.Info'),
        (r'\bslog\.InfoContext\b', 'log.InfoContext'),
        (r'\bslog\.Warn\b', 'log.Warn'),
        (r'\bslog\.WarnContext\b', 'log.WarnContext'),
        (r'\bslog\.Debug\b', 'log.Debug'),
        (r'\bslog\.DebugContext\b', 'log.DebugContext'),
        (r'\bslog\.Error\b', 'log.Error'),
        (r'\bslog\.ErrorContext\b', 'log.ErrorContext'),
    ]
    changed = False
    for pat, repl in patterns:
        new_content = re.sub(pat, repl, content)
        if new_content != content:
            changed = True
        content = new_content
    return content, changed


def still_needs_slog(content: str) -> bool:
    """Check if file still references slog.* after replacing logging calls."""
    # Check for slog type references (Logger, Handler, Level, Attr, etc.)
    # and non-logging functions (Default, New, SetDefault, NewJSONHandler, etc.)
    return bool(re.search(r'\bslog\.', content))


def remove_slog_import(content: str) -> str:
    """Remove the "log/slog" import line if present."""
    lines = content.split('\n')
    new_lines = []
    slog_import_removed = False
    in_import = False
    for line in lines:
        if line.strip().startswith('import ('):
            in_import = True
            new_lines.append(line)
            continue
        if in_import:
            if line.strip() == ')':
                in_import = False
                new_lines.append(line)
                continue
            if '"log/slog"' in line:
                slog_import_removed = True
                continue  # skip this line
        new_lines.append(line)
    if slog_import_removed:
        return '\n'.join(new_lines)
    return content


def process_file(filepath: str, pkg_name: str):
    with open(filepath, 'r') as f:
        content = f.read()

    # Already modern (uses logger.New with method names)
    if has_logger_declaration(content) and 'logger.New(' in content:
        return

    # Add logger import if needed
    new_content = add_logger_import(content)

    # Add var log declaration if needed  
    new_content = add_var_declaration(new_content, pkg_name)

    # Replace slog function calls
    new_content, slog_replaced = replace_slog_calls(new_content)

    if not slog_replaced and new_content == content:
        return  # nothing changed

    # If slog is no longer referenced, remove its import
    if not still_needs_slog(new_content):
        new_content = remove_slog_import(new_content)

    with open(filepath, 'w') as f:
        f.write(new_content)
    rel = os.path.relpath(filepath, PROJECT)
    print(f"  UPDATED {rel}")


def main():
    for root, dirs, files in os.walk(PROJECT):
        rel_root = os.path.relpath(root, PROJECT)
        if rel_root in EXCLUDE_DIRS or rel_root.startswith("."):
            continue

        go_files = [f for f in files if f.endswith('.go') and not f.endswith('_test.go')]
        if not go_files:
            continue

        # Check if any file imports log/slog
        has_slog = False
        for f in go_files:
            if re.search(r'"log/slog"', open(os.path.join(root, f)).read(500)):
                has_slog = True
                break
        if not has_slog:
            continue

        pkg_name = package_name_for_dir(root)
        print(f"\n[{rel_root}] (package {pkg_name})")
        for f in go_files:
            process_file(os.path.join(root, f), pkg_name)


if __name__ == '__main__':
    main()
