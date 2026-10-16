package report

import (
	"bytes"
	_ "embed"
	"fmt"
	"strings"
	"time"
	_ "time/tzdata" // the distroless image has no zoneinfo
	"unicode"

	"github.com/go-pdf/fpdf"

	"pillgaze/internal/model"
	"pillgaze/internal/regimens"
)

// Liberation Sans (SIL OFL 1.1, fonts/LICENSE-Liberation.txt) covers Czech
// and Ukrainian; the built-in PDF fonts do not.
var (
	//go:embed fonts/LiberationSans-Regular.ttf
	fontRegular []byte
	//go:embed fonts/LiberationSans-Bold.ttf
	fontBold []byte
)

const (
	font      = "sans"
	margin    = 15.0
	pageW     = 210.0
	pageH     = 297.0
	bottom    = 18.0
	lineH     = 4.6
	cellPad   = 1.6
	bodySize  = 9.5
	smallSize = 8
)

// Column widths in mm, summing to the 180 mm text width.
var colW = []float64{44, 46, 22, 43, 25}

type rgb struct{ r, g, b int }

var (
	ink     = rgb{20, 24, 31}
	muted   = rgb{90, 98, 110}
	rule    = rgb{205, 210, 218}
	headBg  = rgb{238, 241, 245}
	levelBg = map[string]rgb{
		"major":    {190, 30, 45},
		"moderate": {214, 120, 0},
		"minor":    {70, 110, 170},
		"unknown":  {110, 118, 130},
		"same":     {214, 120, 0},
	}
)

var prague = mustZone("Europe/Prague")

func mustZone(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return loc
}

