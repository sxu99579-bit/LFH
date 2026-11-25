package plugins

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

func builtinMarketplaceCatalog() []MarketplaceItem {
	return []MarketplaceItem{
		{
			ID:             PluginOCRVision,
			Name:           "OCR 图文识别",
			Version:        "1.0.0",
			Author:         "QuailNest Official",
			Description:    "识别图片中的文字与截图元数据，自动生成 .ocr.txt 提取文本并回传入库；本机若安装 tesseract 可自动调用本地识别引擎。",
			Category:       "图像识别 / OCR",
			Capabilities:   []string{CapOCRImage},
			SupportedExts:  []string{".png", ".jpg", ".jpeg", ".bmp", ".webp", ".gif"},
			TriggerModes:   []string{"manual", "auto_upload"},
			Runtime:        RuntimeBuiltin,
			Requirements:   "零依赖内置图像与 PNG 文本块解析；可选 tesseract CLI 增强识别",
			Tags:           []string{"OCR", "图像", "文字提取"},
			MaxConcurrency: 2,
		},
		{
			ID:             PluginAutoOrganizer,
			Name:           "智能文件整理",
			Version:        "1.0.0",
			Author:         "QuailNest Official",
			Description:    "基于文件名、格式特征与正文关键词分析文件语义类别，输出推荐虚拟分类目录与标签摘要（.organize.json）。",
			Category:       "效率与归档",
			Capabilities:   []string{CapAutoClassify},
			SupportedExts:  []string{"*"},
			TriggerModes:   []string{"manual", "auto_upload", "schedule"},
			Runtime:        RuntimeBuiltin,
			Requirements:   "纯 Go 零外部依赖引擎",
			Tags:           []string{"分类", "智能整理", "标签"},
			MaxConcurrency: 4,
		},
		{
			ID:             PluginPDFToWord,
			Name:           "PDF 转 Word (DOCX)",
			Version:        "1.0.0",
			Author:         "QuailNest Official",
			Description:    "将 PDF 文档提取正文结构并转换为标准 Microsoft Word (.docx) 文档；本机若安装 LibreOffice (soffice) 可优先使用排版级无损转换。",
			Category:       "文档转换",
			Capabilities:   []string{CapPDFToDocx},
			SupportedExts:  []string{".pdf"},
			TriggerModes:   []string{"manual"},
			Runtime:        RuntimeBuiltin,
			Requirements:   "内置纯 Go PDF 流解压与 OOXML (.docx) 打包器；可选 LibreOffice 增强转换",
			Tags:           []string{"PDF", "Word", "DOCX", "文档转换"},
			MaxConcurrency: 2,
		},
	}
}

func runBuiltinPlugin(ctx context.Context, m Manifest, req ExecRequest) (ExecResponse, error) {
	switch m.ID {
	case PluginOCRVision:
		return runBuiltinOCR(ctx, req)
	case PluginAutoOrganizer:
		return runBuiltinOrganizer(req)
	case PluginPDFToWord:
		return runBuiltinPDFToWord(ctx, req)
	default:
		return ExecResponse{}, fmt.Errorf("未知内置插件: %s", m.ID)
	}
}

func runExecPlugin(ctx context.Context, m Manifest, req ExecRequest) (ExecResponse, error) {
	cmdPath := strings.TrimSpace(m.Command)
	if cmdPath == "" {
		return ExecResponse{}, fmt.Errorf("插件 %s 未配置可执行命令", m.ID)
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return ExecResponse{}, err
	}
	cmd := exec.CommandContext(ctx, cmdPath, m.Args...)
	cmd.Dir = req.WorkDir
	cmd.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return ExecResponse{}, fmt.Errorf("外部插件 %s 执行失败: %s", m.ID, msg)
	}
	var out ExecResponse
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return ExecResponse{}, fmt.Errorf("解析插件 %s 输出失败: %w", m.ID, err)
	}
	cleanWork := filepath.Clean(req.WorkDir)
	cleanOut := filepath.Clean(out.OutputPath)
	rel, relErr := filepath.Rel(cleanWork, cleanOut)
	if out.OutputPath == "" || relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ExecResponse{}, errors.New("外部插件输出路径无效（禁止越出工作目录）")
	}
	if strings.TrimSpace(out.OutputName) == "" {
		out.OutputName = filepath.Base(cleanOut)
	}
	return out, nil
}

