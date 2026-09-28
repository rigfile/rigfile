---
name: release-checklist
description: "Walk through a small project's release: version bump, changelog, tag, and a final check that the build artifacts exist. Triggers on: release, cut a release, tag a version, changelog."
---

# Release checklist

1. Bump the version in the project's manifest.
2. Move the "Unreleased" changelog entries under the new version.
3. Run `python check_release.py` from this skill's folder; fix anything it reports.
4. Tag the release and push the tag after review.
