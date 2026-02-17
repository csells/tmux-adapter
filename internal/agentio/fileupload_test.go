package agentio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFileUploadPayload(t *testing.T) {
	payload := []byte("report.pdf\x00application/pdf\x00PDF-DATA")

	fileName, mimeType, data, err := ParseFileUploadPayload(payload)
	if err != nil {
		t.Fatalf("ParseFileUploadPayload() unexpected error: %v", err)
	}
	if fileName != "report.pdf" {
		t.Fatalf("fileName = %q, want %q", fileName, "report.pdf")
	}
	if mimeType != "application/pdf" {
		t.Fatalf("mimeType = %q, want %q", mimeType, "application/pdf")
	}
	if string(data) != "PDF-DATA" {
		t.Fatalf("data = %q, want %q", string(data), "PDF-DATA")
	}
}

func TestParseFileUploadPayloadEmptyFilenameDefaults(t *testing.T) {
	payload := []byte("\x00application/octet-stream\x00XYZ")

	fileName, _, _, err := ParseFileUploadPayload(payload)
	if err != nil {
		t.Fatalf("ParseFileUploadPayload() unexpected error: %v", err)
	}
	if fileName != "attachment.bin" {
		t.Fatalf("fileName = %q, want %q", fileName, "attachment.bin")
	}
}

