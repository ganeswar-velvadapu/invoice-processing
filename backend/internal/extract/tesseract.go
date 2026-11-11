package extract

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Tesseract runs the tesseract CLI with TSV output and rebuilds layout-preserving lines from word boxes,
// so table columns stay separated by runs of spaces just like `pdftotext -layout`.
type Tesseract struct {
	Bin  string
	Lang string
}

type word struct {
	text                     string
	left, top, width, height int
	conf                     float64
}

func (t *Tesseract) RecognizeImage(ctx context.Context, imagePath string, page int) ([]Line, float64, error) {
	out, err := run(ctx, 3*time.Minute, t.Bin, imagePath, "stdout", "-l", t.Lang, "--psm", "6", "tsv")
	if err != nil {
		return nil, 0, err
	}
	words := parseTSV(string(out))
	lines, mean := assemble(words, page)
	return lines, mean, nil
}

func parseTSV(s string) []word {
	var ws []word
	for i, row := range strings.Split(s, "\n") {
		if i == 0 {
			continue
		}
		f := strings.Split(row, "\t")
		if len(f) < 12 || f[0] != "5" {
			continue
		}
		txt := strings.TrimSpace(f[11])
		if txt == "" {
			continue
		}
		atoi := func(x string) int { v, _ := strconv.Atoi(x); return v }
		conf, _ := strconv.ParseFloat(f[10], 64)
		ws = append(ws, word{text: txt, left: atoi(f[6]), top: atoi(f[7]), width: atoi(f[8]), height: atoi(f[9]), conf: conf})
	}
	return ws
}

// assemble groups words into visual rows by vertical overlap, then lays them out on a character grid.
func assemble(ws []word, page int) ([]Line, float64) {
	if len(ws) == 0 {
		return nil, 0
	}
	// estimate character width from word geometry
	var totalW, totalC int
	var heights []int
	for _, w := range ws {
		totalW += w.width
		totalC += len([]rune(w.text))
		heights = append(heights, w.height)
	}
	charW := float64(totalW) / float64(max(totalC, 1))
	if charW < 1 {
		charW = 1
	}
	sort.Ints(heights)
	medH := heights[len(heights)/2]

	sort.Slice(ws, func(i, j int) bool { return ws[i].top+ws[i].height/2 < ws[j].top+ws[j].height/2 })
	type row struct {
		words  []word
		center float64
	}
	var rows []*row
	for _, w := range ws {
		c := float64(w.top) + float64(w.height)/2
		if n := len(rows); n > 0 && c-rows[n-1].center < float64(medH)*0.6 {
			r := rows[n-1]
			r.words = append(r.words, w)
			r.center = (r.center*float64(len(r.words)-1) + c) / float64(len(r.words))
			continue
		}
		rows = append(rows, &row{words: []word{w}, center: c})
	}
	var lines []Line
	var confSum float64
	var confN int
	for _, r := range rows {
		sort.Slice(r.words, func(i, j int) bool { return r.words[i].left < r.words[j].left })
		var b strings.Builder
		x0, y0, x1, y1 := r.words[0].left, r.words[0].top, 0, 0
		var lc float64
		for _, w := range r.words {
			col := int(float64(w.left) / charW)
			cur := len([]rune(b.String()))
			pad := col - cur
			if cur > 0 && pad < 1 {
				pad = 1
			}
			b.WriteString(strings.Repeat(" ", max(pad, 0)))
			b.WriteString(w.text)
			x1 = max(x1, w.left+w.width)
			y0 = min(y0, w.top)
			y1 = max(y1, w.top+w.height)
			lc += w.conf
			if w.conf >= 0 {
				confSum += w.conf
				confN++
			}
		}
		bb := [4]int{x0, y0, x1, y1}
		lines = append(lines, Line{Page: page, Text: b.String(), BBox: &bb, Conf: lc / float64(len(r.words))})
	}
	mean := 0.0
	if confN > 0 {
		mean = confSum / float64(confN)
	}
	return lines, mean
}
