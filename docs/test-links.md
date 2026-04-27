# Test Links Page

This page tests local markdown links to verify they work correctly in the viewer.

## Same-directory links

- [Architecture](architecture.md) — link to sibling file
- [Architecture #section](architecture.md#components) — link with fragment anchor

## Root-relative links

- [README](/README.md) — link from project root
- [Spec](/specs/spec.md) — link to specs directory

## Parent-directory links

These should work from a subdirectory:

- [README via ..](../README.md) — parent directory link
- [Spec via ../specs](../specs/spec.md) — sibling directory link

## Nested subdirectory links

- [Test Page B](sub/test-b.md) — link to subdirectory file

## External links (should pass through unchanged)

- [Google](https://google.com) — external URL

## Inline code (should not be treated as links)

Use `docs/architecture.md` in your path.
