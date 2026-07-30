package chat

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/theme"
)

// Tables, which the renderer used to destroy rather than merely fail to style.
//
// A table's rows fell through to the paragraph branch, where the block was joined with spaces and
// reflowed. Three rows of four columns came out as one run-on line of pipes. That is worse than
// leaving it alone: unstyled markdown is still readable as a table by anybody who has seen one, and
// a reflowed table is not readable as anything.
//
// Models emit tables constantly, because a table is the right shape for most of what a coding agent
// has to say about several things at once, so this is not a rare path.

// tableBlock is a parsed table: a header row, an alignment row, and the body.
type tableBlock struct {
	header []string
	rows   [][]string
	align  []alignment
}

type alignment int

const (
	alignLeft alignment = iota
	alignRight
	alignCentre
)

// isTableStart reports whether a header row and its delimiter begin here.
//
// Both lines are required. A single line of pipes is far more likely to be a shell pipeline inside
// prose than a table with no body, and reading it as a table would put a box around somebody's
// command.
func isTableStart(lines []string) bool {
	if len(lines) < 2 {
		return false
	}
	return len(splitRow(lines[0])) > 1 && isDelimiterRow(lines[1])
}

// isDelimiterRow matches the ---|:---:|--- line under a table header.
func isDelimiterRow(line string) bool {
	cells := splitRow(line)
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		cell = strings.TrimSpace(cell)
		cell = strings.TrimPrefix(cell, ":")
		cell = strings.TrimSuffix(cell, ":")
		if cell == "" || strings.Trim(cell, "-") != "" {
			return false
		}
	}
	return true
}

// splitRow breaks a row into cells on unescaped pipes, dropping the leading and trailing ones.
func splitRow(line string) []string {
	line = strings.TrimSpace(line)
	if !strings.Contains(line, "|") {
		return nil
	}
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")

	var cells []string
	var current strings.Builder
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '\\' && i+1 < len(runes) && runes[i+1] == '|' {
			current.WriteRune('|')
			i++
			continue
		}
		if runes[i] == '|' {
			cells = append(cells, strings.TrimSpace(current.String()))
			current.Reset()
			continue
		}
		current.WriteRune(runes[i])
	}
	cells = append(cells, strings.TrimSpace(current.String()))
	return cells
}

// collectTable reads a table from the head of lines and reports how many it consumed.
func collectTable(lines []string) (tableBlock, int) {
	table := tableBlock{header: splitRow(lines[0])}

	for _, cell := range splitRow(lines[1]) {
		cell = strings.TrimSpace(cell)
		left, right := strings.HasPrefix(cell, ":"), strings.HasSuffix(cell, ":")
		switch {
		case left && right:
			table.align = append(table.align, alignCentre)
		case right:
			table.align = append(table.align, alignRight)
		default:
			table.align = append(table.align, alignLeft)
		}
	}

	n := 2
	for n < len(lines) {
		row := splitRow(lines[n])
		if len(row) == 0 {
			break
		}
		table.rows = append(table.rows, row)
		n++
	}
	return table, n
}

// renderTable lays a table out in columns that fit the width.
//
// Columns are sized to their widest cell and then shrunk proportionally if the total does not fit,
// with the widest column giving up the most. Cells that no longer fit wrap inside their column
// rather than being cut, because a table cell holding half a file path is a table cell holding
// something untrue.
func renderTable(table tableBlock, width int) []string {
	t := theme.Current()

	columns := len(table.header)
	for _, row := range table.rows {
		if len(row) > columns {
			columns = len(row)
		}
	}
	if columns == 0 {
		return nil
	}

	// The plain text of every cell, with inline markers already removed, because a column sized
	// against "**yes**" is two columns wider than the "yes" it will draw.
	header := plainCells(table.header, columns)
	body := make([][]string, 0, len(table.rows))
	for _, row := range table.rows {
		body = append(body, plainCells(row, columns))
	}

	widths := make([]int, columns)
	for i, cell := range header {
		widths[i] = lipgloss.Width(cell)
	}
	for _, row := range body {
		for i, cell := range row {
			if w := lipgloss.Width(cell); w > widths[i] {
				widths[i] = w
			}
		}
	}

	// Two spaces between columns where they fit, one where that is the difference between a table
	// and overflow. If even one cell plus one separator per column cannot fit, no horizontal table
	// exists at this width; render the same information as labelled rows instead.
	gap := 2
	if columns+gap*(columns-1) > width {
		gap = 1
	}
	if columns+gap*(columns-1) > width {
		return renderStackedTable(header, body, width)
	}
	widths = fit(widths, width-gap*(columns-1))

	var out []string
	out = append(out, renderRow(header, widths, table.align, gap, t.Heading)...)

	rule := make([]string, columns)
	for i := range rule {
		rule[i] = strings.Repeat("─", widths[i])
	}
	out = append(out, t.Muted.Render(strings.Join(rule, strings.Repeat(" ", gap))))

	for _, row := range body {
		out = append(out, renderRow(row, widths, table.align, gap, t.Body)...)
	}
	return out
}

