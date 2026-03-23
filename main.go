package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

type RelatedFile struct {
	Path    string   `json:"path"`
	Name    string   `json:"name"`
	Dir     string   `json:"dir"`
	Score   float64  `json:"score"`
	Signals []string `json:"signals"`
}

type RelatedResponse struct {
	Related []RelatedFile `json:"related"`
}

type RecentFileInfo struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Dir     string `json:"dir"`
	ModTime int64  `json:"modTime"`
}

type RecentGroup struct {
	Type      string           `json:"type"`
	Message   string           `json:"message"`
	ShortHash string           `json:"shortHash"`
	Age       string           `json:"age"`
	Date      int64            `json:"date"`
	Files     []RecentFileInfo `json:"files"`
}

type RecentResponse struct {
	Project string        `json:"project"`
	Groups  []RecentGroup `json:"groups"`
	IsGit   bool          `json:"isGit"`
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
	mux.HandleFunc("/api/related", handleRelated)
	mux.HandleFunc("/api/recent", handleRecent)
	mux.HandleFunc("/api/asset", handleAsset)

	// Serve static files from embedded FS (no-cache for dev convenience)
	staticSub, _ := fs.Sub(staticFS, "static")
	fileServer := http.FileServer(http.FS(staticSub))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		fileServer.ServeHTTP(w, r)
	}))

	// Auto port shifting
	basePort := 8090
	maxPort := 8110
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

	url := fmt.Sprintf("http://localhost:%d", port)
	fmt.Printf("📄 mds — serving specs for [%s]\n", projectName)
	fmt.Printf("   %s\n", projectDir)
	fmt.Printf("   %s\n", url)

	// Auto-open browser if available (non-blocking)
	go openBrowser(url)

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

// listFiles scans for files respecting .gitignore. If mdOnly is true, only .md files are returned.
func listFiles(mdOnly bool) ([]FileInfo, bool) {
	isGit := isGitRepo()
	changedFiles := make(map[string]bool)
	if isGit {
		changedFiles = getGitChangedFiles()
	}

	var files []FileInfo

	isMD := func(name string) bool {
		return strings.HasSuffix(strings.ToLower(name), ".md")
	}

	if isGit {
		// Use git ls-files for tracked files
		args := []string{"ls-files", "--full-name"}
		if mdOnly {
			args = append(args, "*.md", "**/*.md")
		}
		cmd := exec.Command("git", args...)
		cmd.Dir = projectDir
		out, _ := cmd.Output()

		seen := make(map[string]bool)
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if mdOnly && !isMD(line) {
				continue
			}
			seen[line] = true
			fi := fileInfoFromPath(line, changedFiles)
			if fi != nil {
				files = append(files, *fi)
			}
		}

		// Also include untracked but not ignored files
		args = []string{"ls-files", "--others", "--exclude-standard"}
		if mdOnly {
			args = append(args, "*.md", "**/*.md")
		}
		cmd = exec.Command("git", args...)
		cmd.Dir = projectDir
		out, _ = cmd.Output()
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if mdOnly && !isMD(line) {
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
			if d.IsDir() {
				name := d.Name()
				if name == "node_modules" || name == "vendor" || name == ".git" || name == "__pycache__" {
					return filepath.SkipDir
				}
				return nil
			}
			if mdOnly && !isMD(d.Name()) {
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
	mdOnly := r.URL.Query().Get("all") != "true"
	files, isGit := listFiles(mdOnly)
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

// openBrowser tries to open the URL in the default browser. Silently fails if unavailable.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		// Skip if no DISPLAY/WAYLAND (headless server)
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return
		}
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return
	}
	cmd.Start()
}