func TestParseFileUploadPayloadErrors(t *testing.T) {
	cases := []struct {
		name    string
		payload []byte
	}{
		{name: "missing_filename_separator", payload: []byte("file-only")},
		{name: "missing_mime_separator", payload: []byte("file.txt\x00text/plain")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, err := ParseFileUploadPayload(tc.payload)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestBuildServerPastePath(t *testing.T) {
	workDir := filepath.Join(string(filepath.Separator), "srv", "agent")
	inside := filepath.Join(workDir, ".tmux-adapter", "uploads", "doc.pdf")
	outside := filepath.Join(string(filepath.Separator), "tmp", "doc.pdf")

	gotInside := BuildServerPastePath(workDir, inside)
	if gotInside != ".tmux-adapter/uploads/doc.pdf" {
		t.Fatalf("inside path = %q, want %q", gotInside, ".tmux-adapter/uploads/doc.pdf")
	}

	gotOutside := BuildServerPastePath(workDir, outside)
	if gotOutside != outside {
		t.Fatalf("outside path = %q, want absolute fallback %q", gotOutside, outside)
	}

	gotNoWorkDir := BuildServerPastePath("", inside)
	if gotNoWorkDir != inside {
		t.Fatalf("empty workDir path = %q, want %q", gotNoWorkDir, inside)
	}
}

func TestBuildPastePayload(t *testing.T) {
	savedPath := "/srv/agent/.tmux-adapter/uploads/data.bin"
	pastePath := "./.tmux-adapter/uploads/data.bin"

	smallText := []byte("hello\nworld")
	gotText := BuildPastePayload(savedPath, pastePath, "text/plain", smallText)
	if string(gotText) != string(smallText) {
		t.Fatalf("small text payload should be pasted inline")
	}

	largeText := make([]byte, maxInlinePasteBytes+1)
	for i := range largeText {
		largeText[i] = 'a'
	}
	gotLarge := BuildPastePayload(savedPath, pastePath, "text/plain", largeText)
	if string(gotLarge) != pastePath+" " {
		t.Fatalf("large text payload = %q, want %q", string(gotLarge), pastePath+" ")
	}

	binaryData := []byte{0x00, 0x01, 0x02}
	gotBinary := BuildPastePayload(savedPath, pastePath, "application/octet-stream", binaryData)
	if string(gotBinary) != pastePath+" " {
		t.Fatalf("binary payload = %q, want %q", string(gotBinary), pastePath+" ")
	}

	imgData := []byte{0x89, 0x50, 0x4E, 0x47} // PNG header bytes
	gotImg := BuildPastePayload(savedPath, pastePath, "image/png", imgData)
	if string(gotImg) != savedPath+" " {
		t.Fatalf("image payload = %q, want absolute path %q", string(gotImg), savedPath+" ")
	}
}

func TestSanitizePathComponent(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{in: "", want: "attachment.bin"},
		{in: "safe-name.txt", want: "safe-name.txt"},
		{in: "hello world.txt", want: "hello_world.txt"},
		{in: ".hidden", want: "hidden"},
	}

	for _, tc := range cases {
		got := SanitizePathComponent(tc.in)
		if got != tc.want {
			t.Fatalf("SanitizePathComponent(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSaveUploadedFile(t *testing.T) {
	workDir := t.TempDir()
	content := []byte("hello world upload")

	savedPath, err := SaveUploadedFile(workDir, "test-agent", "myfile.txt", content)
	if err != nil {
		t.Fatalf("SaveUploadedFile() error: %v", err)
	}

	got, err := os.ReadFile(savedPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error: %v", savedPath, err)
	}
	if string(got) != string(content) {
		t.Fatalf("file content = %q, want %q", string(got), string(content))
	}

	expectedPrefix := filepath.Join(workDir, ".tmux-adapter", "uploads")
	if !strings.HasPrefix(savedPath, expectedPrefix) {
		t.Fatalf("savedPath %q not under expected uploads dir %q", savedPath, expectedPrefix)
	}
}

func TestSaveUploadedFileFallback(t *testing.T) {
	content := []byte("fallback test data")

	savedPath, err := SaveUploadedFile("", "test-agent", "data.bin", content)
	if err != nil {
		t.Fatalf("SaveUploadedFile() error: %v", err)
	}
	defer func() { _ = os.Remove(savedPath) }()

	got, err := os.ReadFile(savedPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error: %v", savedPath, err)
	}
	if string(got) != string(content) {
		t.Fatalf("file content = %q, want %q", string(got), string(content))
	}

	expectedDir := filepath.Join(os.TempDir(), "tmux-adapter", "uploads", "test-agent")
	if !strings.HasPrefix(savedPath, expectedDir) {
		t.Fatalf("savedPath %q not under expected fallback dir %q", savedPath, expectedDir)
	}
}

func TestSaveUploadedFileSanitizesName(t *testing.T) {
	workDir := t.TempDir()
	content := []byte("sanitized content")

	savedPath, err := SaveUploadedFile(workDir, "test-agent", "../../etc/passwd", content)
	if err != nil {
		t.Fatalf("SaveUploadedFile() error: %v", err)
	}

	base := filepath.Base(savedPath)
	if strings.Contains(base, "..") {
		t.Fatalf("savedPath base %q contains path traversal", base)
	}

	got, err := os.ReadFile(savedPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error: %v", savedPath, err)
	}
	if string(got) != string(content) {
		t.Fatalf("file content = %q, want %q", string(got), string(content))
	}

	expectedPrefix := filepath.Join(workDir, ".tmux-adapter", "uploads")
	if !strings.HasPrefix(savedPath, expectedPrefix) {
		t.Fatalf("savedPath %q not under workDir uploads, path traversal may have escaped", savedPath)
	}
}

func TestIsTextLikeEdgeCases(t *testing.T) {
	cases := []struct {
		name     string
		mimeType string
		data     []byte
		want     bool
	}{
		{
			name:     "application_json_valid_utf8",
			mimeType: "application/json",
			data:     []byte(`{"key": "value"}`),
			want:     true,
		},
		{
			name:     "text_prefix_binary_data",
			mimeType: "text/plain",
			data:     []byte{0x00, 0x01, 0x02, 0x03},
			want:     false,
		},
		{
			name:     "empty_data",
			mimeType: "text/plain",
			data:     []byte{},
			want:     true,
		},
		{
			name:     "application_xml_valid",
			mimeType: "application/xml",
			data:     []byte("<root>hello</root>"),
			want:     true,
		},
		{
			name:     "application_javascript_valid",
			mimeType: "application/javascript",
			data:     []byte("console.log('hi')"),
			want:     true,
		},
		{
			name:     "unknown_mime_valid_utf8",
			mimeType: "application/octet-stream",
			data:     []byte("actually just text"),
			want:     true,
		},
		{
			name:     "unknown_mime_binary",
			mimeType: "application/octet-stream",
			data:     []byte{0x89, 0x50, 0x4E, 0x47, 0x00},
			want:     false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsTextLike(tc.mimeType, tc.data)
			if got != tc.want {
				t.Fatalf("IsTextLike(%q, ...) = %v, want %v", tc.mimeType, got, tc.want)
			}
		})
	}
}

func TestNewPrompterAndGetLock(t *testing.T) {
	p := NewPrompter(nil, nil)
	if p == nil {
		t.Fatal("NewPrompter returned nil")
	}
	if p.locks == nil {
		t.Fatal("locks map not initialized")
	}

	// GetLock returns a new mutex for a new agent
	lock1 := p.GetLock("agent-a")
	if lock1 == nil {
		t.Fatal("GetLock returned nil")
	}

	// Same agent returns same mutex
	lock2 := p.GetLock("agent-a")
	if lock1 != lock2 {
		t.Fatal("GetLock returned different mutex for same agent")
	}

	// Different agent returns different mutex
	lock3 := p.GetLock("agent-b")
	if lock1 == lock3 {
		t.Fatal("GetLock returned same mutex for different agents")
	}
}

func TestCopyToLocalClipboard(t *testing.T) {
	// On macOS, pbcopy should be available; on CI it may not be.
	// Test that the function at least doesn't panic.
	err := CopyToLocalClipboard([]byte("test clipboard data"))
	// We can't guarantee a clipboard tool exists, so just verify
	// the function returns without panicking. If it errors, that's OK.
	_ = err
}

func TestBuildServerPastePathDotDot(t *testing.T) {
	// When the saved path is outside and above the workdir, filepath.Rel
	// returns a ".." prefix — should fall back to absolute path
	workDir := filepath.Join(string(filepath.Separator), "srv", "deep", "agent")
	outsideAbove := filepath.Join(string(filepath.Separator), "srv", "other.txt")

	got := BuildServerPastePath(workDir, outsideAbove)
	if got != outsideAbove {
		t.Fatalf("outside-above path = %q, want absolute fallback %q", got, outsideAbove)
	}
}

func TestSanitizePathComponentSpecialChars(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{in: "/", want: "attachment.bin"},
		{in: ".", want: "attachment.bin"},
		{in: "file@name#test!.txt", want: "file_name_test_.txt"},
		{in: "normal_file-name.go", want: "normal_file-name.go"},
		{in: "  spaces  ", want: "spaces"},
	}
	for _, tc := range cases {
		got := SanitizePathComponent(tc.in)
		if got != tc.want {
			t.Fatalf("SanitizePathComponent(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIsUTF8TextEdgeCases(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want bool
	}{
		{
			name: "null_bytes",
			data: []byte("hello\x00world"),
			want: false,
		},
		{
			name: "control_char_0x01",
			data: []byte("hello\x01world"),
			want: false,
		},
		{
			name: "control_char_0x1F",
			data: []byte("hello\x1Fworld"),
			want: false,
		},
		{
			name: "valid_utf8_with_newlines_tabs",
			data: []byte("hello\n\tworld\r\n"),
			want: true,
		},
		{
			name: "invalid_utf8",
			data: []byte{0xFF, 0xFE, 0x80, 0x81},
			want: false,
		},
		{
			name: "empty",
			data: []byte{},
			want: true,
		},
		{
			name: "control_after_4096_sample_limit",
			data: func() []byte {
				d := make([]byte, 5000)
				for i := range d {
					d[i] = 'a'
				}
				d[4500] = 0x01 // control char after sample boundary
				return d
			}(),
			want: true, // sample only checks first 4096 bytes
		},
		{
			name: "control_before_4096_sample_limit",
			data: func() []byte {
				d := make([]byte, 5000)
				for i := range d {
					d[i] = 'a'
				}
				d[100] = 0x01 // control char within sample
				return d
			}(),
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsUTF8Text(tc.data)
			if got != tc.want {
				t.Fatalf("IsUTF8Text(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}
