package conv

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func mustParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func TestEncodeWorkDir(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/Users/chris/code/myproject", "-Users-chris-code-myproject"},
		{"/tmp/foo", "-tmp-foo"},
		{"/", "-"},
		{"/Users/csells/gt/hello_gastown/crew/bob", "-Users-csells-gt-hello-gastown-crew-bob"},
	}
	for _, tt := range tests {
		got := encodeWorkDir(tt.input)
		if got != tt.want {
			t.Errorf("encodeWorkDir(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestClaudeDiscovererFindsFiles(t *testing.T) {
	root := t.TempDir()
	workDir := "/Users/chris/code/myproject"
	encoded := encodeWorkDir(workDir)
	projectDir := filepath.Join(root, "projects", encoded)

	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a conversation file
	convPath := filepath.Join(projectDir, "abc123.jsonl")
	if err := os.WriteFile(convPath, []byte(`{"type":"user"}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a subagent file
	subPath := filepath.Join(projectDir, "agent-sub1.jsonl")
	if err := os.WriteFile(subPath, []byte(`{"type":"assistant"}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	disc := NewClaudeDiscoverer(root)
	result, err := disc.FindConversations("test-agent", workDir)
	if err != nil {
		t.Fatalf("FindConversations() error = %v", err)
	}

	if len(result.WatchDirs) != 1 || result.WatchDirs[0] != projectDir {
		t.Fatalf("WatchDirs = %v, want [%s]", result.WatchDirs, projectDir)
	}

	if len(result.Files) != 2 {
		t.Fatalf("got %d files, want 2", len(result.Files))
	}

	// Verify non-subagent file
	var mainFile, subFile *ConversationFile
	for i := range result.Files {
		if !result.Files[i].IsSubagent {
			mainFile = &result.Files[i]
		} else {
			subFile = &result.Files[i]
		}
	}

	if mainFile == nil {
		t.Fatal("no main conversation file found")
	}
	if mainFile.ConversationID != "claude:test-agent:abc123" {
		t.Fatalf("ConversationID = %q, want %q", mainFile.ConversationID, "claude:test-agent:abc123")
	}
	if mainFile.Runtime != "claude" {
		t.Fatalf("Runtime = %q, want %q", mainFile.Runtime, "claude")
	}

	if subFile == nil {
		t.Fatal("no subagent file found")
	}
	if !subFile.IsSubagent {
		t.Fatal("subagent file should have IsSubagent=true")
	}
}

func TestClaudeDiscovererMissingDir(t *testing.T) {
	root := t.TempDir()

	disc := NewClaudeDiscoverer(root)
	result, err := disc.FindConversations("test-agent", "/nonexistent/path")
	if err != nil {
		t.Fatalf("FindConversations() error = %v (should return empty, not error)", err)
	}

	if len(result.Files) != 0 {
		t.Fatalf("got %d files, want 0 for missing directory", len(result.Files))
	}

	if len(result.WatchDirs) == 0 {
		t.Fatal("WatchDirs should be set even for missing directory")
	}
}

func TestConversationIDUniqueness(t *testing.T) {
	// Two agents with the same native file should produce different ConversationIDs
	root := t.TempDir()
	workDir1 := "/tmp/project1"
	workDir2 := "/tmp/project2"

	for _, wd := range []string{workDir1, workDir2} {
		dir := filepath.Join(root, "projects", encodeWorkDir(wd))
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "same-id.jsonl"), []byte(`{"type":"user"}`+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	disc := NewClaudeDiscoverer(root)

	r1, _ := disc.FindConversations("agent-a", workDir1)
	r2, _ := disc.FindConversations("agent-b", workDir2)

	if len(r1.Files) == 0 || len(r2.Files) == 0 {
		t.Fatal("expected files from both agents")
	}

	if r1.Files[0].ConversationID == r2.Files[0].ConversationID {
		t.Fatalf("ConversationIDs should differ: %q == %q", r1.Files[0].ConversationID, r2.Files[0].ConversationID)
	}
}

func TestNewClaudeDiscoverer(t *testing.T) {
	// Explicit root
	disc := NewClaudeDiscoverer("/custom/root")
	if disc.Root != "/custom/root" {
		t.Fatalf("Root = %q, want %q", disc.Root, "/custom/root")
	}

	// Empty root defaults to $HOME/.claude
	disc2 := NewClaudeDiscoverer("")
	home := os.Getenv("HOME")
	want := filepath.Join(home, ".claude")
	if disc2.Root != want {
		t.Fatalf("Root = %q, want %q", disc2.Root, want)
	}
}

func TestScanDirectoryEmpty(t *testing.T) {
	root := t.TempDir()
	workDir := "/tmp/empty-project"
	projectDir := filepath.Join(root, "projects", encodeWorkDir(workDir))
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}

	disc := NewClaudeDiscoverer(root)
	result, err := disc.FindConversations("test-agent", workDir)
	if err != nil {
		t.Fatalf("FindConversations() error = %v", err)
	}
	if len(result.Files) != 0 {
		t.Fatalf("got %d files, want 0 for empty directory", len(result.Files))
	}
}

func TestScanDirectorySkipsNonJSONL(t *testing.T) {
	root := t.TempDir()
	workDir := "/tmp/mixed-files"
	projectDir := filepath.Join(root, "projects", encodeWorkDir(workDir))
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create various file types — only .jsonl should be discovered
	for _, name := range []string{"notes.txt", "config.json", "readme.md"} {
		if err := os.WriteFile(filepath.Join(projectDir, name), []byte("data"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	// One actual JSONL file
	if err := os.WriteFile(filepath.Join(projectDir, "conv1.jsonl"), []byte(`{"type":"user"}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	disc := NewClaudeDiscoverer(root)
	result, err := disc.FindConversations("test-agent", workDir)
	if err != nil {
		t.Fatalf("FindConversations() error = %v", err)
	}
	if len(result.Files) != 1 {
		t.Fatalf("got %d files, want 1 (only .jsonl)", len(result.Files))
	}
	if result.Files[0].NativeConversationID != "conv1" {
		t.Fatalf("NativeConversationID = %q, want %q", result.Files[0].NativeConversationID, "conv1")
	}
}

func TestScanDirectorySkipsSubdirectories(t *testing.T) {
	root := t.TempDir()
	workDir := "/tmp/with-subdirs"
	projectDir := filepath.Join(root, "projects", encodeWorkDir(workDir))
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a subdirectory (should be skipped)
	subDir := filepath.Join(projectDir, "subdir.jsonl")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a real file
	if err := os.WriteFile(filepath.Join(projectDir, "real.jsonl"), []byte(`{"type":"user"}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	disc := NewClaudeDiscoverer(root)
	result, err := disc.FindConversations("test-agent", workDir)
	if err != nil {
		t.Fatalf("FindConversations() error = %v", err)
	}
	if len(result.Files) != 1 {
		t.Fatalf("got %d files, want 1 (subdirectory should be skipped)", len(result.Files))
	}
}

func TestScanDirectorySortsByMtime(t *testing.T) {
	root := t.TempDir()
	workDir := "/tmp/sorted-project"
	projectDir := filepath.Join(root, "projects", encodeWorkDir(workDir))
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create files with different mtimes (older first)
	files := []string{"older.jsonl", "middle.jsonl", "newest.jsonl"}
	for i, name := range files {
		path := filepath.Join(projectDir, name)
		if err := os.WriteFile(path, []byte(`{"type":"user"}`+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
		// Spread mtimes 1 second apart to ensure ordering
		mtime := filepath.Join(projectDir, name)
		t2 := mustParseTime(t, "2026-01-01T00:00:00Z").Add(time.Duration(i) * time.Second)
		if err := os.Chtimes(mtime, t2, t2); err != nil {
			t.Fatal(err)
		}
	}

	disc := NewClaudeDiscoverer(root)
	result, err := disc.FindConversations("test-agent", workDir)
	if err != nil {
		t.Fatalf("FindConversations() error = %v", err)
	}
	if len(result.Files) != 3 {
		t.Fatalf("got %d files, want 3", len(result.Files))
	}

	// Most recent file should be first (descending mtime order)
	if result.Files[0].NativeConversationID != "newest" {
		t.Fatalf("first file = %q, want %q (most recent)", result.Files[0].NativeConversationID, "newest")
	}
	if result.Files[2].NativeConversationID != "older" {
		t.Fatalf("last file = %q, want %q (oldest)", result.Files[2].NativeConversationID, "older")
	}
}
