package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

//go:embed static/*
var staticFS embed.FS

type FileInfo struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Dir     string `json:"dir"`
	ModTime int64  `json:"modTime"`
	Changed bool   `json:"changed"`
}

type FilesResponse struct {
	Project string     `json:"project"`
	Files   []FileInfo `json:"files"`
	IsGit   bool       `json:"isGit"`
}

type DiffResponse struct {
	Diff       string `json:"diff"`
	HasChanges bool   `json:"hasChanges"`
	Label      string `json:"label"`
}

type CommitInfo struct {
	Hash      string `json:"hash"`
	ShortHash string `json:"shortHash"`
	Message   string `json:"message"`
	Author    string `json:"author"`
	Date      int64  `json:"date"`
	Age       string `json:"age"`
}

type HistoryResponse struct {
	Commits []CommitInfo `json:"commits"`
}

var projectDir string
var projectName string

func main() {
	// Determine project directory
	if len(os.Args) > 1 {
		projectDir = os.Args[1]
	} else {
		var err error
		projectDir, err = os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}

	// Resolve to absolute path
	absDir, err := filepath.Abs(projectDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error resolving path: %v\n", err)
		os.Exit(1)
	}
	projectDir = absDir
	projectName = filepath.Base(projectDir)

	// Setup routes
	mux := http.NewServeMux()
	mux.HandleFunc("/api/files", handleFiles)
	mux.HandleFunc("/api/content", handleContent)
	mux.HandleFunc("/api/diff", handleDiff)
	mux.HandleFunc("/api/history", handleHistory)

	// Serve static files from embedded FS
	staticSub, _ := fs.Sub(staticFS, "static")
	fileServer := http.FileServer(http.FS(staticSub))
	mux.Handle("/", fileServer)

	// Auto port shifting
	basePort := 8080
	maxPort := 8100
	var listener net.Listener
	var port int

	for port = basePort; port <= maxPort; port++ {
		addr := fmt.Sprintf("0.0.0.0:%d", port)
		listener, err = net.Listen("tcp", addr)
		if err == nil {
			break
		}
	}
	if listener == nil {
		fmt.Fprintf(os.Stderr, "error: could not find open port between %d-%d\n", basePort, maxPort)
		os.Exit(1)
	}

	fmt.Printf("📄 mds — serving specs for [%s]\n", projectName)
	fmt.Printf("   %s\n", projectDir)
	fmt.Printf("   http://localhost:%d\n", port)

	if err := http.Serve(listener, mux); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// isGitRepo checks if projectDir is inside a git repository
func isGitRepo() bool {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = projectDir
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// getGitChangedFiles returns set of file paths (relative to project root) with uncommitted changes
func getGitChangedFiles() map[string]bool {
	changed := make(map[string]bool)

	// Get git repo root to calculate relative paths correctly
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = projectDir
	rootOut, err := cmd.Output()
	if err != nil {
		return changed
	}
	gitRoot := strings.TrimSpace(string(rootOut))

	// Staged + unstaged changes
	cmd = exec.Command("git", "diff", "--name-only", "HEAD")
	cmd.Dir = projectDir
	out, err := cmd.Output()
	if err != nil {
		// Maybe no commits yet, try without HEAD
		cmd = exec.Command("git", "diff", "--name-only")
		cmd.Dir = projectDir
		out, _ = cmd.Output()
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Convert from git-root-relative to projectDir-relative
		absPath := filepath.Join(gitRoot, line)
		relPath, err := filepath.Rel(projectDir, absPath)
		if err == nil && !strings.HasPrefix(relPath, "..") {
			changed[relPath] = true
		}
	}

	// Also include staged changes
	cmd = exec.Command("git", "diff", "--name-only", "--cached")
	cmd.Dir = projectDir
	out, _ = cmd.Output()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		absPath := filepath.Join(gitRoot, line)
		relPath, err := filepath.Rel(projectDir, absPath)
		if err == nil && !strings.HasPrefix(relPath, "..") {
			changed[relPath] = true
		}
	}

	// Untracked files
	cmd = exec.Command("git", "ls-files", "--others", "--exclude-standard")
	cmd.Dir = projectDir
	out, _ = cmd.Output()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasSuffix(strings.ToLower(line), ".md") {
			changed[line] = true
		}
	}

	return changed
}

