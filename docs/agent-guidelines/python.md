# Python Compatibility

Python code and Python-related dependencies must remain compatible with
Python 3.9 and newer.

## Requirements

- Do not use Python syntax, standard-library APIs, package metadata, or
  dependency versions that require Python 3.10 or newer.
- When adding or updating Python dependencies, check their declared
  `Requires-Python` range and choose a version that supports Python 3.9.
- For Python packaging files, set the minimum Python version to 3.9 unless a
  stricter existing project constraint already applies.
- If a tool or script cannot support Python 3.9, document the exception in the
  change and prefer isolating it from runtime or release-critical paths.
