package preview

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type DocxBlock struct {
	Kind  string     `json:"kind"` // "heading", "paragraph", "table"
	Level int        `json:"level,omitempty"`
	Text  string     `json:"text,omitempty"`
	Rows  [][]string `json:"rows,omitempty"`
}

type DocxResult struct {
	Type   string      `json:"type"`
	Blocks []DocxBlock `json:"blocks"`
}

type XlsxSheet struct {
	Name string     `json:"name"`
	Rows [][]string `json:"rows"`
}

type XlsxResult struct {
	Type   string      `json:"type"`
	Sheets []XlsxSheet `json:"sheets"`
}

type PptxSlide struct {
	Number int      `json:"number"`
	Title  string   `json:"title"`
	Lines  []string `json:"lines"`
}

type PptxResult struct {
	Type   string      `json:"type"`
	Slides []PptxSlide `json:"slides"`
}

func ParseOffice(filePath, ext string) (any, error) {
	ext = strings.ToLower(strings.TrimPrefix(ext, "."))
	switch ext {
	case "docx":
		return parseDocx(filePath)
	case "xlsx":
		return parseXlsx(filePath)
	case "pptx":
		return parsePptx(filePath)
	case "doc", "xls", "ppt":
		return nil, fmt.Errorf("暂不支持旧版二进制 .%s 在线解析，请转换为 .%sx 或直接下载查看", ext, ext)
	default:
		return nil, errors.New("不支持的文档格式")
	}
}

func readZipFile(zr *zip.ReadCloser, name string) ([]byte, error) {
	for _, f := range zr.File {
		if strings.EqualFold(f.Name, name) {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(io.LimitReader(rc, 32*1024*1024))
		}
	}
	return nil, fmt.Errorf("未找到 %s", name)
}

func parseDocx(filePath string) (*DocxResult, error) {
	zr, err := zip.OpenReader(filePath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	data, err := readZipFile(zr, "word/document.xml")
	if err != nil {
		return nil, err
	}

	dec := xml.NewDecoder(bytes.NewReader(data))
	res := &DocxResult{Type: "docx"}

	var inTable bool
	var currentRows [][]string
	var currentRow []string
	var inCell bool
	var cellBuf strings.Builder

	var inPara bool
	var paraStyle string
	var paraBuf strings.Builder
	var inText bool

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "tbl":
				inTable = true
				currentRows = nil
			case "tr":
				if inTable {
					currentRow = nil
				}
			case "tc":
				if inTable {
					inCell = true
					cellBuf.Reset()
				}
			case "p":
				inPara = true
				paraStyle = ""
				paraBuf.Reset()
			case "pStyle":
				if inPara {
					for _, attr := range t.Attr {
						if attr.Name.Local == "val" {
							paraStyle = attr.Value
						}
					}
				}
			case "t":
				if inPara {
					inText = true
				}
			case "br":
				if inPara {
					paraBuf.WriteString("\n")
				}
			case "tab":
				if inPara {
					paraBuf.WriteString("\t")
				}
			}
		case xml.CharData:
			if inText {
				paraBuf.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "p":
				if inPara {
					text := strings.TrimSpace(paraBuf.String())
					if inCell {
						if text != "" {
							if cellBuf.Len() > 0 {
								cellBuf.WriteString("\n")
							}
							cellBuf.WriteString(text)
						}
					} else if text != "" {
						level := headingLevel(paraStyle)
						if level > 0 {
							res.Blocks = append(res.Blocks, DocxBlock{
								Kind:  "heading",
								Level: level,
								Text:  text,
							})
						} else {
							res.Blocks = append(res.Blocks, DocxBlock{
								Kind: "paragraph",
								Text: text,
							})
						}
					}
					inPara = false
				}
			case "tc":
				if inTable && inCell {
					currentRow = append(currentRow, strings.TrimSpace(cellBuf.String()))
					inCell = false
				}
			case "tr":
				if inTable && len(currentRow) > 0 {
					currentRows = append(currentRows, currentRow)
				}
			case "tbl":
				if inTable && len(currentRows) > 0 {
					res.Blocks = append(res.Blocks, DocxBlock{
						Kind: "table",
						Rows: currentRows,
					})
				}
				inTable = false
			}
		}
	}
	return res, nil
}

func headingLevel(style string) int {
	s := strings.ToLower(style)
	if s == "title" {
		return 1
	}
	if strings.HasPrefix(s, "heading") {
		if n, err := strconv.Atoi(strings.TrimPrefix(s, "heading")); err == nil && n >= 1 && n <= 6 {
			return n
		}
		return 2
	}
	return 0
}