// listMDFiles scans for .md files respecting .gitignore
func listMDFiles() ([]FileInfo, bool) {
	isGit := isGitRepo()
	changedFiles := make(map[string]bool)
	if isGit {
		changedFiles = getGitChangedFiles()
	}

	var files []FileInfo

	if isGit {
		// Use git ls-files for tracked files
		cmd := exec.Command("git", "ls-files", "--full-name", "*.md", "**/*.md")
		cmd.Dir = projectDir
		out, _ := cmd.Output()

		seen := make(map[string]bool)
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || !strings.HasSuffix(strings.ToLower(line), ".md") {
				continue
			}
			// Make path relative to projectDir
			seen[line] = true
			fi := fileInfoFromPath(line, changedFiles)
			if fi != nil {
				files = append(files, *fi)
			}
		}

		// Also include untracked but not ignored .md files
		cmd = exec.Command("git", "ls-files", "--others", "--exclude-standard", "*.md", "**/*.md")
		cmd.Dir = projectDir
		out, _ = cmd.Output()
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || !strings.HasSuffix(strings.ToLower(line), ".md") {
				continue
			}
			if !seen[line] {
				fi := fileInfoFromPath(line, changedFiles)
				if fi != nil {
					files = append(files, *fi)
				}
			}
		}
	} else {
		// No git — walk the filesystem
		filepath.WalkDir(projectDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			// Skip common junk directories
			if d.IsDir() {
				name := d.Name()
				if name == "node_modules" || name == "vendor" || name == ".git" || name == "__pycache__" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
				return nil
			}
			rel, err := filepath.Rel(projectDir, path)
			if err != nil {
				return nil
			}
			fi := fileInfoFromPath(rel, changedFiles)
			if fi != nil {
				files = append(files, *fi)
			}
			return nil
		})
	}

	// Sort by modTime descending
	sort.Slice(files, func(i, j int) bool {
		return files[i].ModTime > files[j].ModTime
	})

	return files, isGit
}

func fileInfoFromPath(relPath string, changedFiles map[string]bool) *FileInfo {
	absPath := filepath.Join(projectDir, relPath)
	stat, err := os.Stat(absPath)
	if err != nil {
		return nil
	}
	return &FileInfo{
		Path:    relPath,
		Name:    filepath.Base(relPath),
		Dir:     filepath.Dir(relPath),
		ModTime: stat.ModTime().UnixMilli(),
		Changed: changedFiles[relPath],
	}
}

func handleFiles(w http.ResponseWriter, r *http.Request) {
	files, isGit := listMDFiles()
	if files == nil {
		files = []FileInfo{}
	}

	resp := FilesResponse{
		Project: projectName,
		Files:   files,
		IsGit:   isGit,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func handleContent(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "missing path parameter", http.StatusBadRequest)
		return
	}

	// Sanitize path to prevent directory traversal
	cleanPath := filepath.Clean(path)
	if strings.HasPrefix(cleanPath, "..") || filepath.IsAbs(cleanPath) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	absPath := filepath.Join(projectDir, cleanPath)
	content, err := os.ReadFile(absPath)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(content)
}

