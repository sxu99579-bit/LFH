package preview

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectAndReadZipArchive(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "bundle.zip")

	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	zw := zip.NewWriter(f)
	entries := map[string]string{
		"docs/说明.md":   "# 中文文档\n内容",
		"src/main.go":  "package main\nfunc main() {}\n",
		"../evil.txt":  "should-be-blocked",
	}
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create entry %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("write entry %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zw: %v", err)
	}
	f.Close()

	res, err := InspectArchive(zipPath, "bundle.zip")
	if err != nil {
		t.Fatalf("InspectArchive zip: %v", err)
	}
	if res.Format != "zip" || res.TotalFiles != 2 {
		t.Fatalf("expected 2 safe files (traversal skipped), got %+v", res)
	}

	data, name, ext, err := ReadArchiveEntry(zipPath, "bundle.zip", "docs/说明.md", MaxArchiveEntryReadBytes)
	if err != nil {
		t.Fatalf("ReadArchiveEntry: %v", err)
	}
	if name != "说明.md" || ext != "md" || !strings.Contains(string(data), "中文文档") {
		t.Fatalf("unexpected entry data: name=%q ext=%q data=%q", name, ext, string(data))
	}

	if _, _, _, err := ReadArchiveEntry(zipPath, "bundle.zip", "../evil.txt", MaxArchiveEntryReadBytes); err == nil {
		t.Fatalf("expected traversal path to be rejected")
	}

	var walked []string
	err = WalkArchiveFiles(zipPath, "bundle.zip", 100, 10*1024*1024, func(relPath string, size int64, r io.Reader) error {
		b, _ := io.ReadAll(r)
		if len(b) == 0 {
			t.Fatalf("empty walked entry %s", relPath)
		}
		walked = append(walked, relPath)
		return nil
	})
	if err != nil || len(walked) != 2 {
		t.Fatalf("WalkArchiveFiles error=%v walked=%v", err, walked)
	}
}

func TestInspectAndReadTarGzArchive(t *testing.T) {
	dir := t.TempDir()
	tgzPath := filepath.Join(dir, "pkg.tar.gz")

	f, err := os.Create(tgzPath)
	if err != nil {
		t.Fatalf("create tgz: %v", err)
	}
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	content := []byte("hello tar gz")
	if err := tw.WriteHeader(&tar.Header{
		Name:     "sub/hello.txt",
		Mode:     0644,
		Size:     int64(len(content)),
		Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatalf("tar write: %v", err)
	}
	tw.Close()
	gw.Close()
	f.Close()

	res, err := InspectArchive(tgzPath, "pkg.tar.gz")
	if err != nil {
		t.Fatalf("InspectArchive tgz: %v", err)
	}
	if res.Format != "tar.gz" || res.TotalFiles != 1 || res.Entries[0].Path != "sub/hello.txt" {
		t.Fatalf("unexpected tar.gz result: %+v", res)
	}

	data, _, ext, err := ReadArchiveEntry(tgzPath, "pkg.tar.gz", "sub/hello.txt", MaxArchiveEntryReadBytes)
	if err != nil || ext != "txt" || string(data) != "hello tar gz" {
		t.Fatalf("ReadArchiveEntry tgz failed: err=%v ext=%q data=%q", err, ext, string(data))
	}
}
