package preview

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func createTestZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create entry %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("write entry %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
}

func TestParseDocx(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "sample.docx")
	createTestZip(t, fp, map[string]string{
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>测试标题</w:t></w:r></w:p>
    <w:p><w:r><w:t>第一段正文</w:t></w:r></w:p>
    <w:tbl><w:tr><w:tc><w:p><w:r><w:t>列1</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>列2</w:t></w:r></w:p></w:tc></w:tr></w:tbl>
  </w:body>
</w:document>`,
	})

	res, err := ParseOffice(fp, "docx")
	if err != nil {
		t.Fatalf("ParseOffice docx error: %v", err)
	}
	doc := res.(*DocxResult)
	if len(doc.Blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(doc.Blocks))
	}
	if doc.Blocks[0].Kind != "heading" || doc.Blocks[0].Text != "测试标题" {
		t.Fatalf("unexpected heading block: %+v", doc.Blocks[0])
	}
	if doc.Blocks[2].Kind != "table" || len(doc.Blocks[2].Rows) != 1 || doc.Blocks[2].Rows[0][1] != "列2" {
		t.Fatalf("unexpected table block: %+v", doc.Blocks[2])
	}
}

func TestParseXlsx(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "sample.xlsx")
	createTestZip(t, fp, map[string]string{
		"xl/workbook.xml": `<workbook><sheets><sheet name="销售表"/></sheets></workbook>`,
		"xl/sharedStrings.xml": `<sst><si><t>商品</t></si><si><t>数量</t></si><si><t>苹果</t></si></sst>`,
		"xl/worksheets/sheet1.xml": `<worksheet><sheetData>
  <row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row>
  <row r="2"><c r="A2" t="s"><v>2</v></c><c r="B2"><v>42</v></c></row>
</sheetData></worksheet>`,
	})

	res, err := ParseOffice(fp, "xlsx")
	if err != nil {
		t.Fatalf("ParseOffice xlsx error: %v", err)
	}
	xl := res.(*XlsxResult)
	if len(xl.Sheets) != 1 || xl.Sheets[0].Name != "销售表" {
		t.Fatalf("unexpected sheets: %+v", xl.Sheets)
	}
	if xl.Sheets[0].Rows[1][0] != "苹果" || xl.Sheets[0].Rows[1][1] != "42" {
		t.Fatalf("unexpected row 2: %+v", xl.Sheets[0].Rows[1])
	}
}

func TestParsePptx(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "sample.pptx")
	createTestZip(t, fp, map[string]string{
		"ppt/slides/slide1.xml": `<p:sld xmlns:p="urn:p" xmlns:a="urn:a"><p:cSld><p:spTree><p:sp><p:txBody><a:p><a:r><a:t>封面标题</a:t></a:r></a:p><a:p><a:r><a:t>副标题内容</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld></p:sld>`,
	})

	res, err := ParseOffice(fp, "pptx")
	if err != nil {
		t.Fatalf("ParseOffice pptx error: %v", err)
	}
	ppt := res.(*PptxResult)
	if len(ppt.Slides) != 1 || ppt.Slides[0].Title != "封面标题" || ppt.Slides[0].Lines[0] != "副标题内容" {
		t.Fatalf("unexpected slides: %+v", ppt.Slides)
	}
}
