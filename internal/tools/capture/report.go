package capture

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Renderer turns an HTML file into an image or a PDF: the capture engine's
// browser does it, so the report looks exactly like the page.
type Renderer interface {
	Screenshot(htmlFile, pngFile string) error
	PDF(htmlFile, pdfFile string) error
}

// ReportFolder is the folder of the comparison report of two labels.
func ReportFolder(root string, date time.Time, labels ...string) string {
	name := date.Format("2006-01-02")
	for i, l := range labels {
		if i == 0 {
			name += "-" + safeName(l)
		} else {
			name += "-vs-" + safeName(l)
		}
	}
	if !filepath.IsLocal(name) {
		name = "_"
	}
	return filepath.Join(root, filepath.FromSlash(ReportsDir), name)
}

// ReportFiles are the files of a comparison report, relative to its folder.
type ReportFiles struct {
	Dir          string `json:"dir"`
	Summary      string `json:"summary"`
	Compositions string `json:"compositions"`
	Manifest     string `json:"manifest"`
	Corporate    string `json:"corporate,omitempty"`
	CorporatePDF string `json:"corporate_pdf,omitempty"`
}

// WriteComparisonReport writes comparacion.json, resumen.md, the before/after
// image of every state and the gallery composiciones.html into dir. The
// report folder is self-contained: it keeps its own copy of the screenshots.
func WriteComparisonReport(dir string, a, b *Set, c *Comparison, lang string, r Renderer, log io.Writer) (ReportFiles, error) {
	files := ReportFiles{Dir: dir, Summary: "resumen.md", Compositions: "composiciones.html", Manifest: "comparacion.json"}
	if err := os.MkdirAll(filepath.Join(dir, "composiciones", "src"), 0o755); err != nil {
		return files, err
	}
	for _, side := range []struct {
		set  *Set
		name string
	}{{a, "a"}, {b, "b"}} {
		for _, sh := range side.set.Shots {
			if sh.File == "" {
				continue
			}
			from, err := side.set.Path(sh.File)
			if err != nil {
				return files, err
			}
			to, err := Within(filepath.Join(dir, "images", side.name), sh.File)
			if err != nil {
				return files, err
			}
			if err := copyFile(from, to); err != nil {
				return files, err
			}
		}
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return files, err
	}
	if err := os.WriteFile(filepath.Join(dir, files.Manifest), append(data, '\n'), 0o644); err != nil {
		return files, err
	}
	if err := os.WriteFile(filepath.Join(dir, files.Summary), []byte(BuildSummary(c, lang)), 0o644); err != nil {
		return files, err
	}
	for _, s := range c.States {
		if s.Class == ClassNotCapture || s.Class == ClassUnstable {
			continue
		}
		base := strings.TrimSuffix(filepath.Base(CompositionFile(s.Screen, s.State, s.Viewport)), ".png")
		src := filepath.Join(dir, "composiciones", "src", base+".html")
		if err := os.WriteFile(src, []byte(BuildCompositionHTML(c, s, lang)), 0o644); err != nil {
			return files, err
		}
		if r == nil {
			continue
		}
		if err := r.Screenshot(src, filepath.Join(dir, filepath.FromSlash(CompositionFile(s.Screen, s.State, s.Viewport)))); err != nil {
			return files, fmt.Errorf("composition of %s#%s %s: %w", s.Screen, s.State, s.Viewport, err)
		}
	}
	return files, os.WriteFile(filepath.Join(dir, files.Compositions), []byte(BuildGallery(c, lang)), 0o644)
}

// WriteCorporateReport writes the corporate report of one set: HTML and, with a
// renderer, PDF.
func WriteCorporateReport(dir string, s *Set, root, lang string, r Renderer) (htmlFile, pdfFile string, err error) {
	if err = os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	var brand Brand
	if data, rerr := os.ReadFile(filepath.Join(root, ".theo", "capturas", "marca.json")); rerr == nil {
		_ = json.Unmarshal(data, &brand)
	}
	htmlFile = filepath.Join(dir, "corporativo.html")
	if err = os.WriteFile(htmlFile, []byte(BuildCorporate(s, lang, brand, root)), 0o644); err != nil {
		return
	}
	if r != nil {
		pdfFile = filepath.Join(dir, "corporativo.pdf")
		err = r.PDF(htmlFile, pdfFile)
	}
	return
}

func copyFile(from, to string) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(to)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}