// ==================== Builtin 1: OCR Vision ====================

func runBuiltinOCR(ctx context.Context, req ExecRequest) (ExecResponse, error) {
	raw, err := os.ReadFile(req.InputPath)
	if err != nil {
		return ExecResponse{}, err
	}
	base := strings.TrimSuffix(filepath.Base(req.FileName), filepath.Ext(req.FileName))
	if base == "" {
		base = "image"
	}
	outName := base + ".ocr.txt"
	outPath := filepath.Join(req.WorkDir, outName)

	// Prefer native tesseract CLI if installed on compute node.
	if tesseractBin, err := exec.LookPath("tesseract"); err == nil {
		outBase := filepath.Join(req.WorkDir, "ocr-out")
		for _, lang := range []string{"chi_sim+eng", "eng"} {
			cmd := exec.CommandContext(ctx, tesseractBin, req.InputPath, outBase, "-l", lang)
			if runErr := cmd.Run(); runErr == nil {
				if txtBytes, readErr := os.ReadFile(outBase + ".txt"); readErr == nil {
					text := strings.TrimSpace(string(txtBytes))
					if text != "" {
						header := fmt.Sprintf("# OCR 识别结果（引擎: Tesseract %s）\n# 源文件: %s\n\n%s\n", lang, req.FileName, text)
						if wErr := os.WriteFile(outPath, []byte(header), 0644); wErr != nil {
							return ExecResponse{}, wErr
						}
						return ExecResponse{
							OutputName: outName,
							OutputPath: outPath,
							Summary:    fmt.Sprintf("已通过本地 Tesseract (%s) 提取 %d 字符文本", lang, len([]rune(text))),
						}, nil
					}
				}
			}
		}
	}

	// Zero-dependency fallback: image geometry + embedded PNG text chunks.
	var cfgInfo string
	if cfg, format, decErr := image.DecodeConfig(bytes.NewReader(raw)); decErr == nil {
		cfgInfo = fmt.Sprintf("格式: %s | 分辨率: %dx%d px", strings.ToUpper(format), cfg.Width, cfg.Height)
	} else {
		cfgInfo = fmt.Sprintf("扩展名: %s | 大小: %d 字节", strings.ToLower(filepath.Ext(req.FileName)), len(raw))
	}

	pngTexts := extractPNGTextChunks(raw)
	var b strings.Builder
	b.WriteString(fmt.Sprintf("OCR 图文提取报告 — %s\n", req.FileName))
	b.WriteString("========================================\n")
	b.WriteString(cfgInfo + "\n")
	b.WriteString(fmt.Sprintf("文件体积: %d 字节\n", len(raw)))
	b.WriteString(fmt.Sprintf("处理时间: %s\n\n", time.Now().Format("2006-01-02 15:04:05")))
	if len(pngTexts) > 0 {
		b.WriteString("提取到内置图片文本块:\n")
		for _, line := range pngTexts {
			b.WriteString("- " + line + "\n")
		}
	} else {
		b.WriteString("内置文本块: 无显式嵌入文字元数据。\n")
		b.WriteString("提示: 若当前算力节点安装 tesseract-ocr，将自动切换本地深层文字识别。\n")
	}

	if err := os.WriteFile(outPath, []byte(b.String()), 0644); err != nil {
		return ExecResponse{}, err
	}
	return ExecResponse{
		OutputName: outName,
		OutputPath: outPath,
		Summary:    fmt.Sprintf("完成图像解析 (%s)，提取 %d 条嵌入文本", cfgInfo, len(pngTexts)),
	}, nil
}

