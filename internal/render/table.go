package render

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Align controls a column's justification.
type Align int

const (
	AlignLeft Align = iota
	AlignRight
)

// Column describes one table column.
type Column struct {
	Title string
	Align Align
	// Flex marks the column that absorbs remaining width, and is truncated
	// first when the terminal is narrow. Command lines are the usual case.
	Flex bool
	// Max caps the width even when space is available.
	Max int
}

// Table renders aligned rows.
//
// Alignment is computed from the *unstyled* width of every cell, because a
// coloured cell contains invisible escape bytes that would otherwise throw
// the column padding off by exactly the length of the escape sequence. Cells
// therefore carry their plain text alongside their rendered form.
type Table struct {
	r    *Renderer
	cols []Column
	rows []tableRow
}

// tableRow is either a row of cells or a full-width label introducing the rows
// beneath it.
//
// Labels live inside the table rather than the caller rendering a table per
// group, because a table per group re-measures its own columns: the same field
// would sit at a different offset in every group, and the page would stop
// reading as one list.
type tableRow struct {
	cells []Cell
	label Cell
}

// Cell is one table value: what to print, and how wide it really is.
type Cell struct {
	Text  string // styled, as printed
	Plain string // unstyled, for width computation
}

// C makes an unstyled cell.
func C(s string) Cell { return Cell{Text: s, Plain: s} }

// SC makes a styled cell whose visible width comes from plain.
func SC(styled, plain string) Cell { return Cell{Text: styled, Plain: plain} }

func NewTable(r *Renderer, cols ...Column) *Table {
	return &Table{r: r, cols: cols}
}

func (t *Table) Row(cells ...Cell) {
	t.rows = append(t.rows, tableRow{cells: cells})
}

// Label starts a titled block within the table. The label is printed across
// the full width, above the rows that follow it, and takes no part in column
// sizing.
func (t *Table) Label(styled, plain string) {
	t.rows = append(t.rows, tableRow{label: SC(styled, plain)})
}

// Empty reports whether any rows were added. Labels do not count: a table of
// nothing but headings has nothing to show.
func (t *Table) Empty() bool {
	for _, row := range t.rows {
		if row.label.Plain == "" {
			return false
		}
	}
	return true
}

// Render writes the table.
func (t *Table) Render() {
	if len(t.cols) == 0 {
		return
	}
	widths := t.computeWidths()

	// Header. Suppressed entirely when there is nothing to label.
	wrote := !t.Empty()
	if wrote {
		var parts []string
		for i, c := range t.cols {
			parts = append(parts, pad(c.Title, widths[i], c.Align))
		}
		t.r.Line(t.r.Heading(strings.TrimRight(strings.Join(parts, "  "), " ")))
	}

	for _, row := range t.rows {
		if row.label.Plain != "" {
			// Breathing room above a label, so it reads as introducing the
			// rows below rather than trailing the ones above — and so it is
			// not mistaken for a second line of column headings.
			if wrote {
				t.r.Blank()
			}
			t.r.Line(row.label.Text)
			wrote = true
			continue
		}
		wrote = true
		var parts []string
		for i := range t.cols {
			var cell Cell
			if i < len(row.cells) {
				cell = row.cells[i]
			}
			plain := cell.Plain
			text := cell.Text
			if w := widths[i]; utf8.RuneCountInString(plain) > w {
				plain = Truncate(plain, w)
				// A truncated styled cell would have its escape sequences
				// cut mid-sequence, so fall back to the plain form.
				text = plain
			}
			gap := widths[i] - utf8.RuneCountInString(plain)
			if gap < 0 {
				gap = 0
			}
			switch t.cols[i].Align {
			case AlignRight:
				parts = append(parts, strings.Repeat(" ", gap)+text)
			default:
				parts = append(parts, text+strings.Repeat(" ", gap))
			}
		}
		t.r.Line(strings.TrimRight(strings.Join(parts, "  "), " "))
	}
}

// computeWidths sizes columns to content, then shrinks the flex column to fit
// the terminal.
func (t *Table) computeWidths() []int {
	widths := make([]int, len(t.cols))
	for i, c := range t.cols {
		widths[i] = utf8.RuneCountInString(c.Title)
	}
	for _, row := range t.rows {
		for i := range t.cols {
			if i >= len(row.cells) {
				continue
			}
			if n := utf8.RuneCountInString(row.cells[i].Plain); n > widths[i] {
				widths[i] = n
			}
		}
	}
	for i, c := range t.cols {
		if c.Max > 0 && widths[i] > c.Max {
			widths[i] = c.Max
		}
	}

	total := 0
	for _, w := range widths {
		total += w
	}
	total += 2 * (len(widths) - 1)

	avail := t.r.Width()
	if avail <= 0 || total <= avail {
		return widths
	}
	// Over budget: take it out of the flex column, down to a floor where the
	// value is still recognisable.
	for i, c := range t.cols {
		if !c.Flex {
			continue
		}
		over := total - avail
		const minFlex = 12
		if widths[i]-over >= minFlex {
			widths[i] -= over
		} else {
			widths[i] = minFlex
		}
		break
	}
	return widths
}

func pad(s string, w int, a Align) string {
	gap := w - utf8.RuneCountInString(s)
	if gap < 0 {
		return Truncate(s, w)
	}
	if a == AlignRight {
		return strings.Repeat(" ", gap) + s
	}
	return s + strings.Repeat(" ", gap)
}

// KeyValue writes a simple aligned block, used where a table would be
// overkill (single-record views like `status`).
func (r *Renderer) KeyValue(pairs [][2]string) {
	w := 0
	for _, p := range pairs {
		if n := utf8.RuneCountInString(p[0]); n > w {
			w = n
		}
	}
	for _, p := range pairs {
		fmt.Fprintf(r.w, "  %s  %s\n", r.Muted(pad(p[0], w, AlignLeft)), p[1])
	}
}
