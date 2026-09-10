package test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/WnJee/gorig-om/src/logtool"
)

func writeSearchTestLog(t *testing.T, path, prefix string, count int) {
	t.Helper()

	var content strings.Builder
	for i := 0; i < count; i++ {
		fmt.Fprintf(&content, `{"time":"2026-01-01 00:00:00.000","level":"info","msg":"%s-%d"}`+"\n", prefix, i)
	}
	if err := os.WriteFile(path, []byte(content.String()), 0644); err != nil {
		t.Fatal(err)
	}
}

func makeSearchTestLogs(t *testing.T, firstCount, secondCount int) (string, string, string) {
	t.Helper()

	root := t.TempDir()
	dir := filepath.Join(root, ".logs", "rest")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(dir, "rest-1.jsonl")
	second := filepath.Join(dir, "rest-2.jsonl")
	writeSearchTestLog(t, first, "first", firstCount)
	writeSearchTestLog(t, second, "second", secondCount)
	return root, first, second
}

func TestSearchLogsCapsSize(t *testing.T) {
	root, _, _ := makeSearchTestLogs(t, 1, 0)

	maxInt := int(^uint(0) >> 1)
	result, err := logtool.SearchLogs(logtool.SearchOptions{
		RootDir:    root,
		Categories: []string{"rest"},
		Size:       maxInt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 {
		t.Fatalf("got %d records, want 1", len(result))
	}
}

func TestSearchLogsParallelOrderingAndCursor(t *testing.T) {
	root, first, _ := makeSearchTestLogs(t, 120, 120)

	result, err := logtool.SearchLogs(logtool.SearchOptions{
		RootDir:    root,
		Categories: []string{"rest"},
		Size:       101,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 101 {
		t.Fatalf("got %d records, want 101", len(result))
	}
	for i, record := range result {
		if record.FilePath != first {
			t.Fatalf("result[%d] came from %q, want %q", i, record.FilePath, first)
		}
		if record.LineNumber != int64(i+1) {
			t.Fatalf("result[%d] line = %d, want %d", i, record.LineNumber, i+1)
		}
	}

	result, err = logtool.SearchLogs(logtool.SearchOptions{
		RootDir:    root,
		Categories: []string{"rest"},
		LastPath:   first,
		LastLine:   119,
		Size:       101,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 101 {
		t.Fatalf("cursor returned %d records, want 101", len(result))
	}
	if result[0].FilePath != first || result[0].LineNumber != 120 {
		t.Fatalf("cursor first result = %s:%d, want %s:120", result[0].FilePath, result[0].LineNumber, first)
	}
	if result[1].LineNumber != 1 || !strings.Contains(result[1].FilePath, "rest-2.jsonl") {
		t.Fatalf("cursor second result = %s:%d, want rest-2.jsonl:1", result[1].FilePath, result[1].LineNumber)
	}
}

func TestSearchLogsIgnoresLateFileErrorAfterLimit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission test is not portable to Windows")
	}

	root, _, second := makeSearchTestLogs(t, 101, 1)
	if err := os.Chmod(second, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(second, 0644)

	file, err := os.Open(second)
	if err == nil {
		file.Close()
		t.Skip("test process can read a mode 000 file")
	}

	result, searchErr := logtool.SearchLogs(logtool.SearchOptions{
		RootDir:    root,
		Categories: []string{"rest"},
		Size:       101,
	})
	if searchErr != nil {
		t.Fatalf("SearchLogs returned an error for an unnecessary later file: %v", searchErr)
	}
	if len(result) != 101 {
		t.Fatalf("got %d records, want 101", len(result))
	}
}