func extractPNGTextChunks(raw []byte) []string {
	sig := []byte{137, 80, 78, 71, 13, 10, 26, 10}
	if len(raw) < 16 || !bytes.Equal(raw[:8], sig) {
		return nil
	}
	var out []string
	pos := 8
	for pos+12 <= len(raw) {
		length := int(binary.BigEndian.Uint32(raw[pos : pos+4]))
		chunkType := string(raw[pos+4 : pos+8])
		dataStart := pos + 8
		dataEnd := dataStart + length
		if length < 0 || dataEnd+4 > len(raw) {
			break
		}
		chunkData := raw[dataStart:dataEnd]
		if chunkType == "tEXt" {
			if idx := bytes.IndexByte(chunkData, 0); idx > 0 {
				key := strings.TrimSpace(string(chunkData[:idx]))
				val := strings.TrimSpace(string(chunkData[idx+1:]))
				if val != "" {
					out = append(out, key+": "+val)
				}
			}
		} else if chunkType == "iTXt" {
			if idx := bytes.IndexByte(chunkData, 0); idx > 0 {
				key := strings.TrimSpace(string(chunkData[:idx]))
				rest := chunkData[idx+1:]
				if len(rest) > 3 && rest[0] == 0 {
					parts := bytes.SplitN(rest[2:], []byte{0}, 3)
					if len(parts) == 3 {
						val := strings.TrimSpace(string(parts[2]))
						if val != "" {
							out = append(out, key+": "+val)
						}
					}
				}
			}
		}
		if chunkType == "IEND" {
			break
		}
		pos = dataEnd + 4
	}
	return out
}

// ==================== Builtin 2: Auto Organizer ====================

func runBuiltinOrganizer(req ExecRequest) (ExecResponse, error) {
	info, err := os.Stat(req.InputPath)
	if err != nil {
		return ExecResponse{}, err
	}
	f, err := os.Open(req.InputPath)
	if err != nil {
		return ExecResponse{}, err
	}
	sampleBuf := make([]byte, 64*1024)
	n, _ := io.ReadFull(f, sampleBuf)
	_ = f.Close()
	sampleText := ""
	if n > 0 && utf8.Valid(sampleBuf[:n]) {
		sampleText = strings.ToLower(string(sampleBuf[:n]))
	}

	nameLow := strings.ToLower(req.FileName)
	ext := strings.ToLower(filepath.Ext(req.FileName))
	category := "通用文件"
	tags := []string{}
	confidence := 0.72

	switch {
	case strings.Contains(nameLow, "invoice") || strings.Contains(nameLow, "发票") || strings.Contains(nameLow, "报销") ||
		strings.Contains(sampleText, "发票代码") || strings.Contains(sampleText, "tax"):
		category = "财务票据"
		tags = append(tags, "财务", "发票")
		confidence = 0.94
	case strings.Contains(nameLow, "contract") || strings.Contains(nameLow, "合同") || strings.Contains(nameLow, "协议") ||
		strings.Contains(sampleText, "甲方") || strings.Contains(sampleText, "乙方"):
		category = "合同协议"
		tags = append(tags, "法务", "合同")
		confidence = 0.93
	case strings.Contains(nameLow, "clip_") || strings.Contains(nameLow, "screenshot") || strings.Contains(nameLow, "截图"):
		category = "截图与剪贴板"
		tags = append(tags, "剪贴板", "临时捕获")
		confidence = 0.92
	case ext == ".log" || strings.Contains(nameLow, "error") || strings.Contains(nameLow, "debug") ||
		strings.Contains(sampleText, "traceback") || strings.Contains(sampleText, "panic:"):
		category = "系统日志与排错"
		tags = append(tags, "日志", "运维")
		confidence = 0.9
	case ext == ".go" || ext == ".js" || ext == ".ts" || ext == ".py" || ext == ".json" || ext == ".yaml" || ext == ".yml" || ext == ".sql" || ext == ".ini":
		category = "源代码与配置"
		tags = append(tags, "研发", strings.TrimPrefix(ext, "."))
		confidence = 0.91
	case ext == ".pdf" || ext == ".doc" || ext == ".docx" || ext == ".ppt" || ext == ".pptx" || ext == ".xls" || ext == ".xlsx" || ext == ".md" || ext == ".txt":
		category = "项目文档与报告"
		tags = append(tags, "办公文档", strings.TrimPrefix(ext, "."))
		confidence = 0.85
	case ext == ".mp4" || ext == ".mov" || ext == ".mkv" || ext == ".mp3" || ext == ".wav" || ext == ".flac":
		category = "影音媒体库"
		tags = append(tags, "多媒体")
		confidence = 0.92
	case ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".gif" || ext == ".webp" || ext == ".bmp":
		category = "图片素材"
		tags = append(tags, "图片")
		confidence = 0.89
	case ext == ".zip" || ext == ".rar" || ext == ".7z" || ext == ".tar" || ext == ".gz":
		category = "归档备份包"
		tags = append(tags, "压缩包")
		confidence = 0.9
	}

	suggestedFolder := fmt.Sprintf("智能整理/%s/%s", category, time.Now().Format("2006-01"))
	result := map[string]any{
		"file_name":        req.FileName,
		"size_bytes":       info.Size(),
		"ext":              ext,
		"category":         category,
		"suggested_folder": suggestedFolder,
		"confidence":       confidence,
		"tags":             tags,
		"analyzed_at":      time.Now().Format(time.RFC3339),
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return ExecResponse{}, err
	}

	base := strings.TrimSuffix(filepath.Base(req.FileName), filepath.Ext(req.FileName))
	if base == "" {
		base = "file"
	}
	outName := base + ".organize.json"
	outPath := filepath.Join(req.WorkDir, outName)
	if err := os.WriteFile(outPath, encoded, 0644); err != nil {
		return ExecResponse{}, err
	}
	return ExecResponse{
		OutputName: outName,
		OutputPath: outPath,
		Summary:    fmt.Sprintf("归类为「%s」-> 建议目录 %s（置信度 %.0f%%）", category, suggestedFolder, confidence*100),
		Metadata:   result,
	}, nil
}