// renderStackedTable is the lossless narrow-screen form of a table.
//
// A horizontal table needs at least one cell per column and one separator per boundary. When the
// terminal is narrower than that, pretending it still fits either overflows the frame or gives a
// column zero width. Each record becomes a short labelled list instead. It is taller, but every
// header and value survives and every line remains inside the width.
func renderStackedTable(header []string, body [][]string, width int) []string {
	t := theme.Current()
	if width < 1 {
		width = 1
	}

	rows := body
	if len(rows) == 0 {
		rows = [][]string{make([]string, len(header))}
	}

	var out []string
	for rowIndex, row := range rows {
		if rowIndex > 0 {
			out = append(out, t.Muted.Render(strings.Repeat("─", width)))
		}
		for column, value := range row {
			label := header[column]
			if label == "" {
				label = "column " + strconv.Itoa(column+1)
			}
			out = append(out, renderListItem(&listItem{
				marker: "• ",
				text:   label + ": " + value,
			}, width)...)
		}
	}
	return out
}

// plainCells pads a row to the column count and strips inline markers from each cell.
func plainCells(row []string, columns int) []string {
	out := make([]string, columns)
	for i := range out {
		if i < len(row) {
			out[i] = plainWidthOf(row[i])
		}
	}
	return out
}

// fit shrinks column widths until they add up to the space available.
//
// Proportional rather than equal. Three cells is the preferred floor, reduced as far as one when a
// narrow but still valid horizontal table needs it. A table that cannot give every column one cell
// never reaches this function; renderTable uses the stacked form instead.
func fit(widths []int, available int) []int {
	if len(widths) == 0 {
		return nil
	}
	floor := 3
	if available/len(widths) < floor {
		floor = available / len(widths)
	}
	if floor < 1 {
		floor = 1
	}

	desired := make([]int, len(widths))
	total := 0
	for i, w := range widths {
		if w < 1 {
			w = 1
		}
		desired[i] = w
		total += w
	}
	if total <= available {
		return desired
	}

	out := make([]int, len(widths))
	assigned := 0
	for i, w := range desired {
		scaled := w * available / total
		if scaled < floor {
			scaled = floor
		}
		out[i] = scaled
		assigned += scaled
	}

	// Rounding leaves a few cells over or short. Given to, or taken from, the widest column, which
	// is the one where a cell either way is least visible.
	for assigned > available {
		widest := 0
		for i := range out {
			if out[i] > out[widest] {
				widest = i
			}
		}
		if out[widest] <= floor {
			break
		}
		out[widest]--
		assigned--
	}

	// Proportional rounding can also leave cells unused. Give them to the column furthest below
	// what its content wanted, so the table uses the width it was given without changing order.
	for assigned < available {
		neediest := -1
		for i := range out {
			if out[i] >= desired[i] {
				continue
			}
			if neediest < 0 || desired[i]-out[i] > desired[neediest]-out[neediest] {
				neediest = i
			}
		}
		if neediest < 0 {
			break
		}
		out[neediest]++
		assigned++
	}
	return out
}

// renderRow draws one row, wrapping any cell too tall for a single line.
func renderRow(cells []string, widths []int, align []alignment, gap int, style lipgloss.Style) []string {
	wrapped := make([][]string, len(cells))
	height := 1
	for i, cell := range cells {
		wrapped[i] = wrapLine(cell, widths[i])
		if len(wrapped[i]) == 0 {
			wrapped[i] = []string{""}
		}
		if len(wrapped[i]) > height {
			height = len(wrapped[i])
		}
	}

	var out []string
	for row := 0; row < height; row++ {
		var line strings.Builder
		for i := range cells {
			if i > 0 {
				line.WriteString(strings.Repeat(" ", gap))
			}
			text := ""
			if row < len(wrapped[i]) {
				text = wrapped[i][row]
			}
			line.WriteString(style.Render(pad(text, widths[i], alignOf(align, i))))
		}
		out = append(out, strings.TrimRight(line.String(), " "))
	}
	return out
}

func alignOf(align []alignment, i int) alignment {
	if i < len(align) {
		return align[i]
	}
	return alignLeft
}

// pad sets a cell to its column width, in cells rather than bytes.
func pad(text string, width int, how alignment) string {
	slack := width - lipgloss.Width(text)
	if slack <= 0 {
		return text
	}
	switch how {
	case alignRight:
		return strings.Repeat(" ", slack) + text
	case alignCentre:
		left := slack / 2
		return strings.Repeat(" ", left) + text + strings.Repeat(" ", slack-left)
	default:
		return text + strings.Repeat(" ", slack)
	}
}
