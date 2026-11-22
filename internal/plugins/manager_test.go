package plugins

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPluginMarketplaceInstallToggleUninstallAndReload(t *testing.T) {
	dataDir := t.TempDir()
	pm, err := NewManager(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(pm.Marketplace()) < 3 {
		t.Fatalf("expected >= 3 marketplace items, got %d", len(pm.Marketplace()))
	}

	mf, err := pm.InstallFromMarketplace(PluginOCRVision)
	if err != nil || !mf.Enabled {
		t.Fatalf("InstallFromMarketplace failed: mf=%+v err=%v", mf, err)
	}

	mf, err = pm.SetEnabled(PluginOCRVision, false)
	if err != nil || mf.Enabled {
		t.Fatalf("SetEnabled(false) failed: mf=%+v err=%v", mf, err)
	}

	reloaded, err := NewManager(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	caps := reloaded.Capabilities()
	if len(caps) != 1 || caps[0].PluginID != PluginOCRVision || caps[0].Enabled {
		t.Fatalf("unexpected reloaded capabilities: %+v", caps)
	}

	if err := reloaded.Uninstall(PluginOCRVision); err != nil {
		t.Fatalf("Uninstall failed: %v", err)
	}
	if len(reloaded.ListInstalled()) != 0 {
		t.Fatalf("expected 0 installed plugins after uninstall")
	}
}

func TestBuiltinPluginsExecution(t *testing.T) {
	dataDir := t.TempDir()
	pm, err := NewManager(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{PluginOCRVision, PluginAutoOrganizer, PluginPDFToWord} {
		if _, err := pm.InstallFromMarketplace(id); err != nil {
			t.Fatalf("install %s: %v", id, err)
		}
	}

	ctx := context.Background()

	// 1. Test auto-organizer on invoice text file.
	orgWork := filepath.Join(t.TempDir(), "org")
	invPath := filepath.Join(t.TempDir(), "2026-报销发票.txt")
	if err := os.WriteFile(invPath, []byte("发票代码: 1100223344 金额: 100.00"), 0644); err != nil {
		t.Fatal(err)
	}
	orgRes, _, err := pm.Execute(ctx, ExecRequest{
		Capability: CapAutoClassify,
		FileName:   "2026-报销发票.txt",
		InputPath:  invPath,
		WorkDir:    orgWork,
	})
	if err != nil {
		t.Fatalf("Execute auto-organizer: %v", err)
	}
	orgRaw, err := os.ReadFile(orgRes.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(orgRaw, &parsed); err != nil || parsed["category"] != "财务票据" {
		t.Fatalf("unexpected auto-organizer output: %s", string(orgRaw))
	}

	// 2. Test ocr-vision on generated PNG.
	ocrWork := filepath.Join(t.TempDir(), "ocr")
	pngPath := filepath.Join(t.TempDir(), "sample.png")
	img := image.NewRGBA(image.Rect(0, 0, 32, 16))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	pf, err := os.Create(pngPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = png.Encode(pf, img)
	_ = pf.Close()
	ocrRes, _, err := pm.Execute(ctx, ExecRequest{
		Capability: CapOCRImage,
		FileName:   "sample.png",
		InputPath:  pngPath,
		WorkDir:    ocrWork,
	})
	if err != nil {
		t.Fatalf("Execute ocr-vision: %v", err)
	}
	ocrRaw, err := os.ReadFile(ocrRes.OutputPath)
	if err != nil || !strings.Contains(string(ocrRaw), "32x16") {
		t.Fatalf("unexpected ocr output: err=%v body=%q", err, string(ocrRaw))
	}

	// 3. Test pdf-to-word on synthetic PDF with FlateDecode compressed text stream.
	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	_, _ = zw.Write([]byte("BT /F1 12 Tf 72 720 Td (Hello LFH Distributed PDF Compute) Tj ET"))
	_ = zw.Close()

	var pdfBuf bytes.Buffer
	pdfBuf.WriteString("%PDF-1.4\n1 0 obj<<>>stream\n")
	pdfBuf.Write(compressed.Bytes())
	pdfBuf.WriteString("\nendstream\nendobj\n%%EOF")

	pdfPath := filepath.Join(t.TempDir(), "contract.pdf")
	if err := os.WriteFile(pdfPath, pdfBuf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	pdfWork := filepath.Join(t.TempDir(), "pdf")
	pdfRes, _, err := pm.Execute(ctx, ExecRequest{
		Capability: CapPDFToDocx,
		FileName:   "contract.pdf",
		InputPath:  pdfPath,
		WorkDir:    pdfWork,
	})
	if err != nil {
		t.Fatalf("Execute pdf-to-word: %v", err)
	}
	if !strings.HasSuffix(pdfRes.OutputName, ".docx") {
		t.Fatalf("expected .docx output, got %s", pdfRes.OutputName)
	}
	zr, err := zip.OpenReader(pdfRes.OutputPath)
	if err != nil {
		t.Fatalf("generated .docx must be valid zip: %v", err)
	}
	defer zr.Close()
	foundDocText := false
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			rc, _ := f.Open()
			xmlRaw, _ := io.ReadAll(rc)
			_ = rc.Close()
			if strings.Contains(string(xmlRaw), "Hello LFH Distributed PDF Compute") {
				foundDocText = true
			}
		}
	}
	if !foundDocText {
		t.Fatal("expected word/document.xml inside generated .docx to contain extracted PDF text")
	}
}