// ==================== Builtin 3: PDF to Word (.docx) ====================

func runBuiltinPDFToWord(ctx context.Context, req ExecRequest) (ExecResponse, error) {
	base := strings.TrimSuffix(filepath.Base(req.FileName), filepath.Ext(req.FileName))
	if base == "" {
		base = "document"
	}
	outName := base + ".docx"
	outPath := filepath.Join(req.WorkDir, outName)

	// 1. Prefer LibreOffice headless conversion when installed on compute node.
	for _, bin := range []string{"soffice", "libreoffice"} {
		if path, err := exec.LookPath(bin); err == nil {
			cmd := exec.CommandContext(ctx, path, "--headless", "--convert-to", "docx", "--outdir", req.WorkDir, req.InputPath)
			if runErr := cmd.Run(); runErr == nil {
				produced := filepath.Join(req.WorkDir, strings.TrimSuffix(filepath.Base(req.InputPath), filepath.Ext(req.InputPath))+".docx")
				if st, statErr := os.Stat(produced); statErr == nil && st.Size() > 0 {
					if produced != outPath {
						_ = os.Rename(produced, outPath)
					}
					return ExecResponse{
						OutputName: outName,
						OutputPath: outPath,
						Summary:    "已使用本地 LibreOffice 无损转换为 Word (.docx)",
					}, nil
				}
			}
		}
	}

	// 2. Zero-dependency pure-Go PDF text extractor + valid OOXML .docx builder.
	raw, err := os.ReadFile(req.InputPath)
	if err != nil {
		return ExecResponse{}, err
	}
	lines := extractPDFTextLines(raw)
	if len(lines) == 0 {
		lines = []string{
			fmt.Sprintf("文档 %s 已由 QuailNest PDF 转 Word 插件转换。", req.FileName),
			"未在该 PDF 中检测到可直接提取的纯文本流（可能为扫描版图像 PDF；建议在算力节点安装 LibreOffice 或配合 OCR 插件）。",
		}
	}
	if err := writeSimpleDocx(outPath, lines); err != nil {
		return ExecResponse{}, err
	}
	return ExecResponse{
		OutputName: outName,
		OutputPath: outPath,
		Summary:    fmt.Sprintf("已提取 %d 行文本并打包为标准 .docx 文档", len(lines)),
	}, nil
}