// handleRelated returns files related to the given file
func handleRelated(w http.ResponseWriter, r *http.Request) {
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

	// Get all .md files
	allFiles, _ := listFiles(true)
	if allFiles == nil {
		allFiles = []FileInfo{}
	}

	// Read the content of the current file
	absPath := filepath.Join(projectDir, cleanPath)
	content, err := os.ReadFile(absPath)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	// Parse current file's links and headings
	currentLinks := parseMarkdownLinks(string(content))
	currentHeadings := extractHeadings(string(content))
	currentDir := filepath.Dir(cleanPath)

	// Build a map of all .md file paths for quick lookup
	mdPaths := make(map[string]bool)
	for _, f := range allFiles {
		mdPaths[f.Path] = true
	}

	// Calculate scores for all other .md files
	type scoredFile struct {
		file  RelatedFile
		score float64
	}
	var scored []scoredFile

	for _, f := range allFiles {
		if f.Path == cleanPath {
			continue
		}

		// Read content for this file
		absOtherPath := filepath.Join(projectDir, f.Path)
		otherContent, err := os.ReadFile(absOtherPath)
		if err != nil {
			continue
		}

		otherLinks := parseMarkdownLinks(string(otherContent))
		otherHeadings := extractHeadings(string(otherContent))

		// Signal 1: Cross-references (weight 0.45)
		linkScore := 0.0
		signals := []string{}

		// Check if current file links to this file
		currentLinksToOther := false
		for _, link := range currentLinks {
			resolved := resolveRelativeLink(link, filepath.Dir(cleanPath))
			if resolved == f.Path {
				currentLinksToOther = true
				break
			}
		}

		// Check if this file links to current file
		otherLinksToCurrent := false
		for _, link := range otherLinks {
			resolved := resolveRelativeLink(link, filepath.Dir(f.Path))
			if resolved == cleanPath {
				otherLinksToCurrent = true
				break
			}
		}

		if currentLinksToOther && otherLinksToCurrent {
			linkScore = 1.0
			signals = append(signals, "linked")
		} else if currentLinksToOther {
			linkScore = 1.0
			signals = append(signals, "linked")
		} else if otherLinksToCurrent {
			linkScore = 0.8
			signals = append(signals, "linked")
		}

		// Signal 2: Heading-term overlap (weight 0.30)
		headingSim := computeHeadingSimilarity(currentHeadings, otherHeadings, f.Name)
		if headingSim > 0.1 {
			signals = append(signals, "similar")
		}

		// Signal 3: Directory proximity (weight 0.25)
		dirProximity := computeDirProximity(currentDir, filepath.Dir(f.Path))
		if dirProximity >= 0.4 {
			signals = append(signals, "nearby")
		}

		// Final score
		finalScore := 0.45*linkScore + 0.30*headingSim + 0.25*dirProximity

		if finalScore > 0.05 {
			scored = append(scored, scoredFile{
				file: RelatedFile{
					Path:    f.Path,
					Name:    f.Name,
					Dir:     f.Dir,
					Score:   finalScore,
					Signals: signals,
				},
				score: finalScore,
			})
		}
	}

	// Sort by score descending
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Take top 8
	if len(scored) > 8 {
		scored = scored[:8]
	}

	// Build response
	related := make([]RelatedFile, len(scored))
	for i, s := range scored {
		related[i] = s.file
	}

	resp := RelatedResponse{
		Related: related,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// parseMarkdownLinks extracts links from markdown content: [text](path) and [text]: path
func parseMarkdownLinks(content string) []string {
	var links []string

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		// Find [text](path) inline link patterns
		i := 0
		for i < len(line) {
			// Find opening [
			ob := strings.Index(line[i:], "[")
			if ob == -1 {
				break
			}
			ob += i

			// Find closing ]
			cb := strings.Index(line[ob+1:], "]")
			if cb == -1 {
				break
			}
			cb += ob + 1

			// Must be immediately followed by (
			if cb+1 >= len(line) || line[cb+1] != '(' {
				i = cb + 1
				continue
			}

			// Find closing )
			cp := strings.Index(line[cb+2:], ")")
			if cp == -1 {
				i = cb + 2
				continue
			}
			cp += cb + 2

			linkPath := strings.TrimSpace(line[cb+2 : cp])
			if linkPath != "" {
				links = append(links, linkPath)
			}
			i = cp + 1
		}

		// Reference-style links: [text]: path
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			if cb := strings.Index(trimmed, "]:"); cb > 0 {
				linkPath := strings.TrimSpace(trimmed[cb+2:])
				if linkPath != "" {
					links = append(links, linkPath)
				}
			}
		}
	}

	return links
}

