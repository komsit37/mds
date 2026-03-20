# API Specification

## Endpoints

### GET /api/files
Returns list of all markdown files in the project.

**Response:**
```json
{
  "project": "my-project",
  "files": [
    {
      "path": "docs/readme.md",
      "name": "readme.md",
      "dir": "docs",
      "modTime": 1710000000000,
      "changed": false
    }
  ],
  "isGit": true
}
```

### GET /api/content?path=...
Returns raw markdown content of a file.

### GET /api/diff?path=...
Returns git diff for a file.

```json
{
  "diff": "unified diff text...",
  "hasChanges": true,
  "label": "Uncommitted changes"
}
```
