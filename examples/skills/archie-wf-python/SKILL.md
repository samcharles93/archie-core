---
name: ecosystem-python
description: >
  Python project conventions for archie-core: venv, pytest, ruff, mypy, pip-audit.
  Use when working on Python repositories with archie-core.
version: 1.0.0
metadata:
  archie:
    tools: [python, pytest, ruff, mypy, pip-audit]
    engine: any
---
# Python Ecosystem Conventions

## Preflight

When archie-core's `[[repos]]` entry sets `ecosystem = "python"`, the daemon runs
`python --version` as the preflight check before any agent stage.

## Recommended gate configuration

```toml
[[repos]]
owner = "acme"
name = "my-python-project"
ecosystem = "python"

[[repos.gate]] = ["ruff", "check", "."]
[[repos.gate]] = ["ruff", "format", "--check", "."]
[[repos.gate]] = ["mypy", "."]
[[repos.gate]] = ["pytest", "-x", "--tb=short"]
```

## Test glob

The default test glob for Python is `test_*.py`. The TDD workflow's repro-tests
stage write-protects files matching this glob during the fix stage.

Override with `test_glob = "tests/**/*.py"` in the repo config for projects
with test files outside the package root.

## Package management

arcie-core assumes:
- `requirements.txt` or `pyproject.toml` at the repo root
- Tests run with `pytest` (the ecosystem default)
- Linting with `ruff` (recommended) or `flake8`
- Type checking with `mypy`
- Security auditing with `pip-audit`

## Virtual environment

Before the gate runs, ensure dependencies are installed:
```bash
python -m venv .venv
. .venv/bin/activate
pip install -r requirements.txt
pip install pytest ruff mypy pip-audit
```

For `pyproject.toml` projects:
```bash
pip install -e ".[dev]"
```

## Common gate failures

### ruff
- `F401`: unused import  --  remove it
- `E501`: line too long  --  break the line
- `I001`: import order  --  let ruff fix it with `ruff check --fix`

### mypy
- Missing type annotation on public function  --  add it
- `Cannot determine type`  --  add explicit annotation
- Import errors  --  check the package is installed in the venv

### pytest
- Test discovery failed  --  check `__init__.py` files and test naming
- Import errors  --  verify the package is installed (`pip install -e .`)
- Assertion failures  --  fix the code, not the test