// extractHeadings extracts H1-H3 headings from markdown content
func extractHeadings(content string) []string {
	var headings []string

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Check for H1, H2, or H3 headings
		if strings.HasPrefix(trimmed, "### ") {
			headings = append(headings, strings.TrimPrefix(trimmed, "### "))
		} else if strings.HasPrefix(trimmed, "## ") {
			headings = append(headings, strings.TrimPrefix(trimmed, "## "))
		} else if strings.HasPrefix(trimmed, "# ") {
			headings = append(headings, strings.TrimPrefix(trimmed, "# "))
		}
	}

	return headings
}

// resolveRelativeLink resolves a relative link path against a base directory
func resolveRelativeLink(link, baseDir string) string {
	// Strip fragment anchors (e.g., "file.md#section" → "file.md")
	if idx := strings.Index(link, "#"); idx != -1 {
		link = link[:idx]
	}
	if link == "" {
		return ""
	}

	// Skip URLs
	if strings.Contains(link, "://") {
		return ""
	}

	// Resolve: absolute from project root, or relative to baseDir
	var resolved string
	if strings.HasPrefix(link, "/") {
		resolved = strings.TrimPrefix(link, "/")
	} else {
		resolved = filepath.Join(baseDir, link)
	}

	return filepath.Clean(resolved)
}

// computeHeadingSimilarity computes Jaccard similarity between heading tokens
func computeHeadingSimilarity(currentHeadings, otherHeadings []string, otherFileName string) float64 {
	if len(currentHeadings) == 0 && len(otherHeadings) == 0 {
		return 0.0
	}

	// Get stopwords
	stopwords := map[string]bool{
		"the": true, "a": true, "an": true, "is": true, "are": true, "was": true, "were": true,
		"be": true, "been": true, "being": true, "have": true, "has": true, "had": true,
		"do": true, "does": true, "did": true, "will": true, "would": true, "shall": true,
		"should": true, "may": true, "might": true, "can": true, "could": true, "and": true,
		"but": true, "or": true, "nor": true, "not": true, "so": true, "yet": true, "for": true,
		"in": true, "on": true, "at": true, "to": true, "of": true, "by": true, "with": true,
		"from": true, "up": true, "out": true, "off": true, "over": true, "into": true,
		"then": true, "than": true, "this": true, "that": true, "these": true, "those": true,
		"it": true, "its": true, "spec": true, "specification": true, "overview": true,
		"document": true, "file": true, "section": true,
	}

	// Extract tokens from headings
	getTokens := func(headings []string) map[string]bool {
		tokens := make(map[string]bool)
		for _, h := range headings {
			words := strings.Fields(strings.ToLower(h))
			for _, w := range words {
				// Remove punctuation
				w = strings.Trim(w, ".,;:!?\"'()[]{}")
				if w != "" && !stopwords[w] {
					tokens[w] = true
				}
			}
		}
		return tokens
	}

	currentTokens := getTokens(currentHeadings)
	otherTokens := getTokens(otherHeadings)

	// Add filename tokens
	filenameTokens := strings.Split(otherFileName, "-")
	for _, tok := range filenameTokens {
		tok = strings.ReplaceAll(tok, "_", "-")
		tok = strings.ToLower(tok)
		tok = strings.TrimSuffix(tok, ".md")
		if tok != "" && !stopwords[tok] {
			otherTokens[tok] = true
		}
	}

	// Compute Jaccard similarity
	if len(currentTokens) == 0 && len(otherTokens) == 0 {
		return 0.0
	}

	intersection := 0
	union := 0

	for token := range currentTokens {
		union++
		if otherTokens[token] {
			intersection++
		}
	}

	for token := range otherTokens {
		if !currentTokens[token] {
			union++
		}
	}

	if union == 0 {
		return 0.0
	}

	return float64(intersection) / float64(union)
}

