package output

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/bezilla/capsize/internal/detect"
	"github.com/bezilla/capsize/internal/risk"
	"github.com/bezilla/capsize/internal/units"
)

// TableOptions controls human rendering.
type TableOptions struct {
	NoColor bool
	Top     int
}

// Table renders the report for a human, worst first.
func Table(w io.Writer, r *Report, o TableOptions) error {
	p := palette{on: colorEnabled(w, o.NoColor)}
	b := &strings.Builder{}

	header(b, p, r)
	hiddenSystem(b, p, r)
	contradictionBanner(b, p, r)
	blastRadius(b, p, r, o.Top)
	contradictionDetail(b, p, r)
	findings(b, p, r)
	namespaces(b, p, r)
	summary(b, p, r)

	_, err := io.WriteString(w, b.String())
	return err
}

func section(b *strings.Builder, p palette, title string) {
	fmt.Fprintf(b, "\n%s\n", p.bold(strings.ToUpper(title)))
}

func header(b *strings.Builder, p palette, r *Report) {
	ctx := r.Context
	if ctx == "" {
		ctx = "(current)"
	}
	fmt.Fprintf(b, "%s  context %s  scope %s\n",
		p.bold("capsize"), p.blue(ctx), p.blue(r.Scope))

	if r.MetricsAvailable {
		fmt.Fprintf(b, "%s\n", p.gray("usage data: metrics-server"))
	} else {
		note := r.MetricsNote
		if note == "" {
			note = "unavailable"
		}
		fmt.Fprintf(b, "%s\n", p.gray("usage data: none - "+note+
			"; request-vs-usage findings are unavailable, static findings are unaffected"))
	}
	for _, warn := range r.Warnings {
		fmt.Fprintf(b, "%s %s\n", p.yellow("warning:"), warn)
	}
}

// hiddenSystem states what the default scope left out. capsize has been
// bitten three times by output that was quietly incomplete, so this line is
// unconditional whenever anything was hidden.
func hiddenSystem(b *strings.Builder, p palette, r *Report) {
	w, n := r.HiddenSystemWorkloads, r.HiddenSystemNamespaces
	if w == 0 && n == 0 {
		return
	}
	var parts []string
	if w > 0 {
		parts = append(parts, fmt.Sprintf("%d system workload(s)", w))
	}
	if n > 0 {
		parts = append(parts, fmt.Sprintf("%d system namespace(s)", n))
	}
	fmt.Fprintf(b, "%s\n", p.gray(strings.Join(parts, " and ")+
		" hidden - use --include-system to show them"))
}

func contradictionBanner(b *strings.Builder, p palette, r *Report) {
	cs := r.Contradictions()
	if len(cs) == 0 {
		return
	}
	msg := fmt.Sprintf("%d cost recommendation(s) here would increase blast radius", len(cs))
	fmt.Fprintf(b, "\n%s %s\n", p.red(p.bold("!")), p.bold(msg))
}

func blastRadius(b *strings.Builder, p palette, r *Report, top int) {
	section(b, p, "blast radius")
	if len(r.Rows) == 0 {
		fmt.Fprintf(b, "  %s\n", p.gray("no workloads in scope"))
		return
	}
	hidden := r.Limit(top)

	cols := []string{"RISK", "RATIO", "NBRS", "SPOT", "CEILING", "REQUEST", "KIND", "WORKLOAD", "NODE", "FLAGS"}
	rows := make([][]string, 0, len(r.Rows))
	for _, row := range r.Rows {
		rows = append(rows, tableRow(p, row))
	}
	writeGrid(b, p, cols, rows)

	if hidden > 0 {
		fmt.Fprintf(b, "  %s\n", p.gray(fmt.Sprintf("... %d lower-risk workload(s) hidden by --top", hidden)))
	}
}

func tableRow(p palette, row Row) []string {
	s := row.Score
	if !s.Scored {
		return []string{
			p.gray("-"), p.gray("-"), p.gray("-"), p.gray("-"), p.gray("-"), p.gray("-"),
			string(row.Ref.Kind), row.Ref.Short(), p.gray("-"), p.gray(s.Reason),
		}
	}

	riskCell := units.Score(s.Risk)
	switch {
	case row.Contradictions > 0:
		riskCell = p.red(riskCell)
	case s.CeilingSource == risk.FromNode && s.Neighbors > 0:
		riskCell = p.yellow(riskCell)
	}

	spot := "-"
	if s.Spot {
		spot = p.yellow("yes")
	}

	ceiling := units.Bytes(s.Ceiling)
	if s.CeilingSource == risk.FromNode {
		ceiling = p.yellow(ceiling + "*")
	}

	request := units.Bytes(s.Request)
	if s.RequestAssumed {
		request = p.yellow("~" + request)
	}

	node := s.Node
	if s.Hypothetical {
		node = p.gray(node + " (forecast)")
	}

	var flags []string
	if row.Contradictions > 0 {
		flags = append(flags, p.red("contradiction"))
	}
	if s.SoleTenant() {
		flags = append(flags, p.gray("sole tenant"))
	}
	if row.Findings > 0 {
		flags = append(flags, fmt.Sprintf("%d finding(s)", row.Findings))
	}

	return []string{
		riskCell,
		units.Ratio(s.Ratio),
		strconv.Itoa(s.Neighbors),
		spot,
		ceiling,
		request,
		string(row.Ref.Kind),
		row.Ref.Short(),
		node,
		strings.Join(flags, " "),
	}
}

