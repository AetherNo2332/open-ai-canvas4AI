package skills

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func skillZipForTest(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, body := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// 符合 Agent Skills 规范的 SKILL.md 写法都要能导入，名称与简介按 YAML 语义读出，
// 不能把折叠符号、注释或 BOM 当成内容。
func TestSkillPackageMetadataFollowsYAMLFrontmatter(t *testing.T) {
	bom := "\uFEFF"
	cases := []struct {
		name, body, wantName, wantDescription, wantVersion string
	}{
		{"folded description", "---\nname: pdf-tools\ndescription: >\n  Extract text and tables from PDF files.\n  Use when working with PDFs.\n---\n\n# PDF\n", "pdf-tools", "Extract text and tables from PDF files. Use when working with PDFs.", ""},
		{"folded strip", "---\nname: pdf-tools\ndescription: >-\n  Multi line\n  folded.\n---\n", "pdf-tools", "Multi line folded.", ""},
		{"literal block", "---\nname: pdf-tools\ndescription: |\n  Line one\n  line two\n---\n", "pdf-tools", "Line one line two", ""},
		{"quoted multiline", "---\nname: pdf-tools\ndescription: \"Line one\n  line two\"\n---\n", "pdf-tools", "Line one line two", ""},
		{"trailing comment", "---\n# header comment\nname: pdf-tools  # trailing\ndescription: Extract text.\n---\n", "pdf-tools", "Extract text.", ""},
		{"utf-8 bom", bom + "---\nname: pdf-tools\ndescription: Extract text.\n---\n\nBody.\n", "pdf-tools", "Extract text.", ""},
		{"spec extras", "---\nname: pdf-tools\ndescription: Extract text.\nlicense: Apache-2.0\nallowed-tools: Read Bash\ncompatibility: Requires python3\nmetadata:\n  author: someone\n  version: \"1.2\"\n---\n", "pdf-tools", "Extract text.", "1.2"},
		{"numeric version", "---\nname: pdf-tools\ndescription: Extract text.\nmetadata:\n  version: 2\n---\n", "pdf-tools", "Extract text.", "2"},
		{"crlf", "---\r\nname: pdf-tools\r\ndescription: Extract text.\r\n---\r\n", "pdf-tools", "Extract text.", ""},
		// 不是合法 YAML 的旧写法（值里带冒号空格）仍按原先的逐行读法导入。
		{"legacy unquoted colon", "---\nname: pdf-tools\ndescription: Use when: working with PDFs\n---\n", "pdf-tools", "Use when: working with PDFs", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			metadata := parseSkillPackageMetadata([]byte(tc.body))
			if metadata.Name != tc.wantName || metadata.Description != tc.wantDescription || metadata.Version != tc.wantVersion {
				t.Fatalf("metadata = %+v, want name=%q description=%q version=%q", metadata, tc.wantName, tc.wantDescription, tc.wantVersion)
			}
			if _, err := archiveFromMarkdown([]byte(tc.body), "", ""); err != nil {
				t.Fatalf("markdown import failed: %v", err)
			}
			if _, err := archiveFromZip(skillZipForTest(t, map[string]string{"pdf-tools/SKILL.md": tc.body, "pdf-tools/scripts/run.py": "print(1)\n"}), ""); err != nil {
				t.Fatalf("zip import failed: %v", err)
			}
		})
	}
	if metadata := parseSkillPackageMetadata([]byte("---\nname: pdf-tools\ndescription: " + strings.Repeat("长", 600) + "\n---\n")); len([]rune(metadata.Description)) != 500 {
		t.Fatalf("long description must be truncated to the 500-rune limit, got %d", len([]rune(metadata.Description)))
	}
}