func parseXlsx(filePath string) (*XlsxResult, error) {
	zr, err := zip.OpenReader(filePath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	sharedStrings, _ := parseSharedStrings(zr)
	sheetNames, _ := parseWorkbookSheetNames(zr)

	var sheetFiles []string
	reSheet := regexp.MustCompile(`^xl/worksheets/sheet(\d+)\.xml$`)
	type numberedSheet struct {
		num  int
		path string
	}
	var numbered []numberedSheet
	for _, f := range zr.File {
		if m := reSheet.FindStringSubmatch(strings.ToLower(f.Name)); m != nil {
			n, _ := strconv.Atoi(m[1])
			numbered = append(numbered, numberedSheet{num: n, path: f.Name})
		}
	}
	sort.Slice(numbered, func(i, j int) bool { return numbered[i].num < numbered[j].num })
	for _, item := range numbered {
		sheetFiles = append(sheetFiles, item.path)
	}

	res := &XlsxResult{Type: "xlsx"}
	for idx, path := range sheetFiles {
		raw, err := readZipFile(zr, path)
		if err != nil {
			continue
		}
		rows := parseWorksheetRows(raw, sharedStrings)
		name := fmt.Sprintf("Sheet%d", idx+1)
		if idx < len(sheetNames) && sheetNames[idx] != "" {
			name = sheetNames[idx]
		}
		res.Sheets = append(res.Sheets, XlsxSheet{
			Name: name,
			Rows: rows,
		})
	}
	return res, nil
}

func parseSharedStrings(zr *zip.ReadCloser) ([]string, error) {
	raw, err := readZipFile(zr, "xl/sharedStrings.xml")
	if err != nil {
		return nil, err
	}
	dec := xml.NewDecoder(bytes.NewReader(raw))
	var out []string
	var inSI, inT bool
	var buf strings.Builder
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return out, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "si":
				inSI = true
				buf.Reset()
			case "t":
				if inSI {
					inT = true
				}
			}
		case xml.CharData:
			if inT {
				buf.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inT = false
			case "si":
				out = append(out, buf.String())
				inSI = false
			}
		}
	}
	return out, nil
}

func parseWorkbookSheetNames(zr *zip.ReadCloser) ([]string, error) {
	raw, err := readZipFile(zr, "xl/workbook.xml")
	if err != nil {
		return nil, err
	}
	dec := xml.NewDecoder(bytes.NewReader(raw))
	var names []string
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return names, err
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "sheet" {
			for _, attr := range se.Attr {
				if attr.Name.Local == "name" {
					names = append(names, attr.Value)
					break
				}
			}
		}
	}
	return names, nil
}

func parseWorksheetRows(raw []byte, sharedStrings []string) [][]string {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	var rows [][]string
	var currentRow []string
	var inRow, inCell, inVal, inText bool
	var cellType, cellRef string
	var valBuf strings.Builder

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "row":
				inRow = true
				currentRow = nil
			case "c":
				if inRow {
					inCell = true
					cellType = ""
					cellRef = ""
					valBuf.Reset()
					for _, attr := range t.Attr {
						switch attr.Name.Local {
						case "t":
							cellType = attr.Value
						case "r":
							cellRef = attr.Value
						}
					}
				}
			case "v":
				if inCell {
					inVal = true
				}
			case "t":
				if inCell {
					inText = true
				}
			}
		case xml.CharData:
			if inVal || inText {
				valBuf.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v":
				inVal = false
			case "t":
				inText = false
			case "c":
				if inRow && inCell {
					val := valBuf.String()
					if cellType == "s" {
						if idx, err := strconv.Atoi(strings.TrimSpace(val)); err == nil && idx >= 0 && idx < len(sharedStrings) {
							val = sharedStrings[idx]
						}
					} else if cellType == "b" {
						if val == "1" {
							val = "TRUE"
						} else {
							val = "FALSE"
						}
					}
					colIdx := colIndexFromRef(cellRef)
					if colIdx >= 0 {
						for len(currentRow) <= colIdx {
							currentRow = append(currentRow, "")
						}
						currentRow[colIdx] = val
					} else {
						currentRow = append(currentRow, val)
					}
					inCell = false
				}
			case "row":
				if inRow {
					rows = append(rows, currentRow)
					inRow = false
				}
			}
		}
	}
	return rows
}

func colIndexFromRef(ref string) int {
	if ref == "" {
		return -1
	}
	col := 0
	found := false
	for _, ch := range ref {
		if ch >= 'A' && ch <= 'Z' {
			found = true
			col = col*26 + int(ch-'A'+1)
		} else if ch >= 'a' && ch <= 'z' {
			found = true
			col = col*26 + int(ch-'a'+1)
		} else {
			break
		}
	}
	if !found {
		return -1
	}
	return col - 1
}

func parsePptx(filePath string) (*PptxResult, error) {
	zr, err := zip.OpenReader(filePath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	reSlide := regexp.MustCompile(`^ppt/slides/slide(\d+)\.xml$`)
	type numberedSlide struct {
		num  int
		path string
	}
	var slides []numberedSlide
	for _, f := range zr.File {
		if m := reSlide.FindStringSubmatch(strings.ToLower(f.Name)); m != nil {
			n, _ := strconv.Atoi(m[1])
			slides = append(slides, numberedSlide{num: n, path: f.Name})
		}
	}
	sort.Slice(slides, func(i, j int) bool { return slides[i].num < slides[j].num })

	res := &PptxResult{Type: "pptx"}
	for _, s := range slides {
		raw, err := readZipFile(zr, s.path)
		if err != nil {
			continue
		}
		lines := extractPptxParagraphs(raw)
		title := fmt.Sprintf("第 %d 页", s.num)
		body := lines
		if len(lines) > 0 {
			title = lines[0]
			body = lines[1:]
		}
		res.Slides = append(res.Slides, PptxSlide{
			Number: s.num,
			Title:  title,
			Lines:  body,
		})
	}
	return res, nil
}

func extractPptxParagraphs(raw []byte) []string {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	var lines []string
	var inPara, inText bool
	var buf strings.Builder
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "p":
				inPara = true
				buf.Reset()
			case "t":
				if inPara {
					inText = true
				}
			}
		case xml.CharData:
			if inText {
				buf.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "p":
				if inPara {
					line := strings.TrimSpace(buf.String())
					if line != "" {
						lines = append(lines, line)
					}
					inPara = false
				}
			}
		}
	}
	return lines
}

func BaseNameNoExt(name string) string {
	ext := filepath.Ext(name)
	return strings.TrimSuffix(name, ext)
}