// computeDirProximity computes directory proximity score
func computeDirProximity(dir1, dir2 string) float64 {
	// Normalize paths
	dir1 = strings.Trim(dir1, "/")
	dir2 = strings.Trim(dir2, "/")

	// Same directory
	if dir1 == dir2 {
		return 1.0
	}

	// Parent/child relationship
	if strings.HasPrefix(dir1, dir2+"/") || strings.HasPrefix(dir2, dir1+"/") {
		return 0.6
	}

	// Sibling directories (share parent)
	parent1 := filepath.Dir(dir1)
	parent2 := filepath.Dir(dir2)
	if parent1 == parent2 && parent1 != "." {
		return 0.4
	}

	// Share a common path prefix
	parts1 := strings.Split(dir1, "/")
	parts2 := strings.Split(dir2, "/")

	commonPrefix := 0
	minLen := len(parts1)
	if len(parts2) < minLen {
		minLen = len(parts2)
	}

	for i := 0; i < minLen; i++ {
		if parts1[i] == parts2[i] {
			commonPrefix++
		} else {
			break
		}
	}

	if commonPrefix > 0 {
		return 0.2
	}

	// Unrelated
	return 0.0
}

// handleRecent returns recent file changes grouped by git commit
func handleRecent(w http.ResponseWriter, r *http.Request) {
	if !isGitRepo() {
		resp := RecentResponse{
			Project: projectName,
			Groups:  []RecentGroup{},
			IsGit:   false,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	var groups []RecentGroup

	// 1. Uncommitted group - get changed .md files
	changedFiles := getGitChangedFiles()
	var uncommittedFiles []RecentFileInfo

	for path := range changedFiles {
		// Filter to .md files only
		if !strings.HasSuffix(strings.ToLower(path), ".md") {
			continue
		}
		cleanPath := filepath.Clean(path)
		absPath := filepath.Join(projectDir, cleanPath)
		stat, err := os.Stat(absPath)
		if err != nil {
			// File was deleted, skip it
			continue
		}
		uncommittedFiles = append(uncommittedFiles, RecentFileInfo{
			Path:    cleanPath,
			Name:    filepath.Base(cleanPath),
			Dir:     filepath.Dir(cleanPath),
			ModTime: stat.ModTime().UnixMilli(),
		})
	}

	// Sort by ModTime descending
	sort.Slice(uncommittedFiles, func(i, j int) bool {
		return uncommittedFiles[i].ModTime > uncommittedFiles[j].ModTime
	})

	// Only add uncommitted group if there are files
	if len(uncommittedFiles) > 0 {
		groups = append(groups, RecentGroup{
			Type:      "uncommitted",
			Message:   "",
			ShortHash: "",
			Age:       "",
			Date:      0,
			Files:     uncommittedFiles,
		})
	}

	// 2. Commit groups - get last 10 commits that touched .md files
	cmd := exec.Command("git", "log", "--format=%H%x00%h%x00%s%x00%ar%x00%at", "-10", "--diff-filter=ACMR", "--name-only", "--", "*.md", "**/*.md")
	cmd.Dir = projectDir
	out, err := cmd.Output()
	if err == nil && len(strings.TrimSpace(string(out))) > 0 {
		// Parse the output
		lines := strings.Split(string(out), "\n")
		var commits []struct {
			hash    string
			short   string
			message string
			age     string
			date    int64
			files   []string
		}

		var current *struct {
			hash    string
			short   string
			message string
			age     string
			date    int64
			files   []string
		}

		for _, line := range lines {
			if strings.Contains(line, "\x00") {
				// This is a commit header line
				// Save previous commit if exists
				if current != nil && len(current.files) > 0 {
					commits = append(commits, *current)
				}
				// Start new commit
				fields := strings.SplitN(line, "\x00", 5)
				if len(fields) >= 5 {
					current = &struct {
						hash    string
						short   string
						message string
						age     string
						date    int64
						files   []string
					}{
						hash:    fields[0],
						short:   fields[1],
						message: fields[2],
						age:     fields[3],
						date:    0,
						files:   []string{},
					}
					// Parse date (unix seconds)
					fmt.Sscanf(fields[4], "%d", &current.date)
					current.date = current.date * 1000 // convert to milliseconds
				}
			} else if strings.TrimSpace(line) != "" && current != nil {
				// This is a filename line
				filePath := strings.TrimSpace(line)
				current.files = append(current.files, filePath)
			}
		}

		// Don't forget the last commit
		if current != nil && len(current.files) > 0 {
			commits = append(commits, *current)
		}

		// Get git root for path conversion (once, outside loop)
		rootCmd := exec.Command("git", "rev-parse", "--show-toplevel")
		rootCmd.Dir = projectDir
		rootOut, _ := rootCmd.Output()
		gitRoot := strings.TrimSpace(string(rootOut))

		// Process commits and create RecentGroup for each
		for _, commit := range commits {
			if len(groups) >= 5 {
				// Cap at 5 commit groups
				break
			}

			var groupFiles []RecentFileInfo
			for _, filePath := range commit.files {
				filePath = strings.TrimSpace(filePath)
				if filePath == "" {
					continue
				}

				// Convert from git-root-relative to projectDir-relative
				absPath := filepath.Join(gitRoot, filePath)
				relPath, err := filepath.Rel(projectDir, absPath)
				if err != nil || strings.HasPrefix(relPath, "..") {
					continue
				}

				// Check if file still exists
				stat, err := os.Stat(absPath)
				if err != nil {
					// File was deleted, skip it
					continue
				}

				cleanRelPath := filepath.Clean(relPath)
				groupFiles = append(groupFiles, RecentFileInfo{
					Path:    cleanRelPath,
					Name:    filepath.Base(cleanRelPath),
					Dir:     filepath.Dir(cleanRelPath),
					ModTime: stat.ModTime().UnixMilli(),
				})
			}

			// Only add group if it has files
			if len(groupFiles) > 0 {
				// Cap at 4 files per group
				if len(groupFiles) > 4 {
					groupFiles = groupFiles[:4]
				}

				groups = append(groups, RecentGroup{
					Type:      "commit",
					Message:   commit.message,
					ShortHash: commit.short,
					Age:       commit.age,
					Date:      commit.date,
					Files:     groupFiles,
				})
			}
		}
	}

	if groups == nil {
		groups = []RecentGroup{}
	}

	resp := RecentResponse{
		Project: projectName,
		Groups:  groups,
		IsGit:   true,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// imageExtensions contains allowed image file extensions for the asset endpoint
var imageExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
	".webp": true, ".svg": true, ".bmp": true, ".ico": true,
	".tiff": true, ".tif": true, ".avif": true,
}

func handleAsset(w http.ResponseWriter, r *http.Request) {
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

	// Only serve image files
	ext := strings.ToLower(filepath.Ext(cleanPath))
	if !imageExtensions[ext] {
		http.Error(w, "forbidden file type", http.StatusForbidden)
		return
	}

	absPath := filepath.Join(projectDir, cleanPath)

	// Verify the resolved path is still within the project directory
	absPath, err := filepath.Abs(absPath)
	if err != nil || !strings.HasPrefix(absPath, projectDir) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	// Detect content type
	contentType := mime.TypeByExtension(ext)
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// Serve the file
	data, err := os.ReadFile(absPath)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(data)
}

func init() {
	// Ensure time formatting is consistent
	time.Local = time.UTC
}