// writeGrid aligns columns by printable width, so ANSI codes do not throw the
// padding off the way text/tabwriter would.
func writeGrid(b *strings.Builder, p palette, cols []string, rows [][]string) {
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = width(c)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && width(cell) > widths[i] {
				widths[i] = width(cell)
			}
		}
	}

	line := func(cells []string, style func(string) string) {
		b.WriteString("  ")
		for i, cell := range cells {
			if i == len(cells)-1 {
				b.WriteString(style(cell))
			} else {
				b.WriteString(pad(style(cell), widths[i]) + "  ")
			}
		}
		b.WriteString("\n")
	}

	line(cols, p.gray)
	for _, row := range rows {
		line(row, func(s string) string { return s })
	}
}

func contradictionDetail(b *strings.Builder, p palette, r *Report) {
	cs := r.Contradictions()
	if len(cs) == 0 {
		return
	}
	section(b, p, fmt.Sprintf("contradictions (%d)", len(cs)))
	for _, f := range cs {
		fmt.Fprintf(b, "  %s %s\n", p.red(p.bold(f.Ref.Short())),
			p.gray(fmt.Sprintf("(%s, risk %s)", f.Ref.Kind, units.Score(f.Risk))))
		wrapInto(b, f.Detail, "    ", 88)
	}
}

func findings(b *strings.Builder, p palette, r *Report) {
	// Contradictions have their own section immediately above; repeating them
	// here would bury the point in the noise.
	var rest []detect.Finding
	for _, f := range r.Findings {
		if f.Rule != detect.RuleContradiction {
			rest = append(rest, f)
		}
	}
	if len(rest) == 0 {
		return
	}
	section(b, p, fmt.Sprintf("findings (%d)", len(rest)))
	for _, f := range rest {
		subject := f.Namespace
		if f.Ref != nil {
			subject = f.Ref.Short()
		}
		if f.Container != "" {
			subject += "/" + f.Container
		}
		fmt.Fprintf(b, "  %s %s  %s  %s\n",
			severityTag(p, f.Severity), p.gray(f.Rule), p.bold(subject), f.Title)
		wrapInto(b, f.Detail, "    ", 88)
	}
}

func severityTag(p palette, s detect.Severity) string {
	switch s {
	case detect.SeverityCritical:
		return p.red(pad("critical", 8))
	case detect.SeverityWarn:
		return p.yellow(pad("warn", 8))
	default:
		return p.gray(pad("info", 8))
	}
}

func namespaces(b *strings.Builder, p palette, r *Report) {
	if len(r.Namespaces) == 0 {
		return
	}
	section(b, p, fmt.Sprintf("namespaces with no LimitRange and no ResourceQuota (%d)", len(r.Namespaces)))
	for _, ns := range r.Namespaces {
		fmt.Fprintf(b, "  %s %s\n", p.bold(ns.Name),
			p.gray(fmt.Sprintf("(%d workload(s) admitted with no defaults and no cap)", ns.Workloads)))
	}
}

func summary(b *strings.Builder, p palette, r *Report) {
	section(b, p, "summary")
	fmt.Fprintf(b, "  %d workload(s) scored", r.Scored)
	if r.Unscored > 0 {
		fmt.Fprintf(b, ", %d unscored", r.Unscored)
	}
	fmt.Fprintf(b, "; %d finding(s): %s critical, %s warn, %s info",
		r.Counts.Total(),
		p.red(strconv.Itoa(r.Counts.Critical)),
		p.yellow(strconv.Itoa(r.Counts.Warn)),
		p.gray(strconv.Itoa(r.Counts.Info)))
	if n := len(r.Contradictions()); n > 0 {
		fmt.Fprintf(b, ", %s of which %s a cost fix that raises blast radius",
			p.red(strconv.Itoa(n)), plural(n, "is", "are"))
	}
	b.WriteString("\n")

	if r.MaxRiskRef != nil {
		fmt.Fprintf(b, "  highest blast radius: %s %s\n",
			p.bold(units.Score(r.MaxRisk)), p.gray("("+r.MaxRiskRef.Short()+")"))
	}
	fmt.Fprintf(b, "  %s\n", p.gray("* ceiling is the node's allocatable memory because no container limit bounds it"))
}

// wrapInto word-wraps text to width, prefixing every line with indent.
func wrapInto(b *strings.Builder, text, indent string, wrapWidth int) {
	words := strings.Fields(text)
	if len(words) == 0 {
		return
	}
	line := indent
	lineLen := 0
	for _, w := range words {
		if lineLen > 0 && lineLen+1+len(w) > wrapWidth {
			b.WriteString(line + "\n")
			line, lineLen = indent, 0
		}
		if lineLen > 0 {
			line += " "
			lineLen++
		}
		line += w
		lineLen += len(w)
	}
	b.WriteString(line + "\n")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
