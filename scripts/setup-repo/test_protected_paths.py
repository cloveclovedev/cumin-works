#!/usr/bin/env python3
"""Tests for the script inside protected-paths.yml.

The tests load the inline script from the workflow file, so they run the same
code that the workflow runs. They need Python 3.11 or later and no network.

Run: python3 scripts/setup-repo/test_protected_paths.py
"""
import pathlib
import textwrap
import unittest

HERE = pathlib.Path(__file__).resolve().parent
WORKFLOW = HERE / "protected-paths.yml"
STARTER_CONFIG = HERE / "config.toml"


def load_workflow_script():
    lines = WORKFLOW.read_text(encoding="utf-8").splitlines()
    starts = [i for i, line in enumerate(lines) if line.strip() == "run: |"]
    if len(starts) != 1:
        raise AssertionError(f"expected one 'run: |' block, found {len(starts)}")
    indent = len(lines[starts[0]]) - len(lines[starts[0]].lstrip()) + 2
    block = []
    for line in lines[starts[0] + 1 :]:
        if line.strip() and len(line) - len(line.lstrip()) < indent:
            break
        block.append(line)
    namespace = {"__name__": "cumin_protected_paths"}
    exec(compile(textwrap.dedent("\n".join(block)), str(WORKFLOW), "exec"), namespace)
    return namespace


SCRIPT = load_workflow_script()
DEFAULTS = SCRIPT["DEFAULT_ENTRIES"]


class MatchingRules(unittest.TestCase):
    def test_table(self):
        cases = [
            # entry, path, expected
            ("CLAUDE.md", "CLAUDE.md", True),  # a name at the top level
            ("CLAUDE.md", "sub/dir/CLAUDE.md", True),  # a name in a subdirectory
            ("CLAUDE.md", "sub/claude.md", True),  # a different case
            ("CLAUDE.md", "CLAUDE.md/notes.txt", True),  # a directory with that name
            ("CLAUDE.md", "docs/CLAUDE.md.bak", False),
            ("CLAUDE.md", "docs/MY-CLAUDE.md", False),
            (".claude/", ".claude/settings.json", True),  # a directory entry at the top level
            (".claude/", "app/.claude/skills/x/SKILL.md", True),  # a directory entry at any depth
            (".claude/", "app/.CLAUDE/settings.json", True),
            (".claude/", ".claude", False),  # a file, not a directory
            (".claude/", "docs/.claude.md", False),
            (".cumin/", ".cumin/config.toml", True),
            ("/docs/requirements/", "docs/requirements/overview.md", True),  # a leading "/"
            ("/docs/requirements/", "sub/docs/requirements/overview.md", False),
            ("/CLAUDE.md", "CLAUDE.md", True),
            ("/CLAUDE.md", "sub/CLAUDE.md", False),
            ("docs/requirements/", "docs/requirements/a/b.md", True),  # an inner "/"
            ("docs/requirements/", "sub/docs/requirements/b.md", False),
            ("docs/requirements/", "docs/requirements", False),
            ("docs/requirements", "docs/requirements/b.md", True),
            ("docs/requirements", "docs/requirements", True),
            ("docs/requirements", "docs/requirements-old/b.md", False),
            ("CLAUDE.md", "src/main.go", False),  # a path that matches no entry
            ("café.md", "sub/café.MD", True),  # the same name in another Unicode form
        ]
        for entry, path, expected in cases:
            with self.subTest(entry=entry, path=path):
                self.assertEqual(SCRIPT["entry_matches"](entry, path), expected)

    def test_invalid_entries_are_errors(self):
        for entry in ["", "/", "docs//x", "../x", "./x", "*.md", "docs/**", "!keep", "a[b]", "a\\b", 7]:
            with self.subTest(entry=entry):
                with self.assertRaises(SCRIPT["ConfigError"]):
                    SCRIPT["parse_entry"](entry)


class Config(unittest.TestCase):
    def test_missing_file_gives_the_default_list(self):
        self.assertEqual(SCRIPT["load_entries"](None), DEFAULTS)

    def test_missing_key_gives_the_default_list(self):
        self.assertEqual(SCRIPT["load_entries"]('merge_method = "squash"\n'), DEFAULTS)

    def test_the_list_replaces_the_default_list(self):
        self.assertEqual(SCRIPT["load_entries"]('protected_paths = ["/docs/"]\n'), ["/docs/"])

    def test_an_empty_list_is_valid(self):
        self.assertEqual(SCRIPT["load_entries"]("protected_paths = []\n"), [])

    def test_invalid_toml_is_an_error(self):
        with self.assertRaises(SCRIPT["ConfigError"]):
            SCRIPT["load_entries"]("protected_paths = [")

    def test_a_wrong_type_is_an_error(self):
        for text in ['protected_paths = "CLAUDE.md"\n', "protected_paths = [1]\n", 'protected_paths = ["*.md"]\n']:
            with self.subTest(text=text):
                with self.assertRaises(SCRIPT["ConfigError"]):
                    SCRIPT["load_entries"](text)

    def test_the_starter_config_holds_the_default_list(self):
        text = STARTER_CONFIG.read_text(encoding="utf-8")
        self.assertEqual(SCRIPT["load_entries"](text), DEFAULTS)


class ChangedFiles(unittest.TestCase):
    def violations(self, files):
        return SCRIPT["find_violations"](DEFAULTS, files)

    def test_an_unprotected_change_passes(self):
        files = [{"filename": "src/main.go", "status": "modified"}]
        self.assertEqual(self.violations(files), [])

    def test_an_added_file_in_a_subdirectory_fails(self):
        files = [{"filename": "sub/claude.md", "status": "added"}]
        self.assertEqual(self.violations(files), [("sub/claude.md", "added", "CLAUDE.md")])

    def test_a_removed_file_fails(self):
        files = [{"filename": ".cumin/config.toml", "status": "removed"}]
        self.assertEqual(self.violations(files), [(".cumin/config.toml", "removed", ".cumin/")])

    def test_a_rename_away_from_a_protected_path_fails(self):
        files = [{"filename": "docs/notes.md", "previous_filename": "AGENTS.md", "status": "renamed"}]
        self.assertEqual(self.violations(files), [("AGENTS.md", "renamed", "AGENTS.md")])

    def test_a_rename_to_a_protected_path_fails(self):
        files = [{"filename": ".claude/notes.md", "previous_filename": "docs/notes.md", "status": "renamed"}]
        self.assertEqual(self.violations(files), [(".claude/notes.md", "renamed", ".claude/")])


class WorkflowShape(unittest.TestCase):
    def setUp(self):
        self.text = WORKFLOW.read_text(encoding="utf-8")

    def test_the_trigger_has_no_filter(self):
        self.assertIn("\non:\n  pull_request:\n\n", self.text)

    def test_the_job_runs_for_every_bot_author_and_names_no_app(self):
        self.assertIn("    if: github.event.pull_request.user.type == 'Bot'\n", self.text)
        self.assertNotIn("user.login", self.text)
        self.assertNotIn("IMPLEMENTER", self.text.upper())

    def test_the_permissions_are_read_only(self):
        self.assertIn("\npermissions:\n  contents: read\n  pull-requests: read\n\n", self.text)

    def test_the_job_does_not_check_out_code(self):
        self.assertNotIn("uses:", self.text)


if __name__ == "__main__":
    unittest.main()