func handleDiff(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "missing path parameter", http.StatusBadRequest)
		return
	}

	cleanPath := filepath.Clean(path)
	if strings.HasPrefix(cleanPath, "..") || filepath.IsAbs(cleanPath) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	resp := DiffResponse{}

	if !isGitRepo() {
		resp.Label = "Not a git repository"
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	// If a specific commit is requested, show that commit's diff
	commit := r.URL.Query().Get("commit")
	if commit != "" {
		// Validate commit hash (alphanumeric only)
		for _, c := range commit {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				http.Error(w, "invalid commit hash", http.StatusBadRequest)
				return
			}
		}
		diffForCommit(cleanPath, commit, &resp)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	// Try uncommitted changes first (working tree + staged vs HEAD)
	cmd := exec.Command("git", "diff", "HEAD", "--", cleanPath)
	cmd.Dir = projectDir
	out, err := cmd.Output()
	if err == nil && len(strings.TrimSpace(string(out))) > 0 {
		resp.Diff = string(out)
		resp.HasChanges = true
		resp.Label = "Uncommitted changes"
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	// Fallback: show diff from last commit that touched this file
	cmd = exec.Command("git", "log", "-1", "--format=%H %ar", "--", cleanPath)
	cmd.Dir = projectDir
	out, err = cmd.Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		resp.Label = "No git history for this file"
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	parts := strings.SplitN(strings.TrimSpace(string(out)), " ", 2)
	commitHash := parts[0]
	commitAge := ""
	if len(parts) > 1 {
		commitAge = parts[1]
	}

	cmd = exec.Command("git", "diff", commitHash+"~1", commitHash, "--", cleanPath)
	cmd.Dir = projectDir
	out, err = cmd.Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		// Maybe first commit — show the whole file as addition
		cmd = exec.Command("git", "show", commitHash+":"+cleanPath)
		cmd.Dir = projectDir
		out, _ = cmd.Output()
		if len(out) > 0 {
			lines := strings.Split(string(out), "\n")
			var diffLines []string
			diffLines = append(diffLines, fmt.Sprintf("--- /dev/null"))
			diffLines = append(diffLines, fmt.Sprintf("+++ b/%s", cleanPath))
			diffLines = append(diffLines, fmt.Sprintf("@@ -0,0 +1,%d @@", len(lines)))
			for _, l := range lines {
				diffLines = append(diffLines, "+"+l)
			}
			resp.Diff = strings.Join(diffLines, "\n")
			resp.HasChanges = true
			resp.Label = fmt.Sprintf("Initial commit (%s)", commitAge)
		} else {
			resp.Label = "No changes found"
		}
	} else {
		resp.Diff = string(out)
		resp.HasChanges = true
		resp.Label = fmt.Sprintf("Last commit (%s)", commitAge)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// diffForCommit generates diff for a specific commit
func diffForCommit(cleanPath, commitHash string, resp *DiffResponse) {
	// Get commit metadata for label
	cmd := exec.Command("git", "log", "-1", "--format=%s (%ar)", commitHash)
	cmd.Dir = projectDir
	labelOut, _ := cmd.Output()
	label := strings.TrimSpace(string(labelOut))

	// Get diff: commit vs parent
	cmd = exec.Command("git", "diff", commitHash+"~1", commitHash, "--", cleanPath)
	cmd.Dir = projectDir
	out, err := cmd.Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		// First commit — show whole file as addition
		cmd = exec.Command("git", "show", commitHash+":"+cleanPath)
		cmd.Dir = projectDir
		out, _ = cmd.Output()
		if len(out) > 0 {
			lines := strings.Split(string(out), "\n")
			var diffLines []string
			diffLines = append(diffLines, "--- /dev/null")
			diffLines = append(diffLines, fmt.Sprintf("+++ b/%s", cleanPath))
			diffLines = append(diffLines, fmt.Sprintf("@@ -0,0 +1,%d @@", len(lines)))
			for _, l := range lines {
				diffLines = append(diffLines, "+"+l)
			}
			resp.Diff = strings.Join(diffLines, "\n")
			resp.HasChanges = true
			if label != "" {
				resp.Label = label
			} else {
				resp.Label = "Initial commit"
			}
		} else {
			resp.Label = "No changes found"
		}
	} else {
		resp.Diff = string(out)
		resp.HasChanges = true
		if label != "" {
			resp.Label = label
		} else {
			resp.Label = commitHash[:8]
		}
	}
}

func handleHistory(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "missing path parameter", http.StatusBadRequest)
		return
	}

	cleanPath := filepath.Clean(path)
	if strings.HasPrefix(cleanPath, "..") || filepath.IsAbs(cleanPath) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	resp := HistoryResponse{}

	if !isGitRepo() {
		resp.Commits = []CommitInfo{}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	// Use NUL-delimited format for safe parsing
	// Format: hash, short hash, subject, author, unix timestamp, relative date
	cmd := exec.Command("git", "log", "--follow", "--format=%H%x00%h%x00%s%x00%an%x00%at%x00%ar", "-50", "--", cleanPath)
	cmd.Dir = projectDir
	out, err := cmd.Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		resp.Commits = []CommitInfo{}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	var commits []CommitInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\x00", 6)
		if len(fields) < 6 {
			continue
		}
		var date int64
		fmt.Sscanf(fields[4], "%d", &date)
		commits = append(commits, CommitInfo{
			Hash:      fields[0],
			ShortHash: fields[1],
			Message:   fields[2],
			Author:    fields[3],
			Date:      date * 1000, // convert to milliseconds
			Age:       fields[5],
		})
	}

	if commits == nil {
		commits = []CommitInfo{}
	}
	resp.Commits = commits

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func init() {
	// Ensure time formatting is consistent
	time.Local = time.UTC
}