var (
	rePDFStreamBlock = regexp.MustCompile(`(?s)stream\r?\n(.*?)\r?\n?endstream`)
	rePDFTextShow    = regexp.MustCompile(`\(((?:\\.|[^\\()])*)\)\s*(?:Tj|'|")`)
	rePDFTextArray   = regexp.MustCompile(`(?s)\[(.*?)\]\s*TJ`)
	rePDFLiteralPart = regexp.MustCompile(`\(((?:\\.|[^\\()])*)\)`)
)

func extractPDFTextLines(raw []byte) []string {
	chunks := [][]byte{raw}
	for _, m := range rePDFStreamBlock.FindAllSubmatch(raw, -1) {
		streamData := m[1]
		if zr, err := zlib.NewReader(bytes.NewReader(streamData)); err == nil {
			if dec, readErr := io.ReadAll(io.LimitReader(zr, 8*1024*1024)); readErr == nil && len(dec) > 0 {
				chunks = append(chunks, dec)
			}
			_ = zr.Close()
		} else {
			chunks = append(chunks, streamData)
		}
	}

	seen := make(map[string]bool)
	var lines []string
	appendLine := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		lines = append(lines, s)
	}

	for _, c := range chunks {
		for _, m := range rePDFTextShow.FindAllSubmatch(c, -1) {
			appendLine(decodePDFLiteralString(m[1]))
		}
		for _, m := range rePDFTextArray.FindAllSubmatch(c, -1) {
			var combined strings.Builder
			for _, part := range rePDFLiteralPart.FindAllSubmatch(m[1], -1) {
				combined.WriteString(decodePDFLiteralString(part[1]))
			}
			appendLine(combined.String())
		}
	}
	return lines
}

func decodePDFLiteralString(b []byte) string {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] != '\\' {
			out = append(out, b[i])
			continue
		}
		if i+1 >= len(b) {
			break
		}
		i++
		switch b[i] {
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case '\\', '(', ')':
			out = append(out, b[i])
		default:
			if b[i] >= '0' && b[i] <= '7' {
				val := int(b[i] - '0')
				for j := 0; j < 2 && i+1 < len(b) && b[i+1] >= '0' && b[i+1] <= '7'; j++ {
					i++
					val = val*8 + int(b[i]-'0')
				}
				out = append(out, byte(val))
			} else {
				out = append(out, b[i])
			}
		}
	}
	if len(out) >= 2 && out[0] == 0xFE && out[1] == 0xFF {
		u16 := make([]uint16, 0, (len(out)-2)/2)
		for i := 2; i+1 < len(out); i += 2 {
			u16 = append(u16, binary.BigEndian.Uint16(out[i:i+2]))
		}
		return string(utf16.Decode(u16))
	}
	if utf8.Valid(out) {
		return string(out)
	}
	runes := make([]rune, 0, len(out))
	for _, ch := range out {
		runes = append(runes, rune(ch))
	}
	return string(runes)
}

func writeSimpleDocx(dstPath string, paragraphs []string) error {
	f, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	writeEntry := func(name, content string) error {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = io.WriteString(w, content)
		return err
	}

	const contentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`

	const rootRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`

	const docRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"/>`

	var body strings.Builder
	body.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n")
	body.WriteString(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`)
	for _, p := range paragraphs {
		var esc bytes.Buffer
		_ = xml.EscapeText(&esc, []byte(p))
		body.WriteString(`<w:p><w:r><w:t xml:space="preserve">`)
		body.WriteString(esc.String())
		body.WriteString(`</w:t></w:r></w:p>`)
	}
	body.WriteString(`<w:sectPr/></w:body></w:document>`)

	if err := writeEntry("[Content_Types].xml", contentTypes); err != nil {
		return err
	}
	if err := writeEntry("_rels/.rels", rootRels); err != nil {
		return err
	}
	if err := writeEntry("word/_rels/document.xml.rels", docRels); err != nil {
		return err
	}
	if err := writeEntry("word/document.xml", body.String()); err != nil {
		return err
	}
	return zw.Close()
}