// Render builds the PDF. lang must be a key of langs.
func Render(meds []model.SharedMedication, c Check, lang string, now time.Time) ([]byte, error) {
	t := langs[lang]
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddUTF8FontFromBytes(font, "", fontRegular)
	pdf.AddUTF8FontFromBytes(font, "B", fontBold)
	pdf.SetMargins(margin, margin, margin)
	pdf.SetAutoPageBreak(true, bottom)
	pdf.SetCellMargin(cellPad)
	pdf.SetCreationDate(now)
	pdf.SetModificationDate(now)
	pdf.SetTitle(fmt.Sprintf(t.Title, len(meds)), true)
	pdf.SetProducer("PillGaze", true)
	pdf.AliasNbPages("{nb}")
	pdf.SetFooterFunc(func() {
		pdf.SetY(-12)
		setFont(pdf, "", smallSize, muted)
		pdf.CellFormat(120, 4, t.Footer, "", 0, "L", false, 0, "")
		pdf.CellFormat(0, 4, fmt.Sprintf(t.Page, pdf.PageNo()), "", 0, "R", false, 0, "")
	})
	pdf.AddPage()

	setFont(pdf, "B", 16, ink)
	pdf.CellFormat(0, 8, fmt.Sprintf(t.Title, len(meds)), "", 1, "L", false, 0, "")
	setFont(pdf, "", smallSize+0.5, muted)
	pdf.CellFormat(0, 4.5, fmt.Sprintf(t.Generated, now.In(prague).Format("2. 1. 2006 15:04")), "", 1, "L", false, 0, "")
	pdf.CellFormat(0, 4.5, t.Anonymous, "", 1, "L", false, 0, "")
	pdf.Ln(4)

	medsTable(pdf, meds, c, t)
	pdf.Ln(6)
	interactionsSection(pdf, meds, c, t)

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type line struct {
	text string
	bold bool
}

func medsTable(pdf *fpdf.Fpdf, meds []model.SharedMedication, c Check, t texts) {
	header := func() {
		setFont(pdf, "B", bodySize, ink)
		pdf.SetFillColor(headBg.r, headBg.g, headBg.b)
		for i, h := range []string{t.ColMed, t.ColSubstance, t.ColStrength, t.ColDosage, t.ColSource} {
			pdf.CellFormat(colW[i], 7, h, "", 0, "L", true, 0, "")
		}
		pdf.Ln(-1)
	}
	header()

	for _, m := range meds {
		cells := [][]line{
			wrap(pdf, colW[0], line{m.Name, true}),
			wrap(pdf, colW[1], substanceLines(m, c, t)...),
			wrap(pdf, colW[2], line{val(m.Strength), false}),
			wrap(pdf, colW[3], line{dosage(m), false}),
			wrap(pdf, colW[4], line{sourcesText(m, t), false}),
		}
		if m.Kind == model.KindRegimen {
			cells[0] = append(cells[0], wrap(pdf, colW[0], line{t.Regimen, false})...)
		}
		rows := 0
		for _, cl := range cells {
			rows = max(rows, len(cl))
		}
		h := float64(rows)*lineH + 2*cellPad
		if pdf.GetY()+h > pageH-bottom {
			pdf.AddPage()
			header()
		}
		x0, y0 := pdf.GetXY()
		x := x0
		for i, cl := range cells {
			for j, l := range cl {
				style := ""
				if l.bold {
					style = "B"
				}
				setFont(pdf, style, bodySize, ink)
				pdf.SetXY(x, y0+cellPad+float64(j)*lineH)
				pdf.CellFormat(colW[i], lineH, l.text, "", 0, "L", false, 0, "")
			}
			x += colW[i]
		}
		pdf.SetDrawColor(rule.r, rule.g, rule.b)
		pdf.Line(x0, y0+h, x0+pageW-2*margin, y0+h)
		pdf.SetXY(x0, y0+h)
	}
}

func substanceLines(m model.SharedMedication, c Check, t texts) []line {
	if m.Kind != model.KindRegimen {
		return []line{{val(m.ActiveSubstance), false}}
	}
	r := c.Regimens[m.ID]
	if r == nil {
		return []line{{t.RegimenUnknown, false}}
	}
	how := t.ByAbbr
	if r.Variants > 1 {
		how += ", " + fmt.Sprintf(t.Variants, r.Variants)
	}
	return []line{{strings.Join(r.Drugs, ", "), false}, {"(" + how + ")", false}}
}

func interactionsSection(pdf *fpdf.Fpdf, meds []model.SharedMedication, c Check, t texts) {
	nameOf := func(id string) string { return nameOf(meds, id) }
	width := pageW - 2*margin

	setFont(pdf, "B", 13, ink)
	pdf.CellFormat(0, 7, fmt.Sprintf(t.Interactions, len(c.Findings)+len(c.Duplicates)), "", 1, "L", false, 0, "")
	pdf.Ln(1)

	if len(c.Findings)+len(c.Duplicates) == 0 {
		setFont(pdf, "", bodySize, ink)
		pdf.MultiCell(width, lineH, fmt.Sprintf(t.None, c.CheckedAgainst.Name), "", "L", false)
		pdf.Ln(2)
	}
	for _, f := range c.Findings {
		var body []string
		for _, p := range f.Pairs {
			s := p.A + " × " + p.B
			if len(f.Pairs) > 1 && p.Level != f.Level {
				s += " (" + strings.ToLower(t.Levels[p.Level]) + ")"
			}
			body = append(body, s)
		}
		finding(pdf, f.Level, t.Levels[f.Level], nameOf(f.A)+" × "+nameOf(f.B), body)
	}
	for _, d := range c.Duplicates {
		finding(pdf, "same", t.Levels["same"], nameOf(d.A)+" + "+nameOf(d.B), []string{fmt.Sprintf(t.Duplicate, d.Substance)})
	}

	if len(c.Unchecked) > 0 {
		var body []string
		for _, u := range c.Unchecked {
			reason := t.Reasons[u.Reason]
			if reason == "" {
				reason = u.Reason
			}
			s := u.Name + " – " + reason
			if len(u.Substances) > 0 {
				s += " (" + strings.Join(u.Substances, ", ") + ")"
			}
			body = append(body, s)
		}
		finding(pdf, "unknown", t.UncheckedTag, "", body)
	}

	pdf.Ln(2)
	setFont(pdf, "", smallSize, muted)
	src := c.CheckedAgainst
	note := fmt.Sprintf("%s %s %s (%s, %s, %s). %s", t.CheckedNote, t.SourceLabel, src.Name, src.URL, src.License, src.Version, t.Verify)
	pdf.MultiCell(width, 3.8, clean(note), "", "L", false)
	if c.UsesRegimens {
		pdf.Ln(1)
		rs := regimens.TableSource
		pdf.MultiCell(width, 3.8, clean(fmt.Sprintf("%s %s %s (%s, %s).", t.RegimenNote, t.SourceLabel, rs.Name, rs.URL, rs.Version)), "", "L", false)
	}
}

// finding draws one block: a coloured bar and a text tag (colour is never
// the only signal), an optional bold title and body lines.
func finding(pdf *fpdf.Fpdf, level, tag, title string, body []string) {
	width := pageW - 2*margin - 4
	var lines []line
	setFont(pdf, "B", smallSize, ink)
	lines = append(lines, wrap(pdf, width, line{strings.ToUpper(tag), true})...)
	if title != "" {
		setFont(pdf, "B", bodySize, ink)
		lines = append(lines, wrap(pdf, width, line{title, true})...)
	}
	setFont(pdf, "", bodySize, ink)
	for _, b := range body {
		lines = append(lines, wrap(pdf, width, line{b, false})...)
	}
	h := float64(len(lines))*lineH + 2
	if pdf.GetY()+h > pageH-bottom {
		pdf.AddPage()
	}
	x0, y0 := pdf.GetXY()
	col := levelBg[level]
	pdf.SetFillColor(col.r, col.g, col.b)
	pdf.Rect(x0, y0, 1.4, h-1, "F")
	for i, l := range lines {
		pdf.SetXY(x0+4, y0+float64(i)*lineH)
		switch {
		case i == 0:
			setFont(pdf, "B", smallSize, col)
		case l.bold:
			setFont(pdf, "B", bodySize, ink)
		default:
			setFont(pdf, "", bodySize, ink)
		}
		pdf.CellFormat(width, lineH, l.text, "", 0, "L", false, 0, "")
	}
	pdf.SetXY(x0, y0+h+1)
}

// wrap splits each line to the column width, using its own weight.
func wrap(pdf *fpdf.Fpdf, w float64, ls ...line) []line {
	var out []line
	for _, l := range ls {
		style := ""
		if l.bold {
			style = "B"
		}
		size, _ := pdf.GetFontSize()
		pdf.SetFont(font, style, size)
		for _, s := range pdf.SplitText(clean(l.text), w) {
			out = append(out, line{s, l.bold})
		}
	}
	return out
}

func setFont(pdf *fpdf.Fpdf, style string, size float64, c rgb) {
	pdf.SetFont(font, style, size)
	pdf.SetTextColor(c.r, c.g, c.b)
}

// clean keeps text inside the Basic Multilingual Plane and drops control
// characters, which the font cannot map (emoji in a typed name would
// otherwise break the width tables).
func clean(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return ' '
		case r > 0xFFFF || unicode.IsControl(r) || r == unicode.ReplacementChar:
			return -1
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

func val(s *string) string {
	if s == nil || strings.TrimSpace(*s) == "" {
		return "—"
	}
	return *s
}

func dosage(m model.SharedMedication) string {
	var parts []string
	for _, s := range []*string{m.Frequency, m.Amount} {
		if s != nil && strings.TrimSpace(*s) != "" {
			parts = append(parts, *s)
		}
	}
	if len(parts) == 0 {
		return "—"
	}
	return strings.Join(parts, " · ")
}

func sourcesText(m model.SharedMedication, t texts) string {
	list := m.Sources
	if len(list) == 0 {
		list = []string{m.Source}
	}
	names := map[string]string{model.SourceBox: t.SourceBox, model.SourceNote: t.SourceNote, model.SourceManual: t.SourceManual}
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = names[s]
	}
	text := strings.Join(out, " + ")
	if len(m.EditedFields) > 0 {
		fields := make([]string, len(m.EditedFields))
		for i, f := range m.EditedFields {
			fields[i] = t.Fields[f]
		}
		text += ", " + fmt.Sprintf(t.EditedBy, strings.Join(fields, ", "))
	}
	return text
}
